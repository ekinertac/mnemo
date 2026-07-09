// Tests for JSONL export of session and machine rows.
// Verifies that ExportSession and ExportMachineRows produce valid, sorted JSONL.
package opencode

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

// lineRecord is a minimal struct for verifying the shape of each JSONL line.
type lineRecord struct {
	Type      string         `json:"type"`
	Table     string         `json:"table"`
	ID        string         `json:"id"`
	Data      map[string]any `json:"data"`
	Timestamp int64          `json:"timestamp"`
}

func TestExportSession(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	// Insert session
	_, err := db.Exec(`INSERT INTO session (
		id, project_id, directory, version, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"ses_1", "proj_1", "/tmp/test", "1.0", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	// Insert 2 messages
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"msg_1", "ses_1", 150, 250, `{"role": "user", "content": "hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"msg_2", "ses_1", 180, 280, `{"role": "assistant", "content": "hi"}`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert 2 parts
	_, err = db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		"part_1", "msg_1", "ses_1", 160, 260, `{"type": "text", "text": "hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		"part_2", "msg_2", "ses_1", 190, 290, `{"type": "text", "text": "hi"}`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert 1 todo
	_, err = db.Exec(`INSERT INTO todo (session_id, content, status, priority, position, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"ses_1", "fix bug", "pending", "high", 1, 170, 270)
	if err != nil {
		t.Fatal(err)
	}

	// Insert 1 event
	_, err = db.Exec(`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
		"evt_1", "ses_1", 1, "created", `{"x": 1}`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert session_message
	_, err = db.Exec(`INSERT INTO session_message (id, session_id, type, time_created, time_updated, data, seq) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sm_1", "ses_1", "user", 140, 240, `{"prompt": "hi"}`, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Insert session_input
	_, err = db.Exec(`INSERT INTO session_input (id, session_id, prompt, delivery, admitted_seq, promoted_seq, time_created) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"si_1", "ses_1", "hello", "text", 1, 2, 130)
	if err != nil {
		t.Fatal(err)
	}

	// Insert session_context_epoch
	_, err = db.Exec(`INSERT INTO session_context_epoch (session_id, baseline, snapshot, baseline_seq) VALUES (?, ?, ?, ?)`,
		"ses_1", "base-v1", "snap-v1", 42)
	if err != nil {
		t.Fatal(err)
	}

	// Also insert data for a different session to verify it's not included
	_, err = db.Exec(`INSERT INTO session (
		id, project_id, directory, version, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"other_ses", "proj_2", "/other", "1.0", 500, 600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"other_msg", "other_ses", 550, 650, `{}`)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ExportSession(db, "ses_1")
	if err != nil {
		t.Fatalf("ExportSession: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	expectedCount := 1 + 2 + 2 + 1 + 1 + 1 + 1 + 1 // session + msgs + parts + todo + event + sm + si + epoch
	if len(lines) != expectedCount {
		t.Fatalf("expected %d lines, got %d", expectedCount, len(lines))
	}

	// Verify each line is valid JSON with expected fields
	var records []lineRecord
	var lastTS int64
	for i, line := range lines {
		var rec lineRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d: invalid JSON: %v\n%s", i, err, line)
		}
		if rec.Type != "row" {
			t.Errorf("line %d: type = %q, want %q", i, rec.Type, "row")
		}
		if rec.Timestamp < lastTS {
			t.Errorf("line %d: timestamp %d < previous %d (not sorted)", i, rec.Timestamp, lastTS)
		}
		lastTS = rec.Timestamp
		records = append(records, rec)
	}

	// Spot-check specific lines
	found := map[string]bool{}
	for _, r := range records {
		found[r.Table+":"+r.ID] = true
	}

	expectedIDs := map[string]string{
		"session:ses_1":         "ses_1",
		"message:msg_1":         "msg_1",
		"message:msg_2":         "msg_2",
		"part:part_1":           "part_1",
		"part:part_2":           "part_2",
		"todo:ses_1|1":          "ses_1|1",
		"event:evt_1":           "evt_1",
		"session_message:sm_1":  "sm_1",
		"session_input:si_1":    "si_1",
		"session_context_epoch:ses_1": "ses_1",
	}
	for key := range expectedIDs {
		if !found[key] {
			t.Errorf("missing line: %s", key)
		}
	}

	// Verify data fields in the session line
	var sessionRec lineRecord
	for _, r := range records {
		if r.Table == "session" {
			sessionRec = r
			break
		}
	}
	if sessionRec.Data["project_id"] != "proj_1" {
		t.Errorf("session project_id = %v, want proj_1", sessionRec.Data["project_id"])
	}
	if sessionRec.Data["directory"] != "/tmp/test" {
		t.Errorf("session directory = %v, want /tmp/test", sessionRec.Data["directory"])
	}

	// Verify other-session data is excluded
	if found["session:other_ses"] {
		t.Error("found row for other_ses which should be excluded")
	}
	if found["message:other_msg"] {
		t.Error("found message for other_ses which should be excluded")
	}
}

func TestExportSession_notFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := ExportSession(db, "nonexistent")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ExportSession(nonexistent) = %v, want 'not found' error", err)
	}
}

func TestExportMachineRows(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	// Insert a session (needed for FK references in some rows)
	_, err := db.Exec(`INSERT INTO session (
		id, project_id, directory, version, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"ses_ref", "proj_1", "/tmp", "1", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	// Credential
	_, err = db.Exec(`INSERT INTO credential (id, integration_id, label, value, active, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"cred_1", "github", "token", "ghp_abc", 1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	// Account
	_, err = db.Exec(`INSERT INTO account (id, email, url, access_token, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"acc_1", "user@x.com", "https://x.com", "tok1", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	// Account state
	_, err = db.Exec(`INSERT INTO account_state (id, active_account_id) VALUES (?, ?)`,
		1, "acc_1")
	if err != nil {
		t.Fatal(err)
	}

	// Control account
	_, err = db.Exec(`INSERT INTO control_account (email, url, access_token, active, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"admin@x.com", "https://x.com", "admintok", 1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	// Permission
	_, err = db.Exec(`INSERT INTO permission (id, project_id, action, resource, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"perm_1", "proj_1", "read", "session", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	// Project
	_, err = db.Exec(`INSERT INTO project (id, worktree, time_created, time_updated) VALUES (?, ?, ?, ?)`,
		"proj_1", "/tmp/work", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ExportMachineRows(db)
	if err != nil {
		t.Fatalf("ExportMachineRows: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	expectedCount := 1 + 1 + 1 + 1 + 1 + 1 // cred + acct + acct_state + ctrl_acct + perm + project
	if len(lines) != expectedCount {
		t.Fatalf("expected %d lines, got %d", expectedCount, len(lines))
	}

	var records []lineRecord
	var lastTS int64
	for i, line := range lines {
		var rec lineRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d: invalid JSON: %v\n%s", i, err, line)
		}
		if rec.Type != "row" {
			t.Errorf("line %d: type = %q, want %q", i, rec.Type, "row")
		}
		if rec.Timestamp < lastTS {
			t.Errorf("line %d: timestamp %d < previous %d (not sorted)", i, rec.Timestamp, lastTS)
		}
		lastTS = rec.Timestamp
		records = append(records, rec)
	}

	found := map[string]bool{}
	for _, r := range records {
		found[r.Table+":"+r.ID] = true
	}

	expectedIDs := []string{
		"credential:cred_1",
		"account:acc_1",
		"account_state:" + strconv.Itoa(1),
		"control_account:admin@x.com|https://x.com",
		"permission:perm_1",
		"project:proj_1",
	}
	for _, id := range expectedIDs {
		if !found[id] {
			t.Errorf("missing line: %s", id)
		}
	}

	// Verify control_account composite PK in data
	for _, r := range records {
		if r.Table == "control_account" {
			if r.Data["email"] != "admin@x.com" {
				t.Errorf("control_account email = %v, want admin@x.com", r.Data["email"])
			}
			if r.Data["url"] != "https://x.com" {
				t.Errorf("control_account url = %v, want https://x.com", r.Data["url"])
			}
		}
	}
}

func TestExportMachineRows_empty(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	out, err := ExportMachineRows(db)
	if err != nil {
		t.Fatalf("ExportMachineRows(empty): %v", err)
	}

	// Should have only a trailing newline (empty result)
	trimmed := strings.TrimSpace(string(out))
	if trimmed != "" {
		t.Fatalf("expected empty output, got %q", string(out))
	}
}

func TestLineFromRow(t *testing.T) {
	data := map[string]any{"foo": "bar"}
	line, err := lineFromRow("test_table", "test_id", data, 12345)
	if err != nil {
		t.Fatalf("lineFromRow: %v", err)
	}

	var rec lineRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if rec.Type != "row" {
		t.Errorf("type = %q, want %q", rec.Type, "row")
	}
	if rec.Table != "test_table" {
		t.Errorf("table = %q, want %q", rec.Table, "test_table")
	}
	if rec.ID != "test_id" {
		t.Errorf("id = %q, want %q", rec.ID, "test_id")
	}
	if rec.Timestamp != 12345 {
		t.Errorf("timestamp = %d, want %d", rec.Timestamp, 12345)
	}
	if rec.Data["foo"] != "bar" {
		t.Errorf("data.foo = %v, want %q", rec.Data["foo"], "bar")
	}
}

func TestExtractTimestamp(t *testing.T) {
	line, err := lineFromRow("t", "id", map[string]any{}, 999)
	if err != nil {
		t.Fatal(err)
	}
	if ts := extractTimestamp(line); ts != 999 {
		t.Errorf("extractTimestamp = %d, want %d", ts, 999)
	}
}

func TestExportSessionWithNilPointers(t *testing.T) {
	// Verify that nil pointer fields are omitted from the JSON output
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	// Insert session with all nullable fields as NULL
	_, err := db.Exec(`INSERT INTO session (
		id, project_id, directory, version, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"nil_test", "proj", "/dir", "1", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ExportSession(db, "nil_test")
	if err != nil {
		t.Fatalf("ExportSession: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var rec lineRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Fields that were set should be present
	if rec.Data["id"] != "nil_test" {
		t.Errorf("id = %v, want nil_test", rec.Data["id"])
	}
	if rec.Data["project_id"] != "proj" {
		t.Errorf("project_id = %v, want proj", rec.Data["project_id"])
	}

	// Nil pointer fields should not appear in the map
	nilFields := []string{"title", "slug", "permission", "time_compacting",
		"time_archived", "workspace_id", "path", "agent", "model", "cost",
		"tokens_input", "tokens_output", "tokens_reasoning", "tokens_cache_read",
		"tokens_cache_write", "metadata", "summary_additions", "summary_deletions",
		"summary_files", "summary_diffs", "revert", "parent_id"}
	for _, f := range nilFields {
		if _, ok := rec.Data[f]; ok {
			t.Errorf("nil field %q should be omitted but was present in data", f)
		}
	}
}

func TestExportSessionWithTodoPointerFields(t *testing.T) {
	// Verify that todo rows with nil status/priority omit those fields
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (
		id, project_id, directory, version, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"ses_todo", "proj", "/dir", "1", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	// Todo with nil status and priority
	_, err = db.Exec(`INSERT INTO todo (session_id, content, position, time_created, time_updated) VALUES (?, ?, ?, ?, ?)`,
		"ses_todo", "a task", 1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ExportSession(db, "ses_todo")
	if err != nil {
		t.Fatalf("ExportSession: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	// Find the todo line
	var todoRec lineRecord
	for _, line := range lines {
		if err := json.Unmarshal([]byte(line), &todoRec); err == nil && todoRec.Table == "todo" {
			break
		}
	}

	if todoRec.Table != "todo" {
		t.Fatal("todo line not found")
	}

	if _, ok := todoRec.Data["status"]; ok {
		t.Error("nil status field should be omitted from todo data")
	}
	if _, ok := todoRec.Data["priority"]; ok {
		t.Error("nil priority field should be omitted from todo data")
	}
	if todoRec.Data["content"] != "a task" {
		t.Errorf("content = %v, want 'a task'", todoRec.Data["content"])
	}
}

// TestExportSession_unsortedInput verifies that ExportSession sorts lines
// even when the input rows were inserted out of timestamp order.
func TestExportSession_unsortedInput(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (
		id, project_id, directory, version, time_created, time_updated
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"ses_sort", "proj", "/dir", "1", 300, 400)
	if err != nil {
		t.Fatal(err)
	}

	// Insert messages with decreasing timestamps
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"msg_late", "ses_sort", 500, 600, `{"x": 2}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"msg_early", "ses_sort", 100, 200, `{"x": 1}`)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ExportSession(db, "ses_sort")
	if err != nil {
		t.Fatalf("ExportSession: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	var recs []lineRecord
	for _, line := range lines {
		var rec lineRecord
		json.Unmarshal([]byte(line), &rec)
		recs = append(recs, rec)
	}

	// Should be sorted by timestamp: msg_early (200), session (400), msg_late (600)
	if recs[0].ID != "msg_early" {
		t.Errorf("first line id = %q, want msg_early", recs[0].ID)
	}
	if recs[1].ID != "ses_sort" {
		t.Errorf("second line id = %q, want ses_sort", recs[1].ID)
	}
	if recs[2].ID != "msg_late" {
		t.Errorf("third line id = %q, want msg_late", recs[2].ID)
	}
}
