// text3.go adds a git-backed 3-way text merge to the merge package. Where JSONL() unions
// append-only logs, Text3Way handles files that are edited in place (project memory *.md): given
// two divergent versions and their common ancestor it combines non-overlapping edits and marks
// genuine collisions, so a sync never has to pick a winner and silently drop one side's work.
//
// It shells out to `git merge-file -p`, which does a standalone 3-way merge over three files with
// nothing tracked (no repo, no index). Used by internal/restore (writeFile) via a base fetched from
// restic; required by `mnemo sync`, which checks GitAvailable up front.
package merge

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitAvailable reports whether the git binary can be found. `mnemo sync` requires git for the
// 3-way merge, so it fails fast with an actionable message instead of a mid-run exec error.
func GitAvailable() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git binary not found in PATH: %w (install it, e.g. `brew install git`)", err)
	}
	return nil
}

// Text3Way performs a git 3-way merge of three text blobs. ours and theirs are the divergent
// versions, base their common ancestor. Non-overlapping edits are combined silently; regions both
// sides changed are wrapped in standard <<<<<<< ======= >>>>>>> markers. The int is the conflict
// count (0 = clean). Uses `git merge-file -p`, which writes the merged result to stdout and leaves
// the inputs untouched, so nothing is tracked.
func Text3Way(ours, base, theirs []byte) ([]byte, int, error) {
	dir, err := os.MkdirTemp("", "mnemo-merge-*")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(dir)
	for name, data := range map[string][]byte{"ours": ours, "base": base, "theirs": theirs} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return nil, 0, err
		}
	}
	cmd := exec.Command("git", "merge-file", "-p",
		filepath.Join(dir, "ours"), filepath.Join(dir, "base"), filepath.Join(dir, "theirs"))
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if err == nil {
		return out.Bytes(), 0, nil
	}
	// git merge-file exits with the number of conflicts (>0) on a successful-but-conflicted merge;
	// only a negative/other exit is a real failure.
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() > 0 {
		return out.Bytes(), ee.ExitCode(), nil
	}
	return nil, 0, fmt.Errorf("git merge-file: %w: %s", err, strings.TrimSpace(errBuf.String()))
}
