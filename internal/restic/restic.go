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
	"fmt"
	"os"
	"os/exec"
)

// Repo identifies a restic repository for a sequence of operations.
//
// Repository is the restic repo location (a local path, or an s3:/b2:/rclone: URL). It is
// NOT a secret, so it is safe to set as an env var for the child process. If empty, we
// leave RESTIC_REPOSITORY untouched and let restic resolve the repo from its own
// environment — this lets the spike run purely off `RESTIC_REPOSITORY`/`RESTIC_PASSWORD`.
type Repo struct {
	Repository string
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
	cmd.Env = os.Environ()
	if r.Repository != "" {
		cmd.Env = append(cmd.Env, "RESTIC_REPOSITORY="+r.Repository)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic %s: %w", args[0], err)
	}
	return nil
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
// snapshotSubpath is the staging tree root as it was backed up (stageRootDir()).
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

// Snapshots lists the repo's snapshots (`restic snapshots`). Output streams to stdout; this
// is the M0 stand-in for `mnemo log`.
func (r Repo) Snapshots(ctx context.Context) error {
	return r.run(ctx, "snapshots")
}
