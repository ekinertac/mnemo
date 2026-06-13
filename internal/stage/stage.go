// Package stage builds Mnemo's staging tree (DESIGN §5.4): a deterministic, filtered copy
// of the durable session files that is what we actually hand to `restic backup`. Staging is
// the seam between "what's on disk under ~/.claude" and "what goes in the repo", and it
// exists for two reasons:
//
//  1. Filtering — only files the ephemeral filter (internal/filter) marks Durable are
//     materialized, so scratch and config never reach restic.
//  2. Restructuring headroom — at M1 the staging layout mirrors ~/.claude, but M2 will
//     re-key paths by project identity (DESIGN §5.2) so a snapshot is machine-independent.
//     Excludes alone can't remap paths; a physical staging tree can. Building the machinery
//     now means M2 only changes the path-mapping function, not the pipeline.
//
// Materialization uses hardlinks when possible (zero extra bytes; restic reads file content
// through the link normally) and falls back to copying across filesystem boundaries. Walking
// prunes whole Config/Ephemeral subtrees (e.g. plugins/node_modules) instead of visiting
// every file — important given how large those caches get.
//
// Related: internal/filter (the classification rules this enforces), docs/DESIGN.md §5.1/§5.4.
package stage

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/filter"
)

// Result reports what a Build did, for messaging and `doctor`-style diagnostics. Skipped is
// keyed by the Class that caused the skip so we can say *why* things were left out, not just
// that they were.
type Result struct {
	Included int                  // files materialized into the staging tree
	Bytes    int64                // total bytes of included files
	Skipped  map[filter.Class]int // count of skipped files, by reason
}

// Build walks srcRoot (typically ~/.claude), classifies every entry with c, and materializes
// the Durable files into stageRoot at the same relative layout. Config/Ephemeral directories
// are pruned wholesale so their (often huge) subtrees are never walked. Unknown directories
// are still descended — they may contain durable files deeper down — but Unknown files are
// skipped. stageRoot is created if absent and is assumed to be empty/disposable.
func Build(srcRoot, stageRoot string, c filter.Classifier) (Result, error) {
	res := Result{Skipped: map[filter.Class]int{}}

	info, err := os.Stat(srcRoot)
	if err != nil {
		return res, fmt.Errorf("stage: source root %s: %w", srcRoot, err)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("stage: source root %s is not a directory", srcRoot)
	}

	walkErr := filepath.WalkDir(srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // the root itself
		}

		class := c.Classify(rel)

		if d.IsDir() {
			// Prune subtrees we'll never keep. Unknown dirs are NOT pruned: a durable file can
			// live beneath an unrecognized parent (e.g. projects/ itself classifies Unknown).
			if class == filter.Config || class == filter.Ephemeral {
				res.Skipped[class]++
				return fs.SkipDir
			}
			return nil
		}

		if !class.Include() {
			res.Skipped[class]++
			return nil
		}

		n, err := materialize(p, filepath.Join(stageRoot, rel))
		if err != nil {
			return err
		}
		res.Included++
		res.Bytes += n
		return nil
	})
	if walkErr != nil {
		return res, fmt.Errorf("stage: %w", walkErr)
	}
	return res, nil
}

// materialize places src at dst (creating parent dirs), preferring a hardlink and falling
// back to a content copy when linking isn't possible (cross-device, or the FS forbids it).
// Returns the file size so Build can total the bytes included.
func materialize(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	if err := os.Link(src, dst); err == nil {
		fi, statErr := os.Stat(dst)
		if statErr != nil {
			return 0, statErr
		}
		return fi.Size(), nil
	}
	// Fallback: copy bytes. Covers cross-filesystem staging and link-hostile backends.
	return copyFile(src, dst)
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err != nil {
		out.Close()
		return 0, err
	}
	return n, out.Close()
}
