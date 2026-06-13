// Package command implements Mnemo's CLI subcommands and the dispatch between them.
//
// Execute is the single entrypoint (called from package main). Each subcommand lives in
// its own file (push.go, pull.go, init.go, log.go) and exposes a `func(args []string) error`.
// This package owns argument parsing and config resolution; the actual engine work is
// delegated to internal/restic. Keeping dispatch here (rather than in main) means the CLI
// surface is testable and the entrypoint stays trivial.
//
// At M0 the subcommand set is intentionally minimal — init, push, pull, log — and maps
// almost 1:1 onto restic operations. The richer surface in DESIGN §6 (machines, projects,
// map, prune, verify, diff, doctor) arrives in later milestones once the Claude-aware
// layer exists. We use only the standard library `flag` package: a single-binary tool
// benefits from zero dependencies, and the dispatch is trivial to migrate if the flag
// handling ever outgrows stdlib.
//
// Config resolution (DESIGN §6.1): repo location comes from --repo flag, then MNEMO_REPO,
// then RESTIC_REPOSITORY (which restic itself reads). Secrets are never flags — the repo
// password lives in restic's own env mechanisms. See internal/restic for that boundary.
package command

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/restic"
)

// Execute dispatches argv (excluding the program name) to a subcommand and returns the
// process exit code. Unknown or missing subcommands print usage and exit non-zero — Mnemo
// never drops into an interactive menu (principle 8).
func Execute(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}

	sub, rest := args[0], args[1:]

	var err error
	switch sub {
	case "init":
		err = runInit(rest)
	case "push", "snapshot":
		err = runPush(rest)
	case "pull", "restore":
		err = runPull(rest)
	case "log":
		err = runLog(rest)
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "mnemo: unknown command %q\n\n", sub)
		usage()
		return 2
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "mnemo: %v\n", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprint(os.Stderr, `mnemo — sync Claude Code sessions as restic snapshots (M0 spike)

usage:
  mnemo init     [--repo PATH]                 create/attach a restic repo
  mnemo push     [--repo PATH] [--path DIR]    snapshot Claude sessions     (alias: snapshot)
  mnemo pull     [--repo PATH] [--snapshot ID] [--target DIR]   restore     (alias: restore)
  mnemo log      [--repo PATH]                 list snapshots

config:
  repo location:  --repo  >  $MNEMO_REPO  >  $RESTIC_REPOSITORY
  repo password:  $RESTIC_PASSWORD / $RESTIC_PASSWORD_FILE / $RESTIC_PASSWORD_COMMAND
                  (never a CLI flag — mnemo never prompts)
`)
}

// resolveRepo determines the restic repo to operate on and returns both a restic.Repo for
// the engine and a human-readable description for messaging. Precedence: --repo flag, then
// MNEMO_REPO, then RESTIC_REPOSITORY. When only RESTIC_REPOSITORY is set we leave
// restic.Repo.Repository empty and let restic read the env itself — so the displayed source
// reflects where the value actually came from.
func resolveRepo(flagRepo string) (restic.Repo, string) {
	if flagRepo != "" {
		return restic.Repo{Repository: flagRepo}, flagRepo + " (--repo)"
	}
	if v := os.Getenv("MNEMO_REPO"); v != "" {
		return restic.Repo{Repository: v}, v + " ($MNEMO_REPO)"
	}
	if v := os.Getenv("RESTIC_REPOSITORY"); v != "" {
		// Leave Repository empty: restic will read RESTIC_REPOSITORY from the inherited env.
		return restic.Repo{}, v + " ($RESTIC_REPOSITORY)"
	}
	return restic.Repo{}, "(unset — set --repo, $MNEMO_REPO, or $RESTIC_REPOSITORY)"
}

// defaultProjectsDir is the source of truth for a push at M0: the raw Claude sessions tree.
// Later milestones replace this with a filtered, identity-keyed staging tree (DESIGN §5.4).
func defaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}
