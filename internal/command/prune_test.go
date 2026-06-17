package command

import (
	"slices"
	"strings"
	"testing"
)

// The safety gate: an empty retention policy must REFUSE to build a forget command, so `mnemo
// prune` with no --keep-* flags can never delete anything. This is the structural guard against
// the claude-sync data-loss mode (retention quietly removing data).
func TestForgetArgsRefusesEmptyPolicy(t *testing.T) {
	if _, err := forgetArgs(retention{last: -1, daily: -1, weekly: -1, monthly: -1, yearly: -1}, true); err == nil {
		t.Fatal("expected error for empty retention policy, got nil")
	}
}

// Dry-run is the default: without --apply, the forget carries --dry-run so nothing is removed.
func TestForgetArgsDryRunByDefault(t *testing.T) {
	args, err := forgetArgs(retention{last: 10, daily: -1, weekly: -1, monthly: -1, yearly: -1}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"forget", "--prune", "--group-by", "host", "--keep-last", "10", "--dry-run"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %v\nwant %v", args, want)
	}
}

// With --apply, the --dry-run flag is gone (real deletion).
func TestForgetArgsApplyDropsDryRun(t *testing.T) {
	args, _ := forgetArgs(retention{last: 10, daily: -1, weekly: -1, monthly: -1, yearly: -1}, true)
	if slices.Contains(args, "--dry-run") {
		t.Errorf("apply=true must not include --dry-run: %v", args)
	}
}

// Per-host grouping is always present so pruning one machine's lineage never touches another's.
func TestForgetArgsGroupsByHost(t *testing.T) {
	args, _ := forgetArgs(retention{last: 5, daily: -1, weekly: -1, monthly: -1, yearly: -1}, true)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--group-by host") {
		t.Errorf("expected --group-by host in %v", args)
	}
}

// All keep dimensions map through, including --keep-within (a duration string).
func TestForgetArgsAllDimensions(t *testing.T) {
	args, err := forgetArgs(retention{last: 5, daily: 7, weekly: 4, monthly: 12, yearly: 3, within: "1y"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"--keep-last", "5"}, {"--keep-daily", "7"}, {"--keep-weekly", "4"},
		{"--keep-monthly", "12"}, {"--keep-yearly", "3"}, {"--keep-within", "1y"},
	} {
		i := slices.Index(args, pair[0])
		if i < 0 || i+1 >= len(args) || args[i+1] != pair[1] {
			t.Errorf("missing %s %s in %v", pair[0], pair[1], args)
		}
	}
}
