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
| **Identity = encoded *absolute* cwd** (`-Users-ekinertac-Code-foo`), with `${HOME}` tokenization bolted on later. | Cross-machine resume worked only by *coincidence* of identical absolute layouts. *(Mnemo keeps a path model but makes home-relative `~`-tokenization the clean, deterministic primary identity — §4.4 — and pairs it with `mnemo map`; the data-losing failures below, not path-identity itself, are what Mnemo fixes structurally.)* |

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
4. **Identity by tokenized path, not by absolute path.** A project is identified by Claude's
   encoded cwd dir name with the home prefix tokenized away (`home:-Code-foo`), or by the encoded
   absolute path when it lives outside home (`abs:-opt-services-bar`). Resolution is a pure,
   deterministic function of that string — no git, no manifests, no guessing, no decoding (the
   encoding is lossy; see §4.4). This is machine-independent *for projects under `~` that keep
   the same relative layout across machines*, which is the deliberate, documented contract of a
   personal tool; `mnemo map` is the override for everything else. (Earlier drafts keyed on git
   origin; that was dropped — see §4.4 — as disproportionate complexity for a single-user tool,
   and re-addable later inside the one resolver function if ever needed.)
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

### 4.4 Project identity (a pure function) and `projects.json`

**Identity is a deterministic function of Claude's *encoded* project-dir name** — the
`projects/<encoded-cwd>` directory name, not a decoded path, and not git or a manifest. Claude
encodes a cwd by replacing every non-alphanumeric character with `-` (`/Users/ekinertac/Code/foo`
→ `-Users-ekinertac-Code-foo`), which is **lossy and irreversible** (`age.sh`, `age-sh`, and
`age sh` all collapse to `age-sh`). So Mnemo never decodes — it tokenizes the encoded string by
swapping the machine-specific encoded-home prefix for a token:

```
identity(encodedCwd, encodedHome):        # encodedHome = Claude's encoding of $HOME
  if encodedCwd is under encodedHome → home:<encoded-tail>   # home:-Code-foo
  else                               → abs:<encoded-cwd>     # abs:-opt-services-bar
```

The encoded-home prefix is the whole mechanism: it absorbs the platform-specific home segment
(`/Users/ekinertac`, `C:\Users\ekin`, `/home/ekin` all encode to a `-Users-…`/`-…` prefix), so
two machines match **iff the home-relative portion agrees**, regardless of where home is. Cross-
platform matching, stated exactly:

| cwd location | identity | matches across machines when… |
|---|---|---|
| under `$HOME` | `home:-Code-foo` | the **home-relative path** is the same — robust to different home locations; the reliable cross-platform case |
| outside `$HOME` | `abs:-opt-services-bar` | the **absolute path is byte-identical** — possible within an OS family (two Macs, Mac↔Linux on `/opt`), **never** Windows↔Unix (drive letters vs rooted paths) |

This is intentionally weaker than the git-origin scheme earlier drafts described: a session
only lands in the right project on another machine if that machine uses the same layout (under
`~`) or the same absolute path. For a personal tool where the user controls both machines'
layouts, that's an acceptable contract, and it buys a large simplification — no git URL
normalization (`git@`/`https`/`ssh`/`.git`/ports/self-hosted), and no "git on machine A, no
remote on machine B → identities diverge" bug class, because every machine resolves the same
way. Identity is one function; a git branch can be added inside it later without touching the
repo format, staging, or restore.

**Correctness requirements:** derive `encodedHome` by Claude-encoding `$HOME` (on Windows the
drive letter is stripped first — `C:\Users\u` and `/Users/u` both encode to `-Users-u`); match
the prefix at a separator (`-`) boundary so `-Users-ekin` doesn't swallow `-Users-ekinside`;
compare case-insensitively (case-insensitive filesystems). Working in encoded space makes Unicode
normalization **moot** — Claude's `[^A-Za-z0-9]→-` encoding already collapses every non-ASCII
character to `-`, so an accented folder name matches across machines regardless of NFC vs NFD.

**`projects.json`** (backed up in the repo) is therefore no longer the crux — because identity
⇄ local path is a reversible function for the common case, restore just swaps this machine's
encoded-home prefix back in. The manifest shrinks to two jobs: per-machine **overrides** set by
`mnemo map` (for projects that live at a different path on a given machine), and lightweight
**machine bookkeeping** for the `machines`/`projects` views:

```json
{
  "version": 1,
  "machines": {
    "darwin-mbp":  { "lastSeen": "2026-06-13T21:00:00Z" },
    "win-desktop": { "lastSeen": "2026-06-10T08:12:00Z" }
  },
  "overrides": {
    "darwin-mbp": { "home:-Code-foo": "/Users/ekinertac/work/foo" }
  }
}
```

---

## 5. The four Mnemo components

### 5.1 Ephemeral filter
Pure classification (§4.1). Applied when building the staging tree for backup. Defaults skew
conservative-durable for transcripts/memory, aggressive-skip for scratch. Fully overridable.

### 5.2 Project-identity remapping
On **backup**: for each `projects/<encoded-cwd>` dir, compute `identity` directly from the
encoded dir name (§4.4 — no decode) and stage the session under an identity-keyed path
(`by-id/<identity>/<rest>`, e.g. `by-id/home:-Code-foo/<session>.jsonl`) rather than the
machine-specific encoded cwd. The push also seeds `projects.json` from the latest snapshot and
stamps this host's `lastSeen`, so the machines list accumulates across devices.

On **restore** (`internal/restore.ResolveLocal`): turn each identity back into *this* machine's
encoded local dir —
1. if `projects.json` (overlaid with this host's local `mnemo map` overrides) has an override
   for `(this host, identity)` → encode that path;
2. else if the identity is `home:<tail>` → prepend this machine's encoded-home prefix;
3. else (`abs:<encoded>`) → use the encoded absolute path as-is;

then materialize the transcript at the Claude-Code-expected location
`~/.claude/projects/<encoded-local-cwd>/<rest>`, so `claude --resume` finds it.

> Under-home identities always resolve (placement is harmless even if the project dir doesn't
> exist locally yet), and an `abs:` identity is laid down additively at its encoded path even
> when it came from another OS — never silently dropped. `mnemo projects --unmapped` surfaces
> identities that don't resolve to an existing local project so the user can `mnemo map` them;
> a structurally malformed identity (no `home:`/`abs:` scheme) is reported, never written
> blindly. Conflict policy is last-write-wins at file level; the `.jsonl` append-merge is M3.

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
- **Identity is path-based for *all* projects (settled, not open).** Git-origin identity was
  considered and dropped (§4.4): too much fragile URL-normalization for a personal tool, and it
  introduced a git-on-one-machine-only divergence bug. Identity is `home:<encoded-tail>` under
  home, `abs:<encoded>` outside (computed from Claude's lossy encoded dir name, never decoded).
  Limitation — cross-machine resume needs matching layout (or `mnemo map`) — is accepted and
  documented. Re-addable later inside the single resolver function.
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
- **Windows path handling (not yet verified on a live box).** Under-home cwds tokenize the same
  as macOS (`C:\Users\u\…` → `-Users-u-…`, drive dropped — step-0 finding), so the identity layer
  abstracts them away. Outside-home Windows paths keep the drive (`C:\work\foo` → `abs:C--work-foo`)
  and don't resolve on Unix — accepted. **Known blocker for Windows push:** the staging dir name
  `by-id/home:-Code-foo` contains a `:`, which NTFS forbids in filenames — so push would fail on
  Windows until the identity is given a filesystem-safe encoding for the `by-id/` path component
  (e.g. escape `:` → `__`, decode on restore). Must be fixed before claiming Windows support.

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
