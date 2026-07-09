// sync.go implements `mnemo sync`: pull-then-push in one step, the command users run day to day.
//
// It restores the latest snapshot and lays it down into ~/.claude with the tiered merge
// (internal/restore: .jsonl union, .md 3-way, else newer-wins), then delegates to the exact push
// path so the new snapshot is the merged superset. The 3-way merge base is this host's previous
// snapshot, fetched via restic Dump (see baseLookup). Requires both restic and git.
//
// Related: pull.go and push.go (the two halves; sync reuses runPush verbatim), internal/restore
// (LayDown/BaseFunc), internal/restic (Dump, LatestSnapshotIDForHost, SnapshotPaths), DESIGN §5.
package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/merge"
	"github.com/ekinertac/mnemo/internal/opencode"
	"github.com/ekinertac/mnemo/internal/restic"
	"github.com/ekinertac/mnemo/internal/restore"
)

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	verbose := fs.Bool("verbose", false, "show restic's raw technical output instead of a plain summary")
	fs.BoolVar(verbose, "v", false, "shorthand for --verbose")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}
	if err := merge.GitAvailable(); err != nil {
		return err
	}

	repo, desc, err := resolveRepo(*repoFlag)
	if err != nil {
		return err
	}
	repo.Verbose = *verbose

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	host, err := hostID()
	if err != nil {
		return err
	}

	// PULL + MERGE. Skip cleanly on an empty repo (first sync is a pure push).
	count, err := repo.SnapshotCount(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		fmt.Println("mnemo: empty repo, first sync is a push")
	} else {
		fmt.Printf("mnemo: syncing with %s …\n", repoName(desc))
		target, cleanup, err := restoreStagingTree(ctx, repo, "latest")
		if err != nil {
			return err
		}
		defer cleanup()

		man, err := manifest.Load(filepath.Join(target, "projects.json"))
		if err != nil {
			return err
		}
		if err := overlayLocalOverrides(man, host); err != nil {
			return err
		}
		base, err := baseLookup(ctx, repo, host)
		if err != nil {
			return err
		}
		rep, err := restore.LayDown(target, filepath.Join(home, ".claude"), host, identity.EncodedHome(home), man, base)
		if err != nil {
			return err
		}
		fmt.Printf("mnemo: merged %d files into ~/.claude\n", rep.LaidDown)
		for _, id := range rep.Unmapped {
			fmt.Printf("  unmapped: %s  →  mnemo map %s <local-path>\n", id, id)
		}

		ocDB, err := defaultOpenCodeDB()
		if err != nil {
			return err
		}
		if err := opencode.Restore(target, ocDB, host, identity.EncodedHome(home), man); err != nil {
			fmt.Fprintf(os.Stderr, "mnemo: warning: opencode restore: %v\n", err)
		}

		if len(rep.Conflicted) > 0 {
			fmt.Printf("mnemo: %d file(s) merged with conflicts — edit the markers, then re-run sync:\n", len(rep.Conflicted))
			for _, f := range rep.Conflicted {
				fmt.Printf("  conflict: %s\n", f)
			}
		}
	}

	// PUSH the now-merged tree via the exact push path (DRY: sync == pull + push).
	pushArgs := []string{}
	if *repoFlag != "" {
		pushArgs = append(pushArgs, "--repo", *repoFlag)
	}
	if *verbose {
		pushArgs = append(pushArgs, "--verbose")
	}
	return runPush(pushArgs)
}

// baseLookup builds the 3-way-merge base fetcher for this host. The base is this machine's most
// recent snapshot (its last synced state = the correct common ancestor for its local edits); each
// lookup dumps one file from it. If this machine has never pushed, there is no ancestor and every
// lookup returns ok=false, so .md files fall back to newer-wins.
func baseLookup(ctx context.Context, repo restic.Repo, host string) (restore.BaseFunc, error) {
	snapID, ok, err := repo.LatestSnapshotIDForHost(ctx, host)
	if err != nil {
		return nil, err
	}
	if !ok {
		return func(string) ([]byte, bool) { return nil, false }, nil
	}
	paths, err := repo.SnapshotPaths(ctx, snapID)
	if err != nil {
		return nil, err
	}
	root := paths[0]
	return func(stagingRel string) ([]byte, bool) {
		b, err := repo.Dump(ctx, snapID, root+"/"+stagingRel)
		if err != nil {
			return nil, false
		}
		return b, true
	}, nil
}
