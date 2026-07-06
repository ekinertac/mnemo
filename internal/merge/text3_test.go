package merge

import (
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if err := GitAvailable(); err != nil {
		t.Skip("git not available:", err)
	}
}

// Two machines each add a different line to different regions of the same file. Both edits must
// survive with no conflict markers.
func TestText3WayBothEditsSurvive(t *testing.T) {
	requireGit(t)
	base := []byte("alpha\nbeta\ngamma\n")
	ours := []byte("alpha EDIT\nbeta\ngamma\n")   // changed first line
	theirs := []byte("alpha\nbeta\ngamma EDIT\n") // changed last line
	got, conflicts, err := Text3Way(ours, base, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if conflicts != 0 {
		t.Fatalf("conflicts = %d, want 0", conflicts)
	}
	s := string(got)
	if !strings.Contains(s, "alpha EDIT") || !strings.Contains(s, "gamma EDIT") {
		t.Fatalf("merged result lost an edit:\n%s", s)
	}
	if strings.Contains(s, "<<<<<<<") {
		t.Fatalf("unexpected conflict markers:\n%s", s)
	}
}

// Both machines change the SAME line differently: a real conflict, reported with markers.
func TestText3WayConflictMarked(t *testing.T) {
	requireGit(t)
	base := []byte("shared\n")
	ours := []byte("ours version\n")
	theirs := []byte("theirs version\n")
	got, conflicts, err := Text3Way(ours, base, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if conflicts < 1 {
		t.Fatalf("conflicts = %d, want >= 1", conflicts)
	}
	if !strings.Contains(string(got), "<<<<<<<") {
		t.Fatalf("expected conflict markers, got:\n%s", got)
	}
}
