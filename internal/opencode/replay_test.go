package opencode

import (
	"os"
	"testing"

	"github.com/ekinertac/mnemo/internal/identity"
)

func TestParseRowEvent(t *testing.T) {
	line := []byte(`{"type":"row","table":"session","id":"ses_1","data":{"id":"ses_1"},"timestamp":100}`)
	ev, err := parseRowEvent(line)
	if err != nil {
		t.Fatalf("parseRowEvent: %v", err)
	}
	if ev.Type != "row" || ev.Table != "session" || ev.ID != "ses_1" {
		t.Errorf("unexpected fields: %+v", ev)
	}
}

func TestParseRowEvent_invalid(t *testing.T) {
	cases := []struct {
		name string
		line []byte
	}{
		{"bad JSON", []byte(`{bad`)},
		{"wrong type", []byte(`{"type":"other","table":"session","id":"x","data":{},"timestamp":0}`)},
		{"empty table", []byte(`{"type":"row","table":"","id":"x","data":{},"timestamp":0}`)},
		{"empty id", []byte(`{"type":"row","table":"session","id":"","data":{},"timestamp":0}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseRowEvent(tc.line)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestReplaySession(t *testing.T) {
	db, dbPath := setupTestDB(t)
	defer db.Close()
	defer os.Remove(dbPath)

	jsonl := `{"type":"row","table":"session","id":"ses_1","data":{"id":"ses_1","project_id":"proj_1","directory":"/home/user/Code/foo","version":"1","time_created":100,"time_updated":200},"timestamp":200}
{"type":"row","table":"message","id":"msg_1","data":{"id":"msg_1","session_id":"ses_1","time_created":150,"time_updated":250,"data":"hello"},"timestamp":250}
{"type":"row","table":"part","id":"part_1","data":{"id":"part_1","message_id":"msg_1","session_id":"ses_1","time_created":160,"time_updated":260,"data":"world"},"timestamp":260}
`

	id := identity.Identity("home:-Code-foo")
	localHome := "/Users/localuser"
	err := ReplaySession(db, []byte(jsonl), id, localHome)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	// Verify session row
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
	if dir != "/Users/localuser/Code/foo" {
		t.Errorf("directory = %q, want /Users/localuser/Code/foo", dir)
	}

	// Verify message row
	var msgID, msgSessionID, msgData string
	err = db.QueryRow("SELECT id, session_id, data FROM message WHERE id = ?", "msg_1").
		Scan(&msgID, &msgSessionID, &msgData)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}
	if msgID != "msg_1" || msgSessionID != "ses_1" || msgData != "hello" {
		t.Errorf("message mismatch: %s %s %s", msgID, msgSessionID, msgData)
	}

	// Verify part row
	var partID, partMsgID, partSessionID, partData string
	err = db.QueryRow("SELECT id, message_id, session_id, data FROM part WHERE id = ?", "part_1").
		Scan(&partID, &partMsgID, &partSessionID, &partData)
	if err != nil {
		t.Fatalf("query part: %v", err)
	}
	if partID != "part_1" || partMsgID != "msg_1" || partSessionID != "ses_1" || partData != "world" {
		t.Errorf("part mismatch: %s %s %s %s", partID, partMsgID, partSessionID, partData)
	}

	// Idempotent: calling again should not error or create duplicates
	err = ReplaySession(db, []byte(jsonl), id, localHome)
	if err != nil {
		t.Fatalf("ReplaySession second call: %v", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM session").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 session after second replay, got %d", count)
	}
	db.QueryRow("SELECT COUNT(*) FROM message").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 message after second replay, got %d", count)
	}
	db.QueryRow("SELECT COUNT(*) FROM part").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 part after second replay, got %d", count)
	}
}

func TestPathRemap_homeTail(t *testing.T) {
	localHome := "/Users/localuser"
	id := identity.Identity("home:-Code-foo")
	got := remapPath("/Users/remoteuser/Code/foo", id, localHome)
	want := "/Users/localuser/Code/foo"
	if got != want {
		t.Errorf("remapPath = %q, want %q", got, want)
	}
}

func TestPathRemap_homeRoot(t *testing.T) {
	localHome := "/Users/localuser"
	id := identity.Identity("home:")
	got := remapPath("/Users/remoteuser", id, localHome)
	if got != localHome {
		t.Errorf("remapPath(root) = %q, want %q", got, localHome)
	}
}

func TestPathRemap_abs(t *testing.T) {
	localHome := "/Users/localuser"
	id := identity.Identity("abs:-opt-bar")
	sourcePath := "/opt/bar"
	got := remapPath(sourcePath, id, localHome)
	if got != sourcePath {
		t.Errorf("remapPath(abs) = %q, want %q", got, sourcePath)
	}
}

func TestPathRemap_multiLevel(t *testing.T) {
	localHome := "/home/new"
	id := identity.Identity("home:-Code-mnemo-internal-opencode")
	got := remapPath("/home/old/Code/mnemo/internal/opencode", id, localHome)
	want := "/home/new/Code/mnemo/internal/opencode"
	if got != want {
		t.Errorf("remapPath = %q, want %q", got, want)
	}
}

func TestReplayMachineRows(t *testing.T) {
	db, dbPath := setupTestDB(t)
	defer db.Close()
	defer os.Remove(dbPath)

	jsonl := `{"type":"row","table":"credential","id":"cred_1","data":{"id":"cred_1","integration_id":"github","label":"token","value":"ghp_abc","active":1,"time_created":100,"time_updated":200},"timestamp":200}
{"type":"row","table":"project","id":"proj_1","data":{"id":"proj_1","worktree":"/home/user/project","time_created":100,"time_updated":200},"timestamp":200}
`

	err := ReplayMachineRows(db, []byte(jsonl))
	if err != nil {
		t.Fatalf("ReplayMachineRows: %v", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM credential").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 credential, got %d", count)
	}
	db.QueryRow("SELECT COUNT(*) FROM project").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 project, got %d", count)
	}

	var label, value string
	err = db.QueryRow("SELECT label, value FROM credential WHERE id = ?", "cred_1").Scan(&label, &value)
	if err != nil {
		t.Fatalf("query credential: %v", err)
	}
	if label != "token" || value != "ghp_abc" {
		t.Errorf("credential mismatch: %s %s", label, value)
	}

	// Idempotent
	err = ReplayMachineRows(db, []byte(jsonl))
	if err != nil {
		t.Fatalf("ReplayMachineRows second call: %v", err)
	}
	db.QueryRow("SELECT COUNT(*) FROM credential").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 credential after second replay, got %d", count)
	}
}

func TestRecomputeEventSequence(t *testing.T) {
	db, dbPath := setupTestDB(t)
	defer db.Close()
	defer os.Remove(dbPath)

	_, err := db.Exec(`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
		"e1", "agg-1", 1, "created", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
		"e2", "agg-1", 2, "updated", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
		"e3", "agg-2", 5, "created", `{}`)
	if err != nil {
		t.Fatal(err)
	}

	err = RecomputeEventSequence(db)
	if err != nil {
		t.Fatalf("RecomputeEventSequence: %v", err)
	}

	var seq int64
	err = db.QueryRow("SELECT seq FROM event_sequence WHERE aggregate_id = ?", "agg-1").Scan(&seq)
	if err != nil {
		t.Fatalf("query agg-1: %v", err)
	}
	if seq != 2 {
		t.Errorf("agg-1 seq = %d, want 2", seq)
	}

	err = db.QueryRow("SELECT seq FROM event_sequence WHERE aggregate_id = ?", "agg-2").Scan(&seq)
	if err != nil {
		t.Fatalf("query agg-2: %v", err)
	}
	if seq != 5 {
		t.Errorf("agg-2 seq = %d, want 5", seq)
	}
}
