// pull.go implements `mnemo pull`: restore a snapshot from the restic repo.
//
// At M0 pull restores into an explicit target directory and stops there — it deliberately
// does NOT lay sessions back into the live ~/.claude. That keeps the spike safe: the
// authoritative local session set is never overwritten while we prove restore works.
// Milestone M2 adds the resume-aware lay-down (DESIGN §5.4) that materializes transcripts
// at the path this machine's `claude --resume` expects, keyed on project identity.
//
// Non-interactive (principle 8): pull never asks and never silently clobbers. At M0 that
// safety is structural (we restore to a fresh target); the --on-conflict machinery
// (DESIGN §6) arrives with the lay-down logic.
package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/restic"
)

func runPull(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	snapFlag := fs.String("snapshot", "latest", "snapshot ID to restore (default: latest)")
	targetFlag := fs.String("target", "", "directory to restore into (default: ./mnemo-restore)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	target := *targetFlag
	if target == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot resolve working dir for default target: %w", err)
		}
		target = filepath.Join(cwd, "mnemo-restore")
	}

	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}

	repo, desc := resolveRepo(*repoFlag)
	fmt.Printf("mnemo: restoring snapshot %q from %s -> %s\n", *snapFlag, desc, target)
	if err := repo.Restore(ctx, *snapFlag, target); err != nil {
		return err
	}
	fmt.Printf("mnemo: restored into %s (M0: not laid down into ~/.claude — that's M2)\n", target)
	return nil
}
