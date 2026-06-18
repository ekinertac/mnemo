// Package restic is Mnemo's thin wrapper around the `restic` command-line binary.
//
// Mnemo deliberately does NOT vendor restic as a library (its internal API is not a
// stability contract — see DESIGN §3). Instead every restic operation is an exec of the
// `restic` process with our environment flowing through. This package is the single
// chokepoint for that: every other part of Mnemo asks this package to back up, restore,
// init, or list snapshots, and never builds a `restic` command itself. That keeps the
// "how do we talk to the engine" decision in one place so it can be swapped (embedded
// library, different flags, retries) without touching callers.
//
// Secrets handling (DESIGN §6.1, principle 8): we never pass the repository password as
// a CLI flag — restic reads it from RESTIC_PASSWORD / RESTIC_PASSWORD_FILE /
// RESTIC_PASSWORD_COMMAND in the inherited environment. Mnemo only sets RESTIC_REPOSITORY
// when the caller resolved a repo location, and otherwise lets restic's own env win.
//
// Related: internal/command/* call into this; docs/DESIGN.md §3 (architecture) and §6.1
// (config & secrets) explain why the boundary is drawn here.
package restic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Repo identifies a restic repository for a sequence of operations.
//
// Repository is the restic repo location (a local path, or an s3:/b2:/rclone: URL). It is
// NOT a secret, so it is safe to set as an env var for the child process. If empty, we
// leave RESTIC_REPOSITORY untouched and let restic resolve the repo from its own
// environment — this lets the spike run purely off `RESTIC_REPOSITORY`/`RESTIC_PASSWORD`.
//
// Env carries additional environment variables to set on the restic child — resolved by the
// command layer from config (e.g. RESTIC_PASSWORD and AWS_* fetched from the OS keychain), so a
// user need not export them by hand. The command layer only puts here secrets NOT already in the
// process environment, so these never shadow an explicit env override.
type Repo struct {
	Repository string
	Env        map[string]string
}

// childEnv builds the environment for a restic child: the inherited environment, plus
// RESTIC_REPOSITORY when set, plus any config-resolved Env. Appended last so they take effect,
// without duplicating values the command layer already found in the environment.
func (r Repo) childEnv() []string {
	env := os.Environ()
	if r.Repository != "" {
		env = append(env, "RESTIC_REPOSITORY="+r.Repository)
	}
	for k, v := range r.Env {
		env = append(env, k+"="+v)
	}
	return env
}

// Available reports whether the restic binary can be found and executed. Callers use this
// to fail fast with an actionable message instead of a cryptic exec error mid-operation.
func Available(ctx context.Context) error {
	if _, err := exec.LookPath("restic"); err != nil {
		return fmt.Errorf("restic binary not found in PATH: %w (install it, e.g. `brew install restic`)", err)
	}
	cmd := exec.CommandContext(ctx, "restic", "version")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("found restic but `restic version` failed: %w", err)
	}
	return nil
}

// run execs `restic <args...>`, streaming the child's stdout/stderr straight to ours so
// the user sees restic's native progress and error output unmediated. The child inherits
// our full environment (so RESTIC_PASSWORD* and backend creds flow through), and we layer
// RESTIC_REPOSITORY on top only when r.Repository is set.
func (r Repo) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "restic", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = r.childEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic %s: %w", args[0], err)
	}
	return nil
}

// runCapture is like run but captures stdout (for commands whose output we parse, e.g. --json).
// Unlike run, it captures stderr into the returned error rather than streaming it live: callers
// like SnapshotPaths probe best-effort (e.g. "is there a prior snapshot?"), and restic's benign
// "no snapshot matched" notice on stderr would otherwise look like an error on a fresh repo's
// first push. On a real failure the stderr text is preserved in the error for diagnosis.
func (r Repo) runCapture(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "restic", args...)
	cmd.Env = r.childEnv()
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("restic %s: %w: %s", args[0], err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

// SnapshotPaths returns the backup paths recorded in the given snapshot (default "latest").
// Mnemo always backs up exactly one path — the PUSHING machine's staging root — so callers use
// the returned path as the restore subpath. Deriving it from the snapshot (not from this
// machine's stageRootDir) is what makes cross-machine restore work: each machine's UserCacheDir
// differs, but the snapshot records whatever path it was actually backed up from.
func (r Repo) SnapshotPaths(ctx context.Context, snapshot string) ([]string, error) {
	if snapshot == "" {
		snapshot = "latest"
	}
	out, err := r.runCapture(ctx, "snapshots", snapshot, "--json")
	if err != nil {
		return nil, err
	}
	var snaps []struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal([]byte(out), &snaps); err != nil {
		return nil, fmt.Errorf("parsing restic snapshots json: %w", err)
	}
	if len(snaps) == 0 || len(snaps[len(snaps)-1].Paths) == 0 {
		return nil, fmt.Errorf("no snapshot or backup path found for %q", snapshot)
	}
	return snaps[len(snaps)-1].Paths, nil
}

// Init creates a new restic repository (`restic init`). It is an error if the repo already
// exists — callers that mean "create or attach" should treat that as benign.
func (r Repo) Init(ctx context.Context) error {
	return r.run(ctx, "init")
}

// Backup snapshots the given paths into the repo (`restic backup`). tags are attached to
// the snapshot so later filtering (by host, schema version) is possible — at M0 callers
// pass host/mnemo tags so the snapshot graph is already self-describing.
func (r Repo) Backup(ctx context.Context, paths []string, tags []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("backup: no paths given")
	}
	args := []string{"backup"}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	args = append(args, paths...)
	return r.run(ctx, args...)
}

// Restore materializes a snapshot into target (`restic restore <snapshot> --target`).
// snapshot may be a snapshot ID or the literal "latest". At M0 we always restore into an
// explicit target dir (never over live ~/.claude), so the spike can prove restore without
// risking the authoritative local session set.
func (r Repo) Restore(ctx context.Context, snapshot, target string) error {
	if snapshot == "" {
		snapshot = "latest"
	}
	if target == "" {
		return fmt.Errorf("restore: empty target dir")
	}
	return r.run(ctx, "restore", snapshot, "--target", target)
}

// RestoreSubpath restores only the files rooted at snapshotSubpath (an absolute path within
// the snapshot tree) into target. restic's "snapshotID:subfolder" syntax strips the subfolder
// prefix so files land directly under target — this is what lets pull/projects/machines reach
// the staging root (by-id/, projects.json) without traversing a full absolute-path hierarchy.
// snapshotSubpath should be derived from the snapshot's own recorded path (SnapshotPaths),
// NOT from this machine's stageRootDir — they differ across OS/user, which is the root of the
// cross-machine restore bug.
func (r Repo) RestoreSubpath(ctx context.Context, snapshot, snapshotSubpath, target string) error {
	if snapshot == "" {
		snapshot = "latest"
	}
	if target == "" {
		return fmt.Errorf("restore: empty target dir")
	}
	// restic "snapshotID:/path/within/snapshot" syntax: restores that sub-tree directly
	// under --target without the leading absolute path components.
	return r.run(ctx, "restore", snapshot+":"+snapshotSubpath, "--target", target)
}

// RestoreSubpathInclude restores only the files rooted at snapshotSubpath AND matching the
// include pattern into target. It combines the "snapshotID:subfolder" syntax with --include so
// callers can fetch a single named file (e.g. projects.json) without restoring the entire tree.
// restic strips the subfolder prefix, so matched files land directly under target.
func (r Repo) RestoreSubpathInclude(ctx context.Context, snapshot, snapshotSubpath, include, target string) error {
	if snapshot == "" {
		snapshot = "latest"
	}
	if target == "" {
		return fmt.Errorf("restore: empty target dir")
	}
	return r.run(ctx, "restore", snapshot+":"+snapshotSubpath, "--include", include, "--target", target)
}

// Snapshots lists the repo's snapshots (`restic snapshots`). Output streams to stdout; this
// is the M0 stand-in for `mnemo log`.
func (r Repo) Snapshots(ctx context.Context) error {
	return r.run(ctx, "snapshots")
}

// Check verifies repository integrity (`restic check`). readData additionally re-reads and
// re-hashes every pack (`--read-data`) — thorough but slow and bandwidth-heavy on remote backends.
func (r Repo) Check(ctx context.Context, readData bool) error {
	args := []string{"check"}
	if readData {
		args = append(args, "--read-data")
	}
	return r.run(ctx, args...)
}

// Forget runs `restic forget` with caller-built arguments (see command.forgetArgs). The argument
// construction — including the dry-run/apply gate and per-host grouping — lives in the command
// layer so the safety policy is testable; this method just executes it.
func (r Repo) Forget(ctx context.Context, args []string) error {
	return r.run(ctx, args...)
}

// SnapshotCount returns how many snapshots the repo holds (0 is valid). It doubles as a
// reachability probe for `doctor`: an error means the repo couldn't be read (bad creds, network,
// missing repo), while a clean 0 means reachable-but-empty.
func (r Repo) SnapshotCount(ctx context.Context) (int, error) {
	out, err := r.runCapture(ctx, "snapshots", "--json")
	if err != nil {
		return 0, err
	}
	var snaps []json.RawMessage
	if err := json.Unmarshal([]byte(out), &snaps); err != nil {
		return 0, fmt.Errorf("parsing restic snapshots json: %w", err)
	}
	return len(snaps), nil
}
