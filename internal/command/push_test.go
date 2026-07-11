// push_test.go unit-tests the push-side staging mapper, in particular its refusal to stage a
// project directory whose encoded identity is corrupt (an unexpanded ${HOME} that a broken restore
// left behind). Pairs with the restore-side guard in internal/restore (resolveDst) so neither
// direction ever propagates such garbage. Related: push.go (projectIdentityMapper).
package command

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectIdentityMapperSkipsCorruptIdentity(t *testing.T) {
	m := projectIdentityMapper("-Users-ekin")

	// A valid project path is rewritten to by-id/<identity>/...
	got := m(filepath.FromSlash("projects/-Users-ekin-Code-foo/s.jsonl"))
	if !strings.Contains(filepath.ToSlash(got), "by-id/") {
		t.Errorf("valid path mapped to %q, want a by-id/ path", got)
	}

	// A corrupt ${HOME} project dir is dropped: "" means "skip this file".
	if bad := m(filepath.FromSlash("projects/${HOME}-Code-foo/s.jsonl")); bad != "" {
		t.Errorf("corrupt ${HOME} path mapped to %q, want \"\" (skip)", bad)
	}

	// Non-project data passes straight through.
	if got := m("history.jsonl"); got != "history.jsonl" {
		t.Errorf("non-project path = %q, want unchanged", got)
	}
}
