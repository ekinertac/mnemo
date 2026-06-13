// pull.go implements `mnemo pull`: restore a snapshot from the restic repo, then (by default)
// lay sessions back into ~/.claude via internal/restore.LayDown so this machine can `claude
// --resume` immediately without any further manual step.
//
// Behaviour:
//   --lay-down=true  (default) loads the repo's projects.json from the restored staging tree,
//     overlays any host-local overrides written by `mnemo map`, then calls LayDown to materialize
//     each project's transcripts at the local path this machine's Claude expects.
//   --lay-down=false restores into the target directory and stops there — useful for inspecting
//     the raw staging tree without touching ~/.claude.
//
// The --target flag sets the directory the staging tree lands in (default: ./mnemo-restore).
// restic snapshots store the staging root at its absolute cache path; we use the
// "snapshotID:subpath" restore syntax so the by-id/ and projects.json land directly in target
// rather than nested under the full absolute-path hierarchy.
//
// Non-interactive (principle 8): pull never asks and never silently clobbers. Conflict policy
// at file granularity is last-write-wins; .jsonl append-merge is M3.
//
// Related: internal/restore (LayDown/ResolveLocal), internal/manifest (overlay), root.go
// (overlayLocalOverrides, stageRootDir), internal/identity (EncodedHome).
package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/restic"
	"github.com/ekinertac/mnemo/internal/restore"
)

func runPull(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	snapFlag := fs.String("snapshot", "latest", "snapshot ID to restore (default: latest)")
	targetFlag := fs.String("target", "", "directory to restore into (default: ./mnemo-restore)")
	layDown := fs.Bool("lay-down", true, "after restore, lay sessions into ~/.claude for this machine")
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

	// stageRoot is where the staging tree was backed up FROM; restic stores it at that absolute
	// path. Using the ":subpath" restore syntax strips that prefix so the staging tree lands
	// directly at target (by-id/, projects.json at the top level) rather than nested.
	stageRoot, err := stageRootDir()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}

	repo, desc := resolveRepo(*repoFlag)
	fmt.Printf("mnemo: restoring snapshot %q from %s -> %s\n", *snapFlag, desc, target)
	if err := repo.RestoreSubpath(ctx, *snapFlag, stageRoot, target); err != nil {
		return err
	}
	fmt.Printf("mnemo: restored into %s\n", target)

	if *layDown {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		host, err := hostID()
		if err != nil {
			return err
		}
		man, err := manifest.Load(filepath.Join(target, "projects.json"))
		if err != nil {
			return err
		}
		if err := overlayLocalOverrides(man, host); err != nil {
			return err
		}
		rep, err := restore.LayDown(target, filepath.Join(home, ".claude"), host, identity.EncodedHome(home), man)
		if err != nil {
			return err
		}
		fmt.Printf("mnemo: laid down %d files; %d unmapped\n", rep.LaidDown, len(rep.Unmapped))
		for _, id := range rep.Unmapped {
			fmt.Printf("  unmapped: %s  (use: mnemo map %s <local-path>)\n", id, id)
		}
	}
	return nil
}
