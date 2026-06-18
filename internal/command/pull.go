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
// The restore subpath (what strips the absolute cache prefix from the snapshot tree) is derived
// from the SNAPSHOT's own recorded path via restoreStagingTreeTo — NOT from this machine's
// stageRootDir. This is critical for cross-machine correctness: UserCacheDir differs per
// OS/user, so the pushing machine's path won't match the pulling machine's local cache dir.
//
// Non-interactive (principle 8): pull never asks and never silently clobbers. Conflict policy
// at file granularity is last-write-wins; .jsonl append-merge is M3.
//
// Related: internal/restore (LayDown/ResolveLocal), internal/manifest (overlay), root.go
// (overlayLocalOverrides, restoreStagingTreeTo), internal/identity (EncodedHome).
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
	verbose := fs.Bool("verbose", false, "show restic's raw technical output instead of a plain summary")
	fs.BoolVar(verbose, "v", false, "shorthand for --verbose")
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

	repo, desc, err := resolveRepo(*repoFlag)
	if err != nil {
		return err
	}
	repo.Verbose = *verbose // -v streams restic's raw restore output; default is a clean summary

	fmt.Printf("mnemo: pulling %s from %s …\n", *snapFlag, repoName(desc))
	// restoreStagingTreeTo derives the subpath from the snapshot's own recorded path rather
	// than this machine's stageRootDir, so a snapshot pushed from any machine restores correctly
	// here — cross-machine UserCacheDirs differ.
	if err := restoreStagingTreeTo(ctx, repo, *snapFlag, target); err != nil {
		return err
	}

	if !*layDown {
		fmt.Printf("mnemo: restored ✓  staging tree written to %s (not laid down — --lay-down=false)\n", target)
		return nil
	}

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
	if len(rep.Unmapped) == 0 {
		fmt.Printf("mnemo: pulled ✓  laid down %d files into ~/.claude\n", rep.LaidDown)
	} else {
		fmt.Printf("mnemo: pulled ✓  laid down %d files into ~/.claude (%d unmapped — need `mnemo map`)\n",
			rep.LaidDown, len(rep.Unmapped))
		for _, id := range rep.Unmapped {
			fmt.Printf("  unmapped: %s  →  mnemo map %s <local-path>\n", id, id)
		}
	}
	return nil
}
