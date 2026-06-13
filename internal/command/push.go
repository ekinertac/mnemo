// push.go implements `mnemo push`: snapshot Claude session data into the restic repo.
//
// At M0 this snapshots the raw ~/.claude/projects tree with no filtering or identity
// remapping — the goal is purely to prove the engine + backend path. Milestones M1/M2
// replace the source path with a filtered, identity-keyed staging tree (DESIGN §5.1/§5.4)
// built before this call; the restic invocation itself stays the same.
//
// Snapshots are tagged with host=<machine> and mnemo=<schema-version> so the snapshot graph
// is self-describing and later filtering by device/version is possible (DESIGN §4.3).
package command

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ekinertac/mnemo/internal/restic"
)

// schemaVersion tags snapshots so future Mnemo versions can recognize the staging-tree
// layout they were written with. Bump when the on-disk snapshot shape changes.
const schemaVersion = "0"

func runPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	pathFlag := fs.String("path", "", "directory to snapshot (default: ~/.claude/projects)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *pathFlag
	if path == "" {
		p, err := defaultProjectsDir()
		if err != nil {
			return err
		}
		path = p
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("nothing to push: %w", err)
	}

	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}

	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("cannot resolve hostname for snapshot tag: %w", err)
	}
	tags := []string{"host=" + host, "mnemo=" + schemaVersion}

	repo, desc := resolveRepo(*repoFlag)
	fmt.Printf("mnemo: pushing %s -> %s (tags: %v)\n", path, desc, tags)
	return repo.Backup(ctx, []string{path}, tags)
}
