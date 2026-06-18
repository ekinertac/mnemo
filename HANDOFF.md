# Mnemo — Session Handoff

> Read this first, then `docs/DESIGN.md`. This file orients a fresh Claude Code session that
> picks up Mnemo from zero. Written 2026-06-13.

## What this is

**Mnemo** is a clean-room successor to `tawanorg/claude-sync`: a tool that syncs Claude Code
sessions across machines as encrypted, deduplicated, **append-only snapshots** (via `restic`),
and restores them to the path each machine's `claude --resume` expects — keyed on **project
identity** (git origin), not filesystem path.

It was conceived after using claude-sync heavily on a real macOS + Windows setup and hitting
its foundational problems (a sidecar index that diverges from reality, destructive mirror
semantics that caused real data loss, no merge for logs, per-file objects, path-based identity).
The full critique and the chosen architecture are in `docs/DESIGN.md` — **read it before
writing code.**

## Decisions already locked (do not relitigate without reason)

- **Name:** Mnemo (binary `mnemo`).
- **Architecture:** thin Claude-aware layer **over `restic`** — restic does encryption, dedup,
  snapshots, integrity, retention; we write only: ephemeral filter, project-identity map,
  transcript append-merge, resume-aware restore. (See DESIGN §3.)
- **Language:** Go, single binary. **Shell out to the `restic` binary** to start (not the lib).
- **Backend:** **backend-agnostic** — any restic backend, with S3-compatible stores first-class
  (AWS S3, Backblaze B2, MinIO, Wasabi, DO Spaces, Ceph, self-hosted S3), plus native
  B2/Azure/GCS/SFTP/REST/rclone. `mnemo init` selects the backend; no Mnemo logic assumes a
  provider. The user's *current* store is B2-via-S3, but that's just the migration source, not a
  requirement. New repo — do NOT reuse claude-sync's `.age` objects.
- **Scope: sessions ONLY — a hard boundary, not a toggle.** Sync conversation/session data
  (`projects/` transcripts + per-project `memory/`, `plans/`, `tasks/*.json`, `history.jsonl`).
  **Never** sync MCP configs, `skills/`, `agents/`, `plugins/`, `rules/`, `settings.json`, or
  `CLAUDE.md` — those are config/capabilities, not sessions. Also skip ephemeral scratch
  (subagent/workflow/tool-results/locks). There is no "full" mode. See DESIGN §2 (principle 9)
  and §4.1.
- **No interactivity, ever.** No wizards, prompts, or interactive conflict menus — anywhere,
  including `init` and `pull`. Config via flags → env → config file (`~/.config/mnemo/config.toml`);
  secrets via env/file/keychain/stdin (never plain CLI flags). Conflicts resolved by
  `--on-conflict` flags + deterministic rules, surfaced by `doctor`. Must run cleanly with no
  TTY (cron/CI/hook/agent). See DESIGN §2 (principle 8) and §6.

## Status

**M0–M2 built** on branch `m2-project-identity` (Go, stdlib-only, shells out to `restic`):
- **M0** — `init`/`push`/`pull`/`log` shelling restic; engine + local-repo path proven.
- **M1** — ephemeral filter (`internal/filter`, allowlist) + staging tree (`internal/stage`).
  Sessions-only boundary is structural. Includes the legacy `transcripts/` store.
- **M2** — project identity (`internal/identity`), slim `projects.json` (`internal/manifest`),
  identity-keyed staging, resume-aware lay-down (`internal/restore`), plus `map`/`projects`/
  `machines`. Cross-home re-homing + machine accumulation covered by tests.
- **M3** — append-merge (`internal/merge`): a divergent `.jsonl` at the lay-down destination is
  union-merged (longest common prefix + timestamp-ordered union, never drop a line) instead of
  clobbered. Wired into `restore.writeFile` (atomic temp+rename); non-`.jsonl` stays last-write-wins.
- **M4** — integrity + retention: `verify` (`restic check`), `doctor` (read-only health report),
  and `prune` — the only deleting command, deliberately unforgiving: no `--keep-*` policy → refuses
  (0 counts as unset), dry-run unless `--apply`, always `--group-by host`. `forgetArgs` is TDD'd.
- **M5 (config)** — `internal/config` loads `~/.config/mnemo/config.json` (JSON, not TOML; dir is
  XDG `~/.config`, NOT `os.UserConfigDir()`/macOS Library). Holds repo URL, host, exclude globs, and
  *secret references* (a retrieval command/file/env — never plaintext). `resolveRepo` consults it
  and sets restic's env from keychain-resolved secrets; env still wins. So `mnemo push` etc. work
  with **no env sourcing**.
- Default test suite is offline (`go test ./...`); the cross-home integration test is
  build-tagged: `go test -tags e2e ./...` (needs `restic`).
- **Real B2 backend works, no env sourcing needed:** `~/.config/mnemo/config.json` points at bucket
  `<your-bucket>` (`s3:`) and references three macOS Keychain entries (`mnemo-restic-b2`
  password, `mnemo-b2-keyid`, `mnemo-b2-secret`). Just run `mnemo <cmd>`. (The old
  `~/.config/mnemo/b2.env` is now redundant — delete it to keep secrets only in the Keychain.)
- Specs/plans live under `docs/superpowers/{specs,plans}/`.

**Not yet done:** M5 polish (release builds; `init` could write a config.json skeleton; a `--config`
flag). Smaller follow-ups: silence the restic "restoring" line that `loadRepoManifest` leaks into
`doctor`/`push` output; `doctor` could surface unmapped identities. Note: `mnemo map`'s local
override store moved with M5 from macOS `~/Library/Application Support` to `~/.config/mnemo` — no
live overrides existed, so nothing to migrate. **Still pending verification:** a real
**Mac⇄Windows** resume — the `EncodedHome` Windows drive-strip is reverse-engineered from one
observed dir (unit-tested by injecting `encodedHome`, but not yet run on a live Windows box).
**Migration (Mac side) done:** the full real `~/.claude` is now snapshotted to B2 bucket
`<your-bucket>` (reused as production) — 481 durable files, 722 MiB → ~156 MiB stored,
snapshot `e8547ccd`; verified with `restic check` + a spot-check round-trip. Remaining migration
steps (DESIGN §7): on the **Windows** machine, `init` the same repo → `pull` → verify resume →
`push`; then decommission claude-sync (keep its old bucket read-only as a cold backup). Do NOT
retire claude-sync until Windows is also on Mnemo. **The user explicitly does NOT want
automatic/periodic push** — pushes are manual and deliberate (just `mnemo push`, no env sourcing).

**Windows NTFS path safety — fixed.** The staging dir name now uses a filesystem-safe identity
(`identity.PathSafe`/`FromPathSafe` map the scheme `:` ⇄ `_`, so push writes `by-id/home_-Code-foo`,
not the NTFS-illegal `by-id/home:-…`). Canonical `home:-Code-foo` remains the manifest key and
`mnemo map` argument. What's still unverified is a live Mac⇄Windows run (the `EncodedHome` Windows
drive-strip is reverse-engineered from one observed dir). See DESIGN §8.

## Hard-won fact: Claude's cwd encoding (drove the whole M2 identity design)

Claude names `~/.claude/projects/<encoded-cwd>` by replacing **every non-alphanumeric character
with `-`** (`[^A-Za-z0-9]→-`), case preserved. It is **lossy** (`age.sh`, `age-sh`, `age sh` all
→ `age-sh`). A Windows user-profile path encodes *without* its drive (`C:\Users\u\…` →
`-Users-u-…`). Because decoding is impossible, Mnemo's identity works entirely in this encoded
space: identity = encoded cwd with the encoded-home prefix tokenized (`home:-Code-foo` /
`abs:-…`). See DESIGN §4.4 and the M2 spec.

## Key external facts the new session needs

- **Existing claude-sync setup (the thing we're replacing), still live:**
  - Binary: `~/go/bin/claude-sync` (upstream `tawanorg/claude-sync`, installed via `go install`).
  - Config + creds: `~/.claude-sync/config.yaml` — Backblaze B2, bucket `claude-sync-ekinertac`,
    endpoint `https://s3.eu-central-003.backblazeb2.com`, region `eu-central-003`.
    Encryption (age) key at `~/.claude-sync/age-key.txt`. The bucket has versioning + a
    365-day noncurrent lifecycle (safe to keep as a cold backup during migration).
  - **Local `~/.claude` is currently the authoritative, complete session set** (Mac + Windows
    sessions were just consolidated here). The first `mnemo push` snapshots from this truth.
- **Claude Code retention:** `~/.claude/settings.json` has `cleanupPeriodDays: 365` (raised from
  default 30 so old transcripts aren't purged locally). Mnemo must never let local absence
  delete remote data regardless.
- **Hard-won gotchas from claude-sync (all detailed in DESIGN §1):** index/remote divergence,
  mirror-delete + retention = data loss, path-vs-identity, ephemeral files inflating object
  counts. These are the failure modes Mnemo must structurally prevent.

## Pointers

- Design: `~/Code/mnemo/docs/DESIGN.md`
- The tool being replaced: `~/Code/claude-session-sync/` (a retired fork of claude-sync;
  upstream is `github.com/tawanorg/claude-sync`). Its source is a useful reference for *what
  Claude writes to disk* (session/subagent/workflow/tool-result file shapes).
- Memory: the `claude-session-sync` project memory has a `claude-sync-setup` note capturing the
  user's environment (Mac+Windows, B2, tokenization, retention).

## Working agreement / style (from the user's global rules)

- Every source file starts with a file-level comment block (responsibility, where it fits, key
  relationships, non-obvious constraints).
- Comments explain *why*, not *what*.
- Commit messages explain *why* the change was made.
