// push.go implements `mnemo push`: snapshot Claude session data into the restic repo.
//
// The M1 pipeline is: build a filtered staging tree from ~/.claude (internal/stage, which
// applies the ephemeral filter), then `restic backup` that staging tree. Only durable
// session data reaches restic — scratch and config are pruned before the engine ever sees
// them (DESIGN §5.1/§5.4). The staging tree mirrors the ~/.claude layout for now; M2 re-keys
// it by project identity (DESIGN §5.2) so snapshots become machine-independent.
//
// Snapshots are tagged host=<machine> and mnemo=<schema-version> so the snapshot graph is
// self-describing and later filtering by device/version is possible (DESIGN §4.3).
package command

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ekinertac/mnemo/internal/filter"
	"github.com/ekinertac/mnemo/internal/restic"
	"github.com/ekinertac/mnemo/internal/stage"
)

// schemaVersion tags snapshots so future Mnemo versions can recognize the staging-tree
// layout they were written with. Bump when the on-disk snapshot shape changes.
const schemaVersion = "0"

func runPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	srcFlag := fs.String("path", "", "source root to filter & snapshot (default: ~/.claude)")
	dryRun := fs.Bool("dry-run", false, "build the staging tree and report what would be pushed, without backing up")
	if err := fs.Parse(args); err != nil {
		return err
	}

	src := *srcFlag
	if src == "" {
		s, err := defaultClaudeDir()
		if err != nil {
			return err
		}
		src = s
	}
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("nothing to push: %w", err)
	}

	stageRoot, err := stageRootDir()
	if err != nil {
		return err
	}
	// Rebuild staging from scratch each push so deleted source files don't linger as stale
	// hardlinks. The path itself stays stable for restic parent detection.
	if err := os.RemoveAll(stageRoot); err != nil {
		return fmt.Errorf("clearing stale staging tree: %w", err)
	}

	fmt.Printf("mnemo: building staging tree from %s\n", src)
	res, err := stage.Build(src, stageRoot, filter.Classifier{})
	if err != nil {
		return err
	}
	fmt.Printf("mnemo: staged %d durable files (%s); skipped %d ephemeral, %d config, %d unknown\n",
		res.Included, humanBytes(res.Bytes),
		res.Skipped[filter.Ephemeral], res.Skipped[filter.Config], res.Skipped[filter.Unknown])

	if res.Included == 0 {
		return fmt.Errorf("staging tree is empty — nothing durable found under %s", src)
	}

	if *dryRun {
		fmt.Printf("mnemo: --dry-run, not pushing. Staging tree left at %s for inspection.\n", stageRoot)
		return nil
	}
	// Tidy the staging tree after a real push; hardlinks are cheap but leaving them is messy.
	defer os.RemoveAll(stageRoot)

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
	fmt.Printf("mnemo: pushing staging tree -> %s (tags: %v)\n", desc, tags)
	return repo.Backup(ctx, []string{stageRoot}, tags)
}

// humanBytes formats a byte count for human-readable push summaries.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
