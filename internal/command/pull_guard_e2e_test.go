//go:build e2e

// pull_guard_e2e_test.go verifies the pull safety guard end to end through a real restic round-trip.
// A memory note is pushed with an OLD mtime, then the local copy is edited to be NEWER and
// divergent; restore.LocalAhead must report it. This also proves restic preserves mtimes across
// backup/restore, which is the assumption the guard's "local is newer" signal depends on: if restic
// reset the restored mtime to now, it would look newer than the local file and the guard would miss
// it. Skips when the restic binary is absent.
package command

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ekinertac/mnemo/internal/filter"
	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/restic"
	"github.com/ekinertac/mnemo/internal/restore"
	"github.com/ekinertac/mnemo/internal/stage"
)

func TestPullGuardLocalAheadE2E(t *testing.T) {
	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		t.Skip("restic not available:", err)
	}
	t.Setenv("RESTIC_PASSWORD", "e2e-test")
	repo := restic.Repo{Repository: t.TempDir()}
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	encHome := "-Users-AAA"
	// ~/.claude at push time: a memory note stamped with an OLD mtime.
	src := t.TempDir()
	notePush := filepath.Join(src, "projects", encHome+"-Code-foo", "memory", "n.md")
	mustWrite(t, notePush, "base from remote\n")
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(notePush, old, old); err != nil {
		t.Fatal(err)
	}

	stageDir := t.TempDir()
	if _, err := stage.Build(src, stageDir, filter.Classifier{}, projectIdentityMapper(encHome)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Backup(ctx, []string{stageDir}, []string{"host=A"}, nil); err != nil {
		t.Fatal(err)
	}

	restored := t.TempDir()
	if err := restoreStagingTreeTo(ctx, repo, "latest", restored); err != nil {
		t.Fatal(err)
	}

	// Local ~/.claude now has NEWER, diverging edits to the same note (unpushed work).
	local := t.TempDir()
	noteLocal := filepath.Join(local, "projects", encHome+"-Code-foo", "memory", "n.md")
	mustWrite(t, noteLocal, "LOCAL newer work\n")
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(noteLocal, newer, newer); err != nil {
		t.Fatal(err)
	}

	ahead, err := restore.LocalAhead(restored, local, "A", encHome, manifest.New())
	if err != nil {
		t.Fatal(err)
	}
	want := "projects/-Users-AAA-Code-foo/memory/n.md"
	if len(ahead) != 1 || ahead[0] != want {
		t.Fatalf("LocalAhead = %v, want [%s] (guard must catch newer local through a real restic round-trip)", ahead, want)
	}
}
