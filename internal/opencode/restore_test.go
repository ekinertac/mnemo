package opencode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ekinertac/mnemo/internal/identity"
)

func TestRestore(t *testing.T) {
	db, dbPath := setupTestDB(t)
	defer db.Close()
	defer os.Remove(dbPath)

	restoredRoot := t.TempDir()

	id := identity.Identity("home:-Code-foo")
	idSafe := identity.PathSafe(id)
	sessionsDir := filepath.Join(restoredRoot, "by-id", idSafe, "opencode-sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatal(err)
	}

	sessionJSONL := `{"type":"row","table":"session","id":"ses_1","data":{"id":"ses_1","project_id":"proj_1","directory":"/Users/remote/Code/foo","version":"1","time_created":100,"time_updated":200},"timestamp":200}
{"type":"row","table":"message","id":"msg_1","data":{"id":"msg_1","session_id":"ses_1","time_created":150,"time_updated":250,"data":"hello"},"timestamp":250}
`
	if err := os.WriteFile(filepath.Join(sessionsDir, "ses_1.jsonl"), []byte(sessionJSONL), 0644); err != nil {
		t.Fatal(err)
	}

	machineJSONL := `{"type":"row","table":"credential","id":"cred_1","data":{"id":"cred_1","integration_id":"github","label":"token","value":"ghp_abc","active":1,"time_created":100,"time_updated":200},"timestamp":200}
`
	if err := os.WriteFile(filepath.Join(restoredRoot, "opencode-machine.jsonl"), []byte(machineJSONL), 0644); err != nil {
		t.Fatal(err)
	}

	err := Restore(restoredRoot, dbPath, "testhost", "-Users-remote", nil)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expectedDir := filepath.Join(home, "Code", "foo")

	var sesID, projectID, dir, version string
	var tc, tu int64
	err = db.QueryRow("SELECT id, project_id, directory, version, time_created, time_updated FROM session WHERE id = ?", "ses_1").
		Scan(&sesID, &projectID, &dir, &version, &tc, &tu)
	if err != nil {
		t.Fatalf("query session: %v", err)
	}
	if sesID != "ses_1" || projectID != "proj_1" || version != "1" || tc != 100 || tu != 200 {
		t.Errorf("session mismatch: %s %s %s %d %d", sesID, projectID, version, tc, tu)
	}
	if dir != expectedDir {
		t.Errorf("directory = %q, want %q", dir, expectedDir)
	}

	var msgID, msgSessionID, msgData string
	err = db.QueryRow("SELECT id, session_id, data FROM message WHERE id = ?", "msg_1").
		Scan(&msgID, &msgSessionID, &msgData)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if msgID != "msg_1" || msgSessionID != "ses_1" || msgData != "hello" {
		t.Errorf("message mismatch: %s %s %s", msgID, msgSessionID, msgData)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM credential").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 credential, got %d", count)
	}

	err = db.QueryRow("SELECT COUNT(*) FROM event_sequence").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 event_sequence rows, got %d", count)
	}
}

func TestRestore_idempotent(t *testing.T) {
	db, dbPath := setupTestDB(t)
	defer db.Close()
	defer os.Remove(dbPath)

	restoredRoot := t.TempDir()

	id := identity.Identity("home:-Code-bar")
	idSafe := identity.PathSafe(id)
	sessionsDir := filepath.Join(restoredRoot, "by-id", idSafe, "opencode-sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatal(err)
	}

	sessionJSONL := `{"type":"row","table":"session","id":"ses_1","data":{"id":"ses_1","project_id":"proj_1","directory":"/home/user/Code/bar","version":"1","time_created":100,"time_updated":200},"timestamp":200}
`
	if err := os.WriteFile(filepath.Join(sessionsDir, "ses_1.jsonl"), []byte(sessionJSONL), 0644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := Restore(restoredRoot, dbPath, "testhost", "-Users-remote", nil); err != nil {
			t.Fatalf("Restore call %d: %v", i+1, err)
		}
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM session").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 session after idempotent restore, got %d", count)
	}
}

func TestRestore_missingDB(t *testing.T) {
	err := Restore(t.TempDir(), "/nonexistent/path/db.db", "testhost", "-Users-remote", nil)
	if err != nil {
		t.Fatalf("Restore with missing DB should be non-fatal, got: %v", err)
	}
}

func TestRestore_missingByID(t *testing.T) {
	db, dbPath := setupTestDB(t)
	defer db.Close()
	defer os.Remove(dbPath)

	err := Restore(t.TempDir(), dbPath, "testhost", "-Users-remote", nil)
	if err != nil {
		t.Fatalf("Restore with missing by-id should be non-fatal, got: %v", err)
	}
}

func TestRestore_missingOpendcodeSessions(t *testing.T) {
	db, dbPath := setupTestDB(t)
	defer db.Close()
	defer os.Remove(dbPath)

	restoredRoot := t.TempDir()
	idSafe := identity.PathSafe(identity.Identity("home:-Code-baz"))
	os.MkdirAll(filepath.Join(restoredRoot, "by-id", idSafe), 0755)

	err := Restore(restoredRoot, dbPath, "testhost", "-Users-remote", nil)
	if err != nil {
		t.Fatalf("Restore with missing opencode-sessions should be non-fatal, got: %v", err)
	}
}

func TestRestore_nonJSONLFile(t *testing.T) {
	db, dbPath := setupTestDB(t)
	defer db.Close()
	defer os.Remove(dbPath)

	restoredRoot := t.TempDir()
	idSafe := identity.PathSafe(identity.Identity("home:-Code-foo"))
	sessionsDir := filepath.Join(restoredRoot, "by-id", idSafe, "opencode-sessions")
	os.MkdirAll(sessionsDir, 0755)

	if err := os.WriteFile(filepath.Join(sessionsDir, "readme.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	err := Restore(restoredRoot, dbPath, "testhost", "-Users-remote", nil)
	if err != nil {
		t.Fatalf("Restore with non-jsonl file: %v", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM session").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 sessions, got %d", count)
	}
}
