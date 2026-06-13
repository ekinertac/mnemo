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

Early days, but the core works: **M0–M2 are built** (on the `m2-project-identity` branch) —
filtered, identity-keyed snapshots and resume-aware restore that re-homes a session onto
another machine. Append-merge, retention/verify/doctor, and polish (M3–M5) are still to come,
and a real Mac⇄Windows resume is still to be verified.

- **[docs/DESIGN.md](docs/DESIGN.md)** — architecture, rationale, and the milestone plan.
- **[HANDOFF.md](HANDOFF.md)** — context for picking the project up.

## Name

*Mnemo*, for Mnemosyne — memory. Your sessions are working memory, carried between machines.
