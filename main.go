// Command mnemo is the entrypoint for the Mnemo CLI.
//
// Mnemo syncs Claude Code *sessions* across machines as encrypted, deduplicated,
// append-only snapshots by wrapping the `restic` binary and adding a Claude-aware
// layer (ephemeral filtering, project-identity mapping, transcript append-merge,
// resume-aware restore). See docs/DESIGN.md for the architecture and rationale.
//
// This file is deliberately tiny: it only hands control to internal/command, which
// owns subcommand dispatch. Keeping main thin means the testable surface lives in
// packages, not in package main.
package main

import (
	"os"

	"github.com/ekinertac/mnemo/internal/command"
)

func main() {
	os.Exit(command.Execute(os.Args[1:]))
}
