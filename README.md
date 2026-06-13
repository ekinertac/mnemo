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

Early design. Nothing built yet.

- **[docs/DESIGN.md](docs/DESIGN.md)** — architecture, rationale, and the milestone plan.
- **[HANDOFF.md](HANDOFF.md)** — context for picking the project up from zero.

## Name

*Mnemo*, for Mnemosyne — memory. Your sessions are working memory, carried between machines.
