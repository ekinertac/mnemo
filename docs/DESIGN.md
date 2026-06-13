# Mnemo — Design

> **Mnemo** syncs your Claude Code sessions across machines as encrypted, deduplicated,
> *append-only snapshots* — and lays them back down where `claude --resume` can find them,
> regardless of where each project lives on each machine.
>
> Named for Mnemosyne (memory): your sessions are working memory, carried between machines.

This document is the architecture and rationale. It is written to be the first thing a new
contributor (human or agent) reads. Pair it with `HANDOFF.md`.

---

## 1. Why Mnemo exists (what was wrong with `claude-sync`)

Mnemo is a clean-room successor to `tawanorg/claude-sync`, written after using that tool
heavily for a real multi-machine (macOS + Windows) setup. claude-sync *works*, but its
foundations cause recurring, sometimes data-losing, failure modes. Every one of these was hit
in practice:

| Problem in claude-sync | Consequence we observed |
|---|---|
| **`state.json` is a sidecar index that diverges from reality.** Push decides what to upload by hashing local files against `state.json`, never against the live remote. | After editing the bucket directly, push uploaded 9 files instead of 1,134 — it was blind to remote changes. There is no `verify`, no `repair`, no `push --force`; the only fix was hand-editing `state.json`. |
| **"Sync" is a destructive mirror, coupled to local retention.** Push deletes remote files that are absent locally. | Claude's 30-day `cleanupPeriodDays` purged old transcripts locally, the next push propagated those deletions to the bucket, and a 30-day storage lifecycle then hard-deleted them — **440 transcripts lost.** |
| **No merge for append-only data.** Last-writer-wins per file. | `history.jsonl` conflicts can't be reconciled; a reported conflict didn't even write the promised `.conflict` file. |
| **Per-file objects at scale.** Thousands of tiny individually-encrypted objects (many 33 bytes). | 2,230 objects per push; slow, per-request-costly, non-atomic (an interrupted push leaves the index inconsistent). Ephemeral scratch (subagent/workflow/tool-result files) synced as if durable. |
| **Encryption defeats content-addressing.** Hashes plaintext locally, stores non-deterministic age ciphertext. | Can't verify or dedup remote objects without decrypting; no integrity guarantees. |
| **Identity = filesystem path.** Projects keyed by the encoded absolute cwd (`-Users-ekinertac-Code-foo`). | Cross-machine resume only works by the *coincidence* that both machines use the same `~/Code/foo` layout. The `${HOME}` tokenization claude-sync added is a clever patch over the absence of real project identity. |

The deep mistake is modeling the task as **file mirroring**. The right model is
**content-addressed, snapshot-based backup** — which is exactly what `restic` already is.

---

## 2. Principles

1. **Truth is content, not a sidecar index.** State is derived from the snapshot graph and the
   filesystem, never from a mutable cache that can silently lie.
2. **Additive by default.** A backup never deletes remote data because local data is missing.
   Deletion happens only via an explicit, retention-governed `prune`.
3. **Snapshots, not mirrors.** Every push is an immutable, timestamped snapshot. History and
   time-travel are free; "I lost a session" becomes a restore, not a tragedy.
4. **Identity by project, not by path.** A project is identified by its git origin (or an
   explicit identity), and *mapped* to whatever local path it occupies on each machine.
5. **Merge-aware for append-only logs.** `.jsonl` transcripts and `history.jsonl` are event
   logs; reconcile by union, never by clobber.
6. **Know what's ephemeral.** Subagent scratch, workflow temp, tool-results, lock/watermark
   files are not durable session data and are skipped by default.
7. **Reuse a hardened engine.** Don't re-implement encryption, dedup, integrity, or retention.
   Wrap `restic`. Write only the Claude-specific layer.
8. **No interactive prompts — ever.** Every command runs to completion or fails with a clear
   message and non-zero exit. No wizards, no confirmation prompts, no interactive conflict
   menus. All input comes from flags, environment, and a config file; all decisions are
   resolved by flags and deterministic rules. Mnemo must be safe to run from cron, CI, a hook,
   or an agent with no TTY. (claude-sync's interactive `init`/`pull`/`conflicts` were a direct
   pain point — they hang in non-TTY contexts.)
9. **Sessions only — never configuration.** Mnemo syncs *conversation/session data* and nothing
   else. It deliberately does **not** touch MCP server configs, skills, agents, plugins, rules,
   `settings.json`, or `CLAUDE.md`. Those are capabilities/config — often machine-specific or
   identity-bearing — and are the user's to manage per machine. This is a hard boundary, not a
   toggle; there is no "full" mode. (claude-sync synced all of it, including `plugins/` caches
   with arch-specific `node_modules`/`.venv` — a portability and footgun nightmare.)

---

## 3. Architecture

```
                         ┌─────────────────────────────────────────┐
                         │                 mnemo                    │
                         │           (Claude-aware layer)           │
                         │                                          │
   ~/.claude/  ───────►  │  1. ephemeral filter (what to back up)   │
   projects/             │  2. project-identity map (origin ⇄ path) │  ◄── projects.json
   tasks/ plans/         │  3. transcript append-merge (.jsonl)     │      (in repo)
   history.jsonl         │  4. resume-aware restore (lay-down)      │
                         │                                          │
                         └───────────────────┬──────────────────────┘
                                             │ shells out to
                                             ▼
                         ┌─────────────────────────────────────────┐
                         │                 restic                   │
                         │  encryption · dedup · snapshots ·        │
                         │  integrity (check) · retention (prune)   │
                         └───────────────────┬──────────────────────┘
                                             ▼
        Any restic backend — S3-compatible (AWS S3, Backblaze B2, MinIO,
        Wasabi, DigitalOcean Spaces, Ceph, …), native B2/Azure/GCS,
        SFTP/REST server, or rclone (which opens ~everything else)
```

**restic owns** the dangerous, solved parts: AES-256 encryption, content-defined chunking +
dedup (huge win over per-file objects — thousands of tiny files pack into a few blobs),
immutable snapshots, `check` for integrity, `forget --prune` for retention.

**Mnemo owns** only what is Claude-specific:
1. deciding *what* to hand restic (durable vs ephemeral),
2. the project-identity ⇄ local-path mapping,
3. append-merge for event logs,
4. restoring sessions to the path *this* machine's Claude Code expects.

**Implementation:** Go, single binary. **Shell out to the `restic` binary** initially
(stable CLI, trivial to reason about) rather than vendoring restic as a library (its internal
API is not a stability contract). Revisit embedding later if startup cost matters.

---

## 4. Data model

### 4.1 What gets backed up — three categories

Mnemo is **sessions-only** (principle 9). Everything in `~/.claude` falls into exactly one of:

**(A) In scope — session data (synced):**
- `projects/<id>/<session>.jsonl` — the session transcripts (the whole point)
- `projects/<id>/memory/` — per-project working memory (produced during sessions)
- `plans/` — plan-mode plans (session work product)
- `tasks/*.json` — task state (durable part only; see ephemeral below)
- `history.jsonl` — prompt history (the one cross-cutting log; merged per §5.3)

**(B) Ephemeral — skipped (regenerated on demand, no lasting value):**
- `projects/*/subagents/**`, `projects/*/**/workflows/**` — subagent & workflow scratch
- `projects/*/**/tool-results/**` — tool output dumps
- `tasks/*/.lock`, `tasks/*/.highwatermark` — runner state
- anything matching user `exclude` globs

**(C) Configuration / capabilities — NEVER synced (hard boundary, not a toggle):**
- `mcp` server configs (`~/.claude.json`), `settings.json`, `settings.local.json`
- `skills/`, `agents/`, `plugins/`, `rules/`, `CLAUDE.md`

Category (C) is the explicit departure from claude-sync. These are machine-specific or
identity-bearing config, not conversation data; mirroring them across machines causes breakage
(e.g. `plugins/` caches bundle arch-specific `node_modules`/`.venv`) and isn't Mnemo's job.
Together, dropping (B) and (C) is what keeps snapshots small, portable, and meaningful — and
removes the bulk of the object count (one observed claude-sync push moved 2,230 objects; the
vast majority were (B)/(C) noise).

### 4.2 restic repository

One restic repo per user (shared by all their machines), on **whatever backend the user
chooses** — Mnemo is **backend-agnostic**. Any restic backend works:

- **S3-compatible (first-class, the common case):** AWS S3, Backblaze B2, MinIO, Wasabi,
  DigitalOcean Spaces, Ceph/RGW, or any self-hosted S3 — configured by endpoint + region +
  access keys, exactly like the `s3:` restic backend.
- **Native backends:** Backblaze B2 (`b2:`), Azure Blob, Google Cloud Storage, SFTP, a restic
  REST server, a local/USB path.
- **rclone (`rclone:`):** bridges essentially every other remote (Dropbox, OneDrive, etc.).

`mnemo init` selects backend + collects its credentials; nothing in Mnemo's own logic assumes
a particular provider. (The user's current setup happens to be Backblaze B2 via S3 — that's an
example, not a constraint.) The repo password is the encryption root — stored in the OS keychain
(macOS Keychain / Windows Credential Manager) with a config fallback.

> restic's format is its own; we **cannot** reuse claude-sync's raw `.age` objects. Migration
> is by re-snapshotting from local truth — see §7.

### 4.3 Snapshots

Every `mnemo push` creates one restic snapshot of a **staging tree** (see §5.4), tagged with:
- `host=<machine-id>` — which device produced it
- `mnemo=<schema-version>`

(There is no scope tag: Mnemo only ever snapshots session data — principle 9.)

Snapshots are immutable and additive. N machines pushing = N independent snapshot lineages in
one repo, all deduplicated against each other.

### 4.4 The project-identity manifest (`projects.json`)

Backed up inside the repo. Maps a **stable project identity** to per-machine local paths:

```json
{
  "git:github.com/ekinertac/humbl-ai": {
    "darwin-mbp":   "/Users/ekinertac/Code/humbl-ai",
    "win-desktop":  "C:\\Users\\ekin\\Code\\humbl-ai"
  },
  "path:~/Downloads/scratch": { "darwin-mbp": "/Users/ekinertac/Downloads/scratch" }
}
```

Identity resolution order for a given `~/.claude/projects/<encoded-cwd>` dir:
1. If the decoded cwd is inside a git work tree → `git:<normalized-origin-url>` (origin host +
   path, lowercased, `.git` stripped). This is **machine-independent**.
2. Else → `path:<home-relative-or-absolute>` fallback (the claude-sync `${HOME}` approach).

This is the crux: identity is what makes a session from the Windows box land in the *right*
project on the Mac even if the absolute paths differ.

---

## 5. The four Mnemo components

### 5.1 Ephemeral filter
Pure classification (§4.1). Applied when building the staging tree for backup. Defaults skew
conservative-durable for transcripts/memory, aggressive-skip for scratch. Fully overridable.

### 5.2 Project-identity remapping
On **backup**: for each project dir, resolve identity (§4.4), record/update `projects.json`
with this host's local path, and store the session under an identity-keyed path in the staging
tree (e.g. `by-id/git_github.com_ekinertac_humbl-ai/<session>.jsonl`).

On **restore**: resolve where *this* machine wants the project (from `projects.json`, or by
scanning local git repos for a matching origin, or prompting), then materialize the transcript
at the Claude-Code-expected location: `~/.claude/projects/<encoded-local-cwd>/<session>.jsonl`.
This is what makes `claude --resume` actually find cross-machine sessions.

> If no local path is known for an identity on this machine, the session is restored to a
> holding area and surfaced by `mnemo projects --unmapped`; it becomes resumable once the repo
> is cloned / the path is mapped. We **log** unmapped sessions loudly — never silently drop.

### 5.3 Transcript append-merge
Session `.jsonl` files are append-only event logs. Same session UUID edited on two machines is
rare but possible; `history.jsonl` is routinely divergent.

Merge strategy for append-only logs: **longest common prefix, then union of the remaining
unique lines ordered by their event timestamp.** Deterministic, no clobber. Restic's dedup
means re-storing a merged log is cheap. For the (rare) genuinely conflicting case, keep both
lineages and let `mnemo doctor` surface it — never lose lines.

### 5.4 Staging tree + resume-aware restore
`mnemo push` builds a deterministic **staging tree** (identity-keyed, filtered, merged) and
hands *that* to `restic backup`. `mnemo pull` runs `restic restore` of the chosen snapshot
into a temp dir, then applies §5.2 lay-down into `~/.claude/`. Staging keeps the restic repo
clean and machine-independent, and keeps lay-down logic out of restic.

---

## 6. CLI surface

Every command is **non-interactive** (principle 8): it runs to completion or exits non-zero
with a clear message. No prompts. Add `--json` to any command for machine-readable output;
`--dry-run` previews without writing.

```
mnemo init                 # write config + create/attach restic repo from flags/env; NO prompts (sessions-only; no scope choice)
mnemo push                 # snapshot ~/.claude (filtered, identity-keyed, merged)   [aliases: snapshot]
mnemo pull                 # restore latest snapshots, lay down for THIS machine      [aliases: restore]
mnemo log                  # list snapshots (host, time, scope, size)                 [restic snapshots]
mnemo machines             # devices that have pushed, last-seen
mnemo projects             # identity ⇄ local-path map; --unmapped to see gaps
mnemo map <id> <path>      # set/override a project's local path on this machine (scriptable)
mnemo prune                # apply retention explicitly (never automatic)             [restic forget --prune]
mnemo verify               # integrity check                                          [restic check]
mnemo diff                 # what a push would add (preview); read-only
mnemo doctor               # diagnose config, repo health, unmapped sessions, merges needed
```

Two things are deliberately **absent**: (a) any command that deletes remote data as a side
effect of a normal push — `prune` is the only path to deletion, explicit and retention-bounded;
(b) any interactive flow. Where claude-sync would prompt, Mnemo decides by flag + rule:

- **`mnemo pull` never asks and never silently clobbers.** Default `--on-conflict=keep-newer`
  (compare by event/mtime; for append-only logs, *merge* per §5.3). Other modes:
  `keep-local`, `keep-remote`, `keep-both` (writes the loser alongside with a suffix). Unmapped
  sessions go to the holding area and are reported, never dropped.
- **Conflict resolution is a flag, not a menu.** `mnemo doctor` lists anything needing a human
  decision; you resolve it by re-running with the relevant `--on-conflict` / `mnemo map`.

### 6.1 Configuration & secrets (non-interactive)

Resolution order (highest wins): **CLI flags → environment → config file → defaults.**

- **Config file:** `~/.config/mnemo/config.toml` (override with `--config` / `MNEMO_CONFIG`).
  Non-secret settings: backend kind + endpoint/region/bucket, ephemeral globs, retention
  policy, this machine's `host` id. (No scope setting — Mnemo is sessions-only by definition.)
- **Environment:** `MNEMO_*` for Mnemo settings; backend creds via the providers' standard envs
  (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_ENDPOINT_URL`, `B2_ACCOUNT_ID`/`B2_ACCOUNT_KEY`,
  etc.) passed straight through to restic.
- **Repo password (the encryption root):** never prompted. Use restic's own non-interactive
  mechanisms — `RESTIC_PASSWORD`, `RESTIC_PASSWORD_FILE`, or `RESTIC_PASSWORD_COMMAND` (e.g. a
  keychain read) — or a Mnemo `--password-file` / OS-keychain reference. **Secrets are never
  passed as plain CLI flags** (they leak via the process list); env/file/keychain/stdin only.

`mnemo init` simply materializes the config file and runs `restic init` (or attaches to an
existing repo) using the above sources, validates connectivity, and exits. If something
required is missing, it fails with exactly what to set — it does not ask.

---

## 7. Migration from claude-sync

The Mac currently holds the full, correct session set locally (we just pulled everything,
including the Windows machine's sessions, into `~/.claude`). So:

1. Install `restic`, `mnemo init` against a **new** repo on any backend you like (a new bucket
   or prefix on the existing B2 store is fine, or any other S3-compatible/restic backend; do
   not reuse the claude-sync `.age` objects).
2. `mnemo push` — first snapshot captures everything from local truth.
3. On the Windows machine: install, `mnemo init` (same repo), `mnemo pull` (identity remap),
   verify resume works, then `mnemo push`.
4. Decommission claude-sync once both machines are on Mnemo. Keep the old B2 bucket read-only
   for a while as a cold backup (it has 365-day versioning).

No data is at risk: local is authoritative and additive snapshots can't delete it.

---

## 8. Open questions / risks

- **restic library vs shelling out.** Start by shelling out. Measure; embed later only if
  needed.
- **Identity for non-git projects.** Fallback is path-based (home-relative token). Good enough;
  document the limitation (same as claude-sync today).
- **Concurrent same-session edits across machines.** Rare. Append-merge handles the common
  case; `doctor` surfaces true conflicts. Never auto-clobber.
- **Config/capabilities are out of scope (settled, not open).** `settings.json`, MCP, skills,
  agents, plugins, rules, `CLAUDE.md` are never synced (principle 9 / §4.1-C). Hooks, absolute
  paths, and plugin caches are machine-specific and were a footgun in claude-sync. If a user
  ever wants config sync, that's a *separate* tool — not a Mnemo mode.
- **Password/repo-key recovery.** Lose the restic password → data unrecoverable (by design).
  Since there are no prompts, `init` instead *prints* the password/recovery instruction and
  requires the caller to have supplied it explicitly (env/file/keychain) — it never invents or
  hides one. Optionally store it in the OS keychain. Document loudly in `init` output and `doctor`.
- **Windows path handling.** Encoded cwd differs (`C--Users-...`); identity layer is exactly
  what abstracts this away. Test it early.

---

## 9. Build order (milestones)

- **M0 — Spike.** `mnemo push`/`pull` that just shells `restic backup`/`restore` of raw
  `~/.claude/projects` with no Claude logic. Prove the engine + B2 path end-to-end.
- **M1 — Ephemeral filter + staging tree.** Real durable/ephemeral split; deterministic staging.
- **M2 — Project identity.** `projects.json`, git-origin resolution, identity-keyed staging,
  resume-aware lay-down. The headline feature; test Mac⇄Windows resume.
- **M3 — Append-merge.** `history.jsonl` + same-session union.
- **M4 — Retention & integrity.** `prune` (explicit), `verify`, `doctor`.
- **M5 — Polish.** `init` wizard, keychain, `machines`/`projects` views, docs, release builds.

Ship M0–M2 before retiring claude-sync; M3+ are quality-of-life on top of a correct core.
