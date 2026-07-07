//go:build e2e

// sync_e2e_test.go drives the merge tier through a real local restic repo and real git: machine A
// pushes a memory file, both A and B edit different lines, and a lay-down using A's snapshot as the
// base must preserve both edits. Skips when restic or git is absent (needs both binaries).
package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ekinertac/mnemo/internal/filter"
	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/merge"
	"github.com/ekinertac/mnemo/internal/restic"
	"github.com/ekinertac/mnemo/internal/restore"
	"github.com/ekinertac/mnemo/internal/stage"
)

func TestSyncThreeWayMergeE2E(t *testing.T) {
	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		t.Skip("restic not available:", err)
	}
	if err := merge.GitAvailable(); err != nil {
		t.Skip("git not available:", err)
	}
	t.Setenv("RESTIC_PASSWORD", "e2e-test")
	repoDir := t.TempDir()
	repo := restic.Repo{Repository: repoDir}
	if err := repo.Init(ctx); err != nil {
		t.Fatal("init:", err)
	}
	host := "machineA"
	encHome := identity.EncodedHome("/Users/aaa")

	// Machine A's ~/.claude with a memory file (the common ancestor).
	claudeA := t.TempDir()
	memRel := "projects/-Users-aaa-Code-foo/memory/n.md"
	writeFileTree(t, claudeA, map[string]string{memRel: "one\ntwo\nthree\n"})

	// Stage + push A -> this becomes the base snapshot for host A.
	stageA := t.TempDir()
	if _, err := stage.Build(claudeA, stageA, filter.Classifier{}, projectIdentityMapper(encHome)); err != nil {
		t.Fatal("stage A:", err)
	}
	if _, err := repo.Backup(ctx, []string{stageA}, []string{"host=" + host, "mnemo=0"}, nil); err != nil {
		t.Fatal("backup A:", err)
	}

	// A edits line 1 locally AFTER the push.
	writeFileTree(t, claudeA, map[string]string{memRel: "one A-EDIT\ntwo\nthree\n"})

	// Meanwhile "the server" (theirs) has A's file with line 3 edited — simulate by pushing a second
	// snapshot from a B-shaped stage, then restoring latest.
	claudeB := t.TempDir()
	writeFileTree(t, claudeB, map[string]string{"projects/-Users-aaa-Code-foo/memory/n.md": "one\ntwo\nthree B-EDIT\n"})
	stageB := t.TempDir()
	if _, err := stage.Build(claudeB, stageB, filter.Classifier{}, projectIdentityMapper(encHome)); err != nil {
		t.Fatal("stage B:", err)
	}
	if _, err := repo.Backup(ctx, []string{stageB}, []string{"host=machineB", "mnemo=0"}, nil); err != nil {
		t.Fatal("backup B:", err)
	}

	// Restore latest (B's snapshot) into a staging tree.
	restored, cleanup, err := restoreStagingTree(ctx, repo, "latest")
	if err != nil {
		t.Fatal("restore:", err)
	}
	defer cleanup()

	// Base fetcher = host A's last snapshot (the ancestor).
	base, err := baseLookup(ctx, repo, host)
	if err != nil {
		t.Fatal("baseLookup:", err)
	}

	rep, err := restore.LayDown(restored, claudeA, host, encHome, manifest.New(), base)
	if err != nil {
		t.Fatal("laydown:", err)
	}
	got, _ := os.ReadFile(filepath.Join(claudeA, filepath.FromSlash(memRel)))
	if !strings.Contains(string(got), "A-EDIT") || !strings.Contains(string(got), "B-EDIT") {
		t.Fatalf("3-way merge lost an edit:\n%s", got)
	}
	if len(rep.Conflicted) != 0 {
		t.Fatalf("unexpected conflicts: %v", rep.Conflicted)
	}
}

// writeFileTree writes rel->content under root, creating parents.
func writeFileTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, c := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
