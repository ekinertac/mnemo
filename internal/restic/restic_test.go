package restic

import (
	"strings"
	"testing"
)

// streamBackup must (a) fire the progress callback for each status message with the live file/byte
// counts, and (b) extract the final summary's human-meaningful numbers, ignoring status noise.
func TestStreamBackup(t *testing.T) {
	stream := `{"message_type":"status","percent_done":0.25,"total_files":126,"files_done":30,"total_bytes":1000,"bytes_done":250}
{"message_type":"status","percent_done":0.5,"total_files":126,"files_done":63,"total_bytes":1000,"bytes_done":500}
{"message_type":"summary","files_new":3,"files_changed":491,"data_added_packed":4138291,"total_files_processed":494,"total_bytes_processed":767000000,"snapshot_id":"5e4315ee1234abcd"}
`
	var prog []BackupProgress
	s, err := streamBackup(strings.NewReader(stream), func(p BackupProgress) { prog = append(prog, p) })
	if err != nil {
		t.Fatal(err)
	}
	if s.SnapshotID != "5e4315ee1234abcd" || s.TotalFiles != 494 || s.BytesUploaded != 4138291 {
		t.Errorf("summary wrong: %+v", s)
	}
	if len(prog) != 2 {
		t.Fatalf("expected 2 progress callbacks, got %d", len(prog))
	}
	if prog[1].FilesDone != 63 || prog[1].TotalFiles != 126 || prog[1].PercentDone != 0.5 {
		t.Errorf("progress[1] wrong: %+v", prog[1])
	}
	if prog[1].BytesDone != 500 || prog[1].TotalBytes != 1000 {
		t.Errorf("progress[1] byte counts wrong: %+v", prog[1])
	}
}

// A nil callback is fine (caller doesn't want progress); summary still parses.
func TestStreamBackupNilCallback(t *testing.T) {
	s, err := streamBackup(strings.NewReader(
		`{"message_type":"status","files_done":1}`+"\n"+
			`{"message_type":"summary","snapshot_id":"abc","total_files_processed":2}`), nil)
	if err != nil || s.SnapshotID != "abc" {
		t.Errorf("got %+v, err %v", s, err)
	}
}

// No summary in the stream is an error (caller surfaces it rather than reporting a bogus success).
func TestStreamBackupNoSummary(t *testing.T) {
	if _, err := streamBackup(strings.NewReader(`{"message_type":"status","percent_done":1}`), nil); err == nil {
		t.Error("expected error when no summary present")
	}
}
