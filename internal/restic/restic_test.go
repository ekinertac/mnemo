package restic

import "testing"

// parseBackupSummary must pull the human-meaningful numbers out of restic's --json stream
// (a run of status messages followed by one summary), ignoring the status noise.
func TestParseBackupSummary(t *testing.T) {
	out := `{"message_type":"status","percent_done":0.5}
{"message_type":"status","percent_done":1}
{"message_type":"summary","files_new":3,"files_changed":491,"files_unmodified":0,"data_added":16955392,"data_added_packed":4138291,"total_files_processed":494,"total_bytes_processed":767000000,"snapshot_id":"5e4315ee1234abcd","total_duration":4.1}
`
	s, err := parseBackupSummary(out)
	if err != nil {
		t.Fatal(err)
	}
	if s.SnapshotID != "5e4315ee1234abcd" {
		t.Errorf("SnapshotID = %q", s.SnapshotID)
	}
	if s.FilesNew != 3 || s.FilesChanged != 491 || s.TotalFiles != 494 {
		t.Errorf("file counts wrong: %+v", s)
	}
	if s.BytesUploaded != 4138291 || s.BytesProcessed != 767000000 {
		t.Errorf("byte counts wrong: %+v", s)
	}
}

// No summary in the stream is an error (caller falls back gracefully).
func TestParseBackupSummaryMissing(t *testing.T) {
	if _, err := parseBackupSummary(`{"message_type":"status","percent_done":1}`); err == nil {
		t.Error("expected error when no summary line present")
	}
}
