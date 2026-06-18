# Mnemo

**Pick up any Claude Code session on any machine.**

Mnemo syncs your Claude Code conversations across computers as encrypted, deduplicated,
append-only snapshots — then lays them back down so `claude --resume` finds them, no matter where
each project lives on each machine.

```console
$ mnemo push
mnemo: pushing to b2:my-sessions …
  uploading [481/481 files] 100%
mnemo: pushed ✓  snapshot a1b2c3d4 · 481 files · 1.8 MiB uploaded (only changes sent)

# …later, on your other machine:
$ mnemo pull
mnemo: pulled ✓  laid down 481 files into ~/.claude
$ claude --resume          # the session you started on your laptop is right here
```

## Why

You start a conversation with Claude Code on your laptop. The next day you're at your desktop, and
that session is gone — pinned to the other machine's filesystem. Mnemo carries it across.

It's a clean-room successor to `tawanorg/claude-sync`, rebuilt around three guarantees the naive
"just mirror the folder" approach gets wrong (and that cost the original tool **440 transcripts**):

- **It can't lose your data.** Every sync is an immutable, additive snapshot. A file missing
  locally never deletes anything remote — deletion happens *only* through an explicit,
  retention-bounded `prune`. "I lost a session" becomes a restore, not a tragedy.
- **It resumes anywhere.** Sessions are keyed by *project identity*, not absolute path, so a session
  from `~/Code/foo` on your laptop lands in the right project on your desktop even if it lives
  somewhere else there.
- **It syncs sessions, not config.** Your conversations, memory, plans, and history — never your
  MCP servers, skills, agents, plugins, or settings. Those are machine-specific; mirroring them is a
  footgun. A hard boundary, not a toggle.

## How it works

A thin, Claude-aware layer over [`restic`](https://restic.net). restic handles the hard, dangerous
parts — AES-256 encryption, content-defined dedup (so a 700 MB session tree syncs as the ~2 MB that
actually changed), immutable snapshots, integrity checks, retention. Mnemo adds only what's
Claude-specific: deciding what's durable vs. scratch, mapping projects to wherever they live on each
machine, union-merging append-only logs so two machines never clobber each other's history, and
laying sessions down exactly where `claude --resume` expects them.

Backend-agnostic — any restic backend works: S3-compatible (AWS, Backblaze B2, MinIO, Wasabi, …),
native B2 / Azure / GCS, SFTP, or rclone. Every command is non-interactive, so it's safe to run from
cron, CI, or a hook.

## Quick start

```sh
go build -o mnemo .

mnemo init        # create or attach a restic repo (from config or env)
mnemo push        # snapshot this machine's sessions
mnemo pull        # on another machine: restore + lay down for claude --resume
mnemo doctor      # is everything healthy?
mnemo log         # what's stored — and the real (deduped) footprint, not the logical size
```

Config lives in `~/.config/mnemo/config.json`: the repo location plus *references* to secrets (a
keychain command, a file, an env var) — so no credential is ever written to the file, and the same
setup works across macOS, Windows, and Linux. Output is plain language by default, with a live
progress counter on a terminal and raw `restic` behind `-v`.

## Status

Working and in daily use on macOS, validated end-to-end against a real Backblaze B2 (S3) backend.
The full CLI — `push` `pull` `log` `map` `projects` `machines` `verify` `prune` `doctor` — is built.
Still to verify: a live **Mac⇄Windows** resume (the Windows path handling is unit-tested, not yet
run on a real Windows box).

- **[docs/DESIGN.md](docs/DESIGN.md)** — the architecture, and the reasoning behind every decision.

## Name

*Mnemo*, for Mnemosyne — memory. Your sessions are working memory, carried between machines.
