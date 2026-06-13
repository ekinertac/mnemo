// push.go implements `mnemo push`: snapshot Claude session data into the restic repo.
//
// The pipeline is: build a filtered staging tree from ~/.claude (internal/stage, which applies
// the ephemeral filter), re-key its project sessions by identity, then `restic backup` that
// staging tree. Only durable session data reaches restic — scratch and config are pruned before
// the engine ever sees them (DESIGN §5.1/§5.4). The staging mapper rewrites
// projects/<encoded-cwd>/<rest> to by-id/<identity>/<rest> (internal/identity), so snapshots are
// machine-independent and a session pushed here resumes on another machine (DESIGN §5.2).
//
// Each push also stamps this host into projects.json in the staging root (internal/manifest) so
// the snapshot records which devices have pushed. The manifest is seeded from the latest snapshot
// before stamping (via loadRepoManifest in root.go) so the machines list accumulates across
// devices instead of being overwritten each push. Snapshots are tagged host=<machine> and
// mnemo=<schema-version> so the snapshot graph is self-describing (DESIGN §4.3).
package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ekinertac/mnemo/internal/filter"
	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/restic"
	"github.com/ekinertac/mnemo/internal/stage"
)

// projectIdentityMapper returns a stage.Mapper that rewrites
// `projects/<encoded-cwd>/<rest>` to `by-id/<identity>/<rest>`, making snapshots
// machine-independent. Everything else (history.jsonl, top-level config, etc.) passes through
// unchanged. Extracted from runPush so the e2e test and push both use the same code path —
// the mapper is the key correctness invariant for cross-home resume (DESIGN §5.2).
func projectIdentityMapper(encHome string) stage.Mapper {
	return func(rel string) string {
		relSlash := filepath.ToSlash(rel)
		const pfx = "projects/"
		if !strings.HasPrefix(relSlash, pfx) {
			return rel
		}
		rest := relSlash[len(pfx):]
		slash := strings.IndexByte(rest, '/')
		if slash < 0 {
			// bare `projects/<encoded>` with no tail — leave unchanged
			return rel
		}
		enc, tail := rest[:slash], rest[slash+1:]
		id := identity.FromEncoded(enc, encHome)
		return filepath.FromSlash("by-id/" + string(id) + "/" + tail)
	}
}

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

	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}

	// Resolve the repo early — loadRepoManifest needs it to seed the machines list before
	// the staging tree is built, so we can't defer this to just before Backup.
	repo, desc := resolveRepo(*repoFlag)

	// Build the encoded-home prefix so the mapper can tokenise projects/<encodedCwd> into
	// by-id/<identity> — turning machine-specific path segments into portable project keys.
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	encHome := identity.EncodedHome(home)

	fmt.Printf("mnemo: building staging tree from %s\n", src)
	res, err := stage.Build(src, stageRoot, filter.Classifier{}, projectIdentityMapper(encHome))
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

	// hostID is shared by the manifest stamp and the restic tag — single lookup, no drift.
	host, err := hostID()
	if err != nil {
		return fmt.Errorf("cannot resolve hostname: %w", err)
	}

	// Seed the manifest from the latest snapshot so the machines list accumulates across pushes
	// and devices; the first push (no snapshot yet) starts fresh rather than returning an error.
	// Ensure stageRoot exists so manifest.Save (which does not mkdir) never fails on an
	// unexpected layout where no durable files landed at root level.
	if err := os.MkdirAll(stageRoot, 0o755); err != nil {
		return fmt.Errorf("ensuring staging root for manifest: %w", err)
	}
	man := manifest.New()
	if prior, err := loadRepoManifest(ctx, repo); err == nil {
		man = prior
	}
	man.TouchMachine(host, nowRFC3339())
	mpath := manifestStagePath(stageRoot)
	if err := man.Save(mpath); err != nil {
		return err
	}

	tags := []string{"host=" + host, "mnemo=" + schemaVersion}

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
