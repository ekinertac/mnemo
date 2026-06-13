// Command mnemo is the entrypoint for the Mnemo CLI.
//
// Mnemo syncs Claude Code *sessions* across machines as encrypted, deduplicated,
// append-only snapshots by wrapping the `restic` binary and adding a Claude-aware
// layer (ephemeral filtering, project-identity mapping, transcript merge, resume-aware
// restore). See docs/DESIGN.md for the architecture and HANDOFF.md for context.
//
// This file is deliberately tiny: it only hands control to internal/command, which
// owns subcommand dispatch. Keeping main thin means the testable surface lives in
// packages, not in package main.
//
// At this milestone (M0) the only logic is shelling out to restic for push/pull/init/
// log against a raw ~/.claude/projects tree — no Claude-specific logic yet. The point
// of M0 is to prove the engine + backend path end-to-end before layering anything on.
package main

import (
	"os"

	"github.com/ekinertac/mnemo/internal/command"
)

func main() {
	os.Exit(command.Execute(os.Args[1:]))
}
