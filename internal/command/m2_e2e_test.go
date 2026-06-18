//go:build e2e

// m2_e2e_test.go drives the full M2 stack against a real local restic repo: stage a synthetic
// tree as "machine A", back it up, restore it, and lay it down as "machine B" with a DIFFERENT
// home — proving cross-machine resume. Also pushes as two hosts to prove the machines list
// accumulates across pushes (the functional gap fixed in Task 8). Build-tagged `e2e` so the
// default offline test suite stays fast; the tag also documents that these tests shell out to
// the real `restic` binary and need it in PATH.
package command

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ekinertac/mnemo/internal/filter"
	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/restic"
	"github.com/ekinertac/mnemo/internal/restore"
	"github.com/ekinertac/mnemo/internal/stage"
)

// mustWrite creates parent directories and writes content to path. Fails the test on error.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mustWrite MkdirAll %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("mustWrite WriteFile %s: %v", path, err)
	}
}

// TestM2CrossHomeResume stages a synthetic ~/.claude tree as "machine A" (encoded home
// -Users-AAA), backs it up to a local restic repo, restores the staging tree, and lays it
// down as "machine B" (encoded home -Users-BBB) — proving that the identity mapper and restore
// layer correctly re-home a session across machines with different home directories.
func TestM2CrossHomeResume(t *testing.T) {
	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		t.Skip("restic not available:", err)
	}
	t.Setenv("RESTIC_PASSWORD", "e2e-test")

	repoDir := t.TempDir()
	repo := restic.Repo{Repository: repoDir}
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// "Machine A": synthetic ~/.claude with one project session under encoded home -Users-AAA.
	// The mapper will turn projects/-Users-AAA-Code-foo/s.jsonl → by-id/home_-Code-foo/s.jsonl.
	srcA := t.TempDir()
	encHomeA := "-Users-AAA"
	mustWrite(t, filepath.Join(srcA, "projects", encHomeA+"-Code-foo", "s.jsonl"), "hello\n")

	// Stage the tree using the same projectIdentityMapper that push.go uses — one code path,
	// so the test catches any regression in the mapper itself.
	stageA := t.TempDir()
	if _, err := stage.Build(srcA, stageA, filter.Classifier{}, projectIdentityMapper(encHomeA)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Backup(ctx, []string{stageA}, []string{"host=machineA"}); err != nil {
		t.Fatal(err)
	}

	// Restore the staging tree (subpath derived from the snapshot, not from this machine's
	// cache dir) into a temp dir — this exercises the same path that pull.go takes.
	restored := t.TempDir()
	if err := restoreStagingTreeTo(ctx, repo, "latest", restored); err != nil {
		t.Fatal(err)
	}

	// "Machine B": lay down with a DIFFERENT encoded home -Users-BBB. The restore layer should
	// re-home by-id/home_-Code-foo/s.jsonl → projects/-Users-BBB-Code-foo/s.jsonl.
	claudeB := t.TempDir()
	rep, err := restore.LayDown(restored, claudeB, "machineB", "-Users-BBB", manifest.New())
	if err != nil {
		t.Fatal(err)
	}

	got := filepath.Join(claudeB, "projects", "-Users-BBB-Code-foo", "s.jsonl")
	b, readErr := os.ReadFile(got)
	if readErr != nil || string(b) != "hello\n" {
		t.Fatalf("cross-home transcript missing/wrong at %s: err=%v content=%q", got, readErr, b)
	}
	if rep.LaidDown != 1 {
		t.Errorf("LaidDown = %d, want 1", rep.LaidDown)
	}
}

// TestM2MachineAccumulation pushes two staging trees (one stamped by "machineA", one by
// "machineB") into the same repo and asserts that loadRepoManifest sees BOTH hosts in the
// final snapshot. This directly tests the Part B accumulation path: each push seeds from the
// prior snapshot, so machines accumulate rather than overwriting each other.
func TestM2MachineAccumulation(t *testing.T) {
	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		t.Skip("restic not available:", err)
	}
	t.Setenv("RESTIC_PASSWORD", "e2e-test")

	repoDir := t.TempDir()
	repo := restic.Repo{Repository: repoDir}
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Push 1: stamp machineA into a fresh manifest, save into stageA, back up.
	stageA := t.TempDir()
	manA := manifest.New()
	manA.TouchMachine("machineA", "2026-01-01T00:00:00Z")
	if err := manA.Save(filepath.Join(stageA, "projects.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Backup(ctx, []string{stageA}, []string{"host=machineA"}); err != nil {
		t.Fatal(err)
	}

	// Push 2: seed from the repo (gets machineA), stamp machineB, back up.
	// This exercises the exact accumulation logic from Part B.
	stageB := t.TempDir()
	manB, err := loadRepoManifest(ctx, repo)
	if err != nil {
		t.Fatalf("loadRepoManifest after first push: %v", err)
	}
	// machineA must already be in the seeded manifest.
	if _, ok := manB.Machines["machineA"]; !ok {
		t.Fatalf("seeded manifest missing machineA; machines = %v", manB.Machines)
	}
	manB.TouchMachine("machineB", "2026-01-02T00:00:00Z")
	if err := manB.Save(filepath.Join(stageB, "projects.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Backup(ctx, []string{stageB}, []string{"host=machineB"}); err != nil {
		t.Fatal(err)
	}

	// Now load the manifest from the latest snapshot and assert both hosts are present.
	final, err := loadRepoManifest(ctx, repo)
	if err != nil {
		t.Fatalf("loadRepoManifest after second push: %v", err)
	}
	if _, ok := final.Machines["machineA"]; !ok {
		t.Errorf("final manifest missing machineA; machines = %v", final.Machines)
	}
	if _, ok := final.Machines["machineB"]; !ok {
		t.Errorf("final manifest missing machineB; machines = %v", final.Machines)
	}
}
