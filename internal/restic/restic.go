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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	// Verbose, when true, streams restic's native (technical) output to the user. When false
	// (the default), restic's chatter is suppressed and Mnemo prints its own plain-language
	// summaries instead — so `mnemo push`/`pull` read like a sync tool, not a git plumbing dump.
	Verbose bool
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

// runQuiet execs restic with output discarded (stderr captured into the error). Used for internal
// plumbing — manifest fetches, staging-tree restores — whose restic chatter ("Restored 1 files…")
// is noise the user never asked for; Mnemo prints its own summaries instead.
func (r Repo) runQuiet(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "restic", args...)
	cmd.Env = r.childEnv()
	cmd.Stdout = io.Discard
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic %s: %w: %s", args[0], err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

// maybeStream runs restic, streaming its native output only when Verbose; otherwise it runs quietly
// so Mnemo can present its own clean summary. Used by the restore paths.
func (r Repo) maybeStream(ctx context.Context, args ...string) error {
	if r.Verbose {
		return r.run(ctx, args...)
	}
	return r.runQuiet(ctx, args...)
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

// BackupSummary is the human-meaningful outcome of a backup, extracted from restic's --json
// output: which snapshot, how many files, and — the number users actually care about — how many
// bytes were really uploaded (data_added_packed, after dedup+compression), not the logical size.
type BackupSummary struct {
	SnapshotID     string
	FilesNew       int
	FilesChanged   int
	TotalFiles     int
	BytesUploaded  int64 // data_added_packed — actual bytes added to the repo
	BytesProcessed int64 // total_bytes_processed — logical size scanned
}

// BackupProgress is a live snapshot of a backup's progress, delivered to Backup's onProgress
// callback for each restic status message so the command layer can render a counter/bar.
type BackupProgress struct {
	FilesDone   int
	TotalFiles  int
	BytesDone   int64
	TotalBytes  int64
	PercentDone float64 // 0..1
}

// Backup snapshots the given paths into the repo (`restic backup`). tags are attached so later
// filtering by host/schema is possible. When Verbose, restic's native output streams and the
// returned summary is zero (the user is reading restic directly). Otherwise restic runs with
// --json: Backup parses the stream LIVE, invoking onProgress (may be nil) for each status update
// and returning a BackupSummary from the final summary message. stderr streams live either way so
// errors/lock-waits are never hidden.
func (r Repo) Backup(ctx context.Context, paths []string, tags []string, onProgress func(BackupProgress)) (BackupSummary, error) {
	if len(paths) == 0 {
		return BackupSummary{}, fmt.Errorf("backup: no paths given")
	}
	args := []string{"backup"}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	if r.Verbose {
		args = append(args, paths...)
		return BackupSummary{}, r.run(ctx, args...)
	}
	args = append(args, "--json")
	args = append(args, paths...)

	cmd := exec.CommandContext(ctx, "restic", args...)
	cmd.Env = r.childEnv()
	cmd.Stderr = os.Stderr // live, so lock-waits/errors are visible during a long push
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return BackupSummary{}, err
	}
	if err := cmd.Start(); err != nil {
		return BackupSummary{}, err
	}
	summary, perr := streamBackup(stdout, onProgress) // drains stdout until restic exits
	if werr := cmd.Wait(); werr != nil {
		return summary, fmt.Errorf("restic backup: %w", werr)
	}
	return summary, perr
}

// streamBackup reads restic's newline-delimited --json backup output, calling onProgress for each
// status message and capturing the single summary. It returns an error if no summary arrives (so a
// silent restic failure isn't reported as success).
func streamBackup(rd io.Reader, onProgress func(BackupProgress)) (BackupSummary, error) {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // restic status lines can be long (current_files)
	var summary BackupSummary
	haveSummary := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m struct {
			MessageType         string  `json:"message_type"`
			PercentDone         float64 `json:"percent_done"`
			TotalFiles          int     `json:"total_files"`
			FilesDone           int     `json:"files_done"`
			TotalBytes          int64   `json:"total_bytes"`
			BytesDone           int64   `json:"bytes_done"`
			SnapshotID          string  `json:"snapshot_id"`
			FilesNew            int     `json:"files_new"`
			FilesChanged        int     `json:"files_changed"`
			TotalFilesProcessed int     `json:"total_files_processed"`
			DataAddedPacked     int64   `json:"data_added_packed"`
			TotalBytesProcessed int64   `json:"total_bytes_processed"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		switch m.MessageType {
		case "status":
			if onProgress != nil {
				onProgress(BackupProgress{
					FilesDone: m.FilesDone, TotalFiles: m.TotalFiles,
					BytesDone: m.BytesDone, TotalBytes: m.TotalBytes, PercentDone: m.PercentDone,
				})
			}
		case "summary":
			summary = BackupSummary{
				SnapshotID:     m.SnapshotID,
				FilesNew:       m.FilesNew,
				FilesChanged:   m.FilesChanged,
				TotalFiles:     m.TotalFilesProcessed,
				BytesUploaded:  m.DataAddedPacked,
				BytesProcessed: m.TotalBytesProcessed,
			}
			haveSummary = true
		}
	}
	if err := sc.Err(); err != nil {
		return summary, err
	}
	if !haveSummary {
		return summary, fmt.Errorf("no summary in restic backup output")
	}
	return summary, nil
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
	return r.maybeStream(ctx, "restore", snapshot+":"+snapshotSubpath, "--target", target)
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
	return r.maybeStream(ctx, "restore", snapshot+":"+snapshotSubpath, "--include", include, "--target", target)
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

// RawDataSize returns the repository's ACTUAL footprint in bytes — the deduplicated, compressed
// data restic stores (`restic stats --mode raw-data`). This is the real bytes-on-backend figure,
// far smaller than any single snapshot's logical "restore size", and is what `mnemo log` surfaces
// so users don't mistake the per-snapshot size for what's uploaded or stored.
func (r Repo) RawDataSize(ctx context.Context) (int64, error) {
	out, err := r.runCapture(ctx, "stats", "--mode", "raw-data", "--json")
	if err != nil {
		return 0, err
	}
	// `restic stats` prints a scan-progress line before the JSON on stdout; start at the object.
	if i := strings.IndexByte(out, '{'); i >= 0 {
		out = out[i:]
	}
	var s struct {
		TotalSize int64 `json:"total_size"`
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return 0, fmt.Errorf("parsing restic stats json: %w", err)
	}
	return s.TotalSize, nil
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
