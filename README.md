# Mnemo

**Pick up any Claude Code session on your other machines.**

[![CI](https://github.com/ekinertac/mnemo/actions/workflows/ci.yml/badge.svg)](https://github.com/ekinertac/mnemo/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/ekinertac/mnemo)](https://goreportcard.com/report/github.com/ekinertac/mnemo)
[![Release](https://img.shields.io/github/v/release/ekinertac/mnemo)](https://github.com/ekinertac/mnemo/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Mnemo syncs your Claude Code conversations across computers as encrypted, deduplicated,
append-only snapshots — then lays them back down where `claude --resume` finds them, keyed on
project identity rather than a raw filesystem path.

```console
$ mnemo init
$ mnemo sync
mnemo: empty repo, first sync is a push
mnemo: pushing to b2:my-sessions …
  uploading [481/481 files] 100%
mnemo: pushed ✓  snapshot a1b2c3d4 · 481 files · 1.8 MiB uploaded (only changes sent)

# …later, on your other machine:
$ mnemo init
$ mnemo sync
mnemo: merged 481 files into ~/.claude
mnemo: pushed ✓  snapshot e5f6a7b8 · 481 files · 0 B uploaded (only changes sent)
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
- **It follows your layout across machines.** Sessions are keyed by a path-tokenized *project
  identity*, not a raw absolute path: the home prefix is tokenized away, so `~/Code/foo` matches
  across machines regardless of OS or username (`/Users/you`, `/home/you`, `C:\Users\you`). Keep the
  same layout under `~` and a laptop session resumes on your desktop automatically; if a project
  lives at a different path on another machine, one `mnemo map` points it there.
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

## Install

Mnemo needs both [`restic`](https://restic.net) and `git` on your `PATH`; the installers below pull
both in for you. `git` is only ever run locally, to 3-way-merge `.md` files during `sync`; nothing is
pushed to a remote or exposes your sessions to any git hosting.

**Homebrew** (macOS/Linux), installs `restic` and `git` automatically:

```sh
brew install ekinertac/tap/mnemo
```

**`go install`** (needs [Go](https://go.dev) 1.23+ and `restic`/`git` already installed):

```sh
go install github.com/ekinertac/mnemo@latest   # drops mnemo in $(go env GOPATH)/bin
```

**Prebuilt binaries** — grab a `.tar.gz` for your OS/arch from the
[Releases page](https://github.com/ekinertac/mnemo/releases) (remember to install `restic` and
`git` too).

**From source:**

```sh
git clone https://github.com/ekinertac/mnemo.git && cd mnemo
go build -o mnemo .
```

## Quick start

```sh
mnemo init        # create or attach a restic repo (from config or env)
mnemo sync        # pull, merge, then push: the one command you'll actually run, on every machine
mnemo doctor      # is everything healthy?
mnemo log         # what's stored — and the real (deduped) footprint, not the logical size
```

### Advanced: push and pull separately

`sync` is a pull followed by a push, merging as it goes. Reach for it by default; `push` and `pull`
are its two halves, for when you want only one side of that:

- `mnemo push`: snapshot this machine's sessions. No pull, no merge.
- `mnemo pull`: restore the latest snapshot and lay it down. No push.

Config lives in `~/.config/mnemo/config.json`: the repo location plus *references* to secrets (a
keychain command, a file, an env var) — so no credential is ever written to the file, and the same
setup works across macOS, Windows, and Linux. Output is plain language by default, with a live
progress counter on a terminal and raw `restic` behind `-v`.

## Status

Working and in daily use on macOS, validated end-to-end against a real Backblaze B2 (S3) backend.
The full CLI (`sync`, `push`, `pull`, `log`, `map`, `projects`, `machines`, `verify`, `prune`, `doctor`) is built.
Still to verify: a live **Mac⇄Windows** resume (the Windows path handling is unit-tested, not yet
run on a real Windows box).

- **[docs/DESIGN.md](docs/DESIGN.md)** — the architecture, and the reasoning behind every decision.

## Name

*Mnemo*, for Mnemosyne — memory. Your sessions are working memory, carried between machines.
