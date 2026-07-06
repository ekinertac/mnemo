package command

import "testing"

// `mnemo sync` must be a recognized subcommand (not fall through to the unknown-command path).
func TestSyncIsDispatched(t *testing.T) {
	// No repo configured -> runSync should fail while resolving the repo/backends, NOT return the
	// exit-2 "unknown command" code. Execute returns 1 on a command error.
	t.Setenv("MNEMO_REPO", "")
	t.Setenv("RESTIC_REPOSITORY", "")
	t.Setenv("MNEMO_CONFIG", "/nonexistent/does-not-exist.json")
	code := Execute([]string{"sync"})
	if code == 2 {
		t.Fatalf("`sync` returned unknown-command exit 2; it should be dispatched")
	}
}
