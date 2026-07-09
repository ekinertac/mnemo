// Tests for Stage — verifies that sessions are written as JSONL under
// by-id/<identity>/opencode-sessions/ and machine rows to opencode-machine.jsonl.
package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ekinertac/mnemo/internal/identity"
)

func TestStage(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	// Insert a session under a home directory
	_, err := db.Exec(`INSERT INTO session (
		id, project_id, directory, version, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"ses_home", "proj1", "/home/user/project-a", "1", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a global session (project_id="global", directory="")
	_, err = db.Exec(`INSERT INTO session (
		id, project_id, directory, version, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"ses_global", "global", "", "1", 300, 400)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a credential so machine rows are non-empty
	_, err = db.Exec(`INSERT INTO credential (
		id, integration_id, label, value, active, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"cred_1", "github", "token", "ghp_abc", 1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	stageRoot := t.TempDir()
	encHome := identity.EncodedHome("/home/user")

	count, err := Stage(path, stageRoot, encHome)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 sessions staged, got %d", count)
	}

	// Home session: dir /home/user/project-a → encoded -home-user-project-a
	// → identity home:-project-a → path-safe home_-project-a
	homeFile := filepath.Join(stageRoot, "by-id/home_-project-a", "opencode-sessions", "ses_home.jsonl")
	if _, err := os.Stat(homeFile); os.IsNotExist(err) {
		t.Fatalf("home session file not found: %s", homeFile)
	}

	// Global session: dir="" + project_id="global" → dir="/" → encoded "-"
	// → identity abs:- → path-safe abs_-
	globalFile := filepath.Join(stageRoot, "by-id/abs_-", "opencode-sessions", "ses_global.jsonl")
	if _, err := os.Stat(globalFile); os.IsNotExist(err) {
		t.Fatalf("global session file not found: %s", globalFile)
	}

	machineFile := filepath.Join(stageRoot, "opencode-machine.jsonl")
	if _, err := os.Stat(machineFile); os.IsNotExist(err) {
		t.Fatalf("machine file not found: %s", machineFile)
	}

	for _, fp := range []string{homeFile, globalFile, machineFile} {
		data, err := os.ReadFile(fp)
		if err != nil {
			t.Fatalf("read %s: %v", fp, err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		for i, line := range lines {
			var rec lineRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("%s line %d: invalid JSON: %v", fp, i, err)
			}
			if rec.Type != "row" {
				t.Errorf("%s line %d: type = %q, want 'row'", fp, i, rec.Type)
			}
		}
	}
}

func TestStage_missingDB(t *testing.T) {
	count, err := Stage("/nonexistent/db.sqlite", t.TempDir(), "")
	if err != nil {
		t.Fatalf("Stage(missing): %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 for missing DB, got %d", count)
	}
}

func TestStage_zeroSessions(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	stageRoot := t.TempDir()
	count, err := Stage(path, stageRoot, "")
	if err != nil {
		t.Fatalf("Stage(empty): %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 for empty DB, got %d", count)
	}
}


