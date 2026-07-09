//go:build e2e

package command

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/opencode"
)

func TestOpenCodeStageRoundTrip(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skip("no opencode DB on this machine")
	}

	encHome := identity.EncodedHome(home)
	stageRoot := t.TempDir()

	// Stage
	count, err := opencode.Stage(dbPath, stageRoot, encHome)
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("expected at least 1 session")
	}

	// Verify staging tree structure
	byID, _ := os.ReadDir(filepath.Join(stageRoot, "by-id"))
	if len(byID) == 0 {
		t.Fatal("expected by-id/ directories")
	}
	found := 0
	for _, idDir := range byID {
		sessDir := filepath.Join(stageRoot, "by-id", idDir.Name(), "opencode-sessions")
		files, err := os.ReadDir(sessDir)
		if err == nil {
			found += len(files)
		}
	}
	if found != count {
		t.Fatalf("expected %d session files in staging tree, got %d", count, found)
	}

	// Verify machine rows exported
	if _, err := os.Stat(filepath.Join(stageRoot, "opencode-machine.jsonl")); os.IsNotExist(err) {
		t.Log("no opencode-machine.jsonl (expected on empty credential tables)")
	}

	// Verify each session JSONL and replay into a fresh DB
	targetDB := filepath.Join(t.TempDir(), "opencode.db")
	for _, idDir := range byID {
		sessDir := filepath.Join(stageRoot, "by-id", idDir.Name(), "opencode-sessions")
		files, err := os.ReadDir(sessDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(sessDir, f.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", f.Name(), err)
			}
			if len(data) == 0 {
				t.Fatalf("empty JSONL: %s", f.Name())
			}
		}
	}

	hostID, err := hostID()
	if err != nil {
		t.Fatal(err)
	}

	// Restore into fresh DB
	if err := opencode.Restore(stageRoot, targetDB, hostID, identity.EncodedHome(home), nil); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Re-restore idempotent check
	if err := opencode.Restore(stageRoot, targetDB, hostID, identity.EncodedHome(home), nil); err != nil {
		t.Fatalf("idempotent restore: %v", err)
	}

	t.Logf("opencode stage: %d sessions staged, restored, and verified idempotent", count)
}
