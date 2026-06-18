# Mnemo

Sync your Claude Code sessions across machines as encrypted, deduplicated, **append-only
snapshots** — and resume them anywhere, keyed on project identity rather than filesystem path.

Built as a thin, Claude-aware layer over [`restic`](https://restic.net): restic handles
encryption, dedup, snapshots, integrity, and retention; Mnemo handles what's Claude-specific —
deciding what's worth keeping, mapping projects to wherever they live on each machine, merging
append-only logs, and laying sessions back down where `claude --resume` will find them.

> Successor to `tawanorg/claude-sync`, redesigned to be additive-by-default (a backup can never
> delete your remote data because a local file went missing), identity-aware (a session from
> your laptop resumes on your desktop even when the absolute paths differ), and **sessions-only**
> — it syncs your conversations, not your config (no MCP, skills, agents, plugins, or settings).
> Every command is non-interactive and works with any restic backend (S3-compatible and more).

## Status

Working and in daily use on macOS. **M0–M5 are built:**

- **Filtered, identity-keyed snapshots** — only durable session data (transcripts, per-project
  memory, plans, tasks, history), keyed by a path-tokenized project identity so a session resumes
  on another machine even when the absolute paths differ.
- **Resume-aware restore** that lays sessions back where `claude --resume` expects them.
- **Append-merge** for `.jsonl` logs — divergent histories union instead of clobber (never lose lines).
- **Integrity & explicit retention** — `verify`, `doctor`, and a deliberately unforgiving `prune`.
- **Config-driven** — a `config.json` with OS-keychain secret references; no env to source.
- **Plain-language CLI** — clean summaries by default with a live progress counter, raw restic behind `-v`.

Validated end-to-end against a real Backblaze B2 (S3) backend. Still to do: a live **Mac⇄Windows**
resume (the Windows path encoding is unit-tested but not yet run on a real Windows box).

```sh
go build -o mnemo .
mnemo push      # snapshot your sessions
mnemo pull      # restore + lay them down on another machine
mnemo doctor    # health check
```

- **[docs/DESIGN.md](docs/DESIGN.md)** — architecture, rationale, and the milestone plan.

## Name

*Mnemo*, for Mnemosyne — memory. Your sessions are working memory, carried between machines.
