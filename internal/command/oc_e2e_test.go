//go:build e2e

package command

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/opencode"
	_ "github.com/mattn/go-sqlite3"
)

const opencodeSchema = `
CREATE TABLE IF NOT EXISTS session (
	id TEXT PRIMARY KEY, project_id TEXT, directory TEXT,
	title TEXT, slug TEXT, version INTEGER, permission TEXT,
	time_created INTEGER, time_updated INTEGER, time_compacting INTEGER,
	time_archived INTEGER, workspace_id TEXT, path TEXT,
	agent TEXT, model TEXT, cost TEXT, tokens_input INTEGER,
	tokens_output INTEGER, tokens_reasoning INTEGER, tokens_cache_read INTEGER,
	tokens_cache_write INTEGER, metadata TEXT, summary_additions TEXT,
	summary_deletions TEXT, summary_files TEXT, summary_diffs TEXT,
	revert TEXT, parent_id TEXT
);
CREATE TABLE IF NOT EXISTS message (
	id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER,
	time_updated INTEGER, data TEXT
);
CREATE TABLE IF NOT EXISTS part (
	id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT,
	time_created INTEGER, time_updated INTEGER, data TEXT
);
CREATE TABLE IF NOT EXISTS todo (
	session_id TEXT, content TEXT, position INTEGER,
	status TEXT, priority TEXT, time_created INTEGER, time_updated INTEGER,
	PRIMARY KEY (session_id, position)
);
CREATE TABLE IF NOT EXISTS event (
	id TEXT PRIMARY KEY, aggregate_id TEXT, seq INTEGER,
	type TEXT, data TEXT
);
CREATE TABLE IF NOT EXISTS event_sequence (
	aggregate_id TEXT PRIMARY KEY, seq INTEGER
);
CREATE TABLE IF NOT EXISTS session_message (
	id TEXT PRIMARY KEY, session_id TEXT, type TEXT,
	time_created INTEGER, time_updated INTEGER, data TEXT, seq INTEGER
);
CREATE TABLE IF NOT EXISTS session_input (
	id TEXT PRIMARY KEY, session_id TEXT, prompt TEXT,
	delivery TEXT, time_created INTEGER,
	admitted_seq INTEGER, promoted_seq INTEGER
);
CREATE TABLE IF NOT EXISTS session_context_epoch (
	session_id TEXT, baseline TEXT, snapshot TEXT, baseline_seq INTEGER
);
CREATE TABLE IF NOT EXISTS project (
	id TEXT PRIMARY KEY, worktree TEXT, vcs TEXT, name TEXT,
	icon_url TEXT, icon_color TEXT, time_created INTEGER,
	time_updated INTEGER, time_initialized INTEGER,
	sandboxes TEXT, commands TEXT, icon_url_override TEXT
);
CREATE TABLE IF NOT EXISTS credential (
	id TEXT PRIMARY KEY, integration_id TEXT, label TEXT,
	value TEXT, active INTEGER, time_created INTEGER,
	time_updated INTEGER, connector_id TEXT, method_id TEXT
);
CREATE TABLE IF NOT EXISTS account (
	id TEXT PRIMARY KEY, email TEXT, url TEXT,
	access_token TEXT, refresh_token TEXT,
	token_expiry INTEGER, time_created INTEGER, time_updated INTEGER
);
CREATE TABLE IF NOT EXISTS account_state (
	id INTEGER PRIMARY KEY, active_account_id TEXT, active_org_id TEXT
);
CREATE TABLE IF NOT EXISTS control_account (
	email TEXT, url TEXT, access_token TEXT, refresh_token TEXT,
	token_expiry INTEGER, active INTEGER,
	time_created INTEGER, time_updated INTEGER,
	PRIMARY KEY (email, url)
);
CREATE TABLE IF NOT EXISTS permission (
	id TEXT PRIMARY KEY, project_id TEXT, action TEXT,
	resource TEXT, time_created INTEGER, time_updated INTEGER
);`

func createOpenCodeDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(opencodeSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
}

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

	// Verify each session JSONL is non-empty
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

	// Create fresh target DB with schema
	targetDB := filepath.Join(t.TempDir(), "opencode.db")
	createOpenCodeDB(t, targetDB)

	// Restore into fresh DB
	if err := opencode.Restore(stageRoot, targetDB, hostID, identity.EncodedHome(home), nil); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Re-restore idempotent check
	if err := opencode.Restore(stageRoot, targetDB, hostID, identity.EncodedHome(home), nil); err != nil {
		t.Fatalf("idempotent restore: %v", err)
	}

	// Verify rows actually made it into the DB
	db, err := sql.Open("sqlite3", targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sessionCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM session").Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount == 0 {
		t.Fatal("restore verified: expected at least 1 session in target DB, got 0")
	}
	var messageCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM message").Scan(&messageCount); err != nil {
		t.Fatal(err)
	}
	t.Logf("opencode stage+restore: %d sessions, %d messages staged, restored, and idempotent verified", sessionCount, messageCount)
}
