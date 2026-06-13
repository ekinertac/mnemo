// Package command implements Mnemo's CLI subcommands and the dispatch between them.
//
// Execute is the single entrypoint (called from package main). Each subcommand lives in
// its own file (push.go, pull.go, init.go, log.go, map.go, projects.go, machines.go) and
// exposes a `func(args []string) error`. This package owns argument parsing and config
// resolution; the actual engine work is delegated to internal/restic, internal/restore,
// internal/manifest, and internal/identity. Keeping dispatch here (rather than in main)
// means the CLI surface is testable and the entrypoint stays trivial.
//
// M2 adds map/projects/machines and resume-aware pull lay-down. Host-local overrides
// (written offline by `mnemo map`) live at ~/.config/mnemo/projects.json; they are overlaid
// onto the repo manifest at pull/projects time so an override applies on the next pull
// without requiring a push round-trip. See overlayLocalOverrides and localManifestPath.
//
// Config resolution (DESIGN §6.1): repo location comes from --repo flag, then MNEMO_REPO,
// then RESTIC_REPOSITORY (which restic itself reads). Secrets are never flags — the repo
// password lives in restic's own env mechanisms. See internal/restic for that boundary.
package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/restic"
)

// Execute dispatches argv (excluding the program name) to a subcommand and returns the
// process exit code. Unknown or missing subcommands print usage and exit non-zero — Mnemo
// never drops into an interactive menu (principle 8).
func Execute(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}

	sub, rest := args[0], args[1:]

	var err error
	switch sub {
	case "init":
		err = runInit(rest)
	case "push", "snapshot":
		err = runPush(rest)
	case "pull", "restore":
		err = runPull(rest)
	case "log":
		err = runLog(rest)
	case "map":
		err = runMap(rest)
	case "projects":
		err = runProjects(rest)
	case "machines":
		err = runMachines(rest)
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "mnemo: unknown command %q\n\n", sub)
		usage()
		return 2
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "mnemo: %v\n", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprint(os.Stderr, `mnemo — sync Claude Code sessions as restic snapshots

usage:
  mnemo init     [--repo PATH]                                     create/attach a restic repo
  mnemo push     [--repo PATH] [--path DIR]                        snapshot Claude sessions     (alias: snapshot)
  mnemo pull     [--repo PATH] [--snapshot ID] [--target DIR]      restore and lay down         (alias: restore)
                 [--lay-down=false]                                 (--lay-down=false: raw restore only)
  mnemo log      [--repo PATH]                                     list snapshots
  mnemo map      <identity> <local-path>                           record a host-local path override (offline)
  mnemo projects [--repo PATH] [--unmapped]                        list project identities and local resolution
  mnemo machines [--repo PATH]                                     list machines that have pushed

config:
  repo location:  --repo  >  $MNEMO_REPO  >  $RESTIC_REPOSITORY
  repo password:  $RESTIC_PASSWORD / $RESTIC_PASSWORD_FILE / $RESTIC_PASSWORD_COMMAND
                  (never a CLI flag — mnemo never prompts)
`)
}

// resolveRepo determines the restic repo to operate on and returns both a restic.Repo for
// the engine and a human-readable description for messaging. Precedence: --repo flag, then
// MNEMO_REPO, then RESTIC_REPOSITORY. When only RESTIC_REPOSITORY is set we leave
// restic.Repo.Repository empty and let restic read the env itself — so the displayed source
// reflects where the value actually came from.
func resolveRepo(flagRepo string) (restic.Repo, string) {
	if flagRepo != "" {
		return restic.Repo{Repository: flagRepo}, flagRepo + " (--repo)"
	}
	if v := os.Getenv("MNEMO_REPO"); v != "" {
		return restic.Repo{Repository: v}, v + " ($MNEMO_REPO)"
	}
	if v := os.Getenv("RESTIC_REPOSITORY"); v != "" {
		// Leave Repository empty: restic will read RESTIC_REPOSITORY from the inherited env.
		return restic.Repo{}, v + " ($RESTIC_REPOSITORY)"
	}
	return restic.Repo{}, "(unset — set --repo, $MNEMO_REPO, or $RESTIC_REPOSITORY)"
}

// defaultClaudeDir is the source root a push reads from: the whole ~/.claude tree. The
// ephemeral filter (internal/filter) decides what within it is durable — push never relies
// on this pointing at only session data, so widening from M0's projects/-only view to the
// full tree is safe (plans/, tasks/, history.jsonl are now picked up too).
func defaultClaudeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// stageRootDir is the fixed location Mnemo materializes its staging tree into before handing
// it to restic. It is intentionally STABLE across pushes (not a random temp dir): restic
// detects a parent snapshot by backup path, so a constant path lets incremental pushes skip
// rescanning unchanged files. It lives under the user cache dir (same filesystem as ~/.claude
// on a normal setup, so staging can hardlink instead of copy). The tree is rebuilt each push.
func stageRootDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve cache dir for staging: %w", err)
	}
	return filepath.Join(cache, "mnemo", "stage"), nil
}

// hostID returns this machine's hostname for snapshot tags and the manifest. Centralised here
// so push.go has a single call site — the host value is shared between the manifest stamp and
// the restic backup tag, and having one function makes the coupling obvious.
func hostID() (string, error) { return os.Hostname() }

// manifestStagePath is where projects.json lives inside the staging tree. The manifest is
// written into the staging root before restic runs so it is versioned inside the snapshot,
// making every backup self-describing (DESIGN §4.3).
func manifestStagePath(stageRoot string) string { return filepath.Join(stageRoot, "projects.json") }

// nowRFC3339 is the single point where wall-clock time enters the command layer. Keeping time
// out of the internal/* packages keeps them deterministically testable with injected timestamps.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// localManifestPath is this host's local override/bookkeeping store, written by `mnemo map`
// (offline, no restic) and overlaid onto the repo manifest at pull/projects time so an override
// applies immediately. Lives under the user config dir; the directory is created if absent.
func localManifestPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve config dir: %w", err)
	}
	dir := filepath.Join(cfg, "mnemo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "projects.json"), nil
}

// overlayLocalOverrides copies this host's overrides from the local store onto man, so an
// override set by `mnemo map` applies on the next pull/projects without a push round-trip.
// A missing local store is not an error — it just means no overrides have been set yet.
func overlayLocalOverrides(man *manifest.Manifest, host string) error {
	lpath, err := localManifestPath()
	if err != nil {
		return err
	}
	local, err := manifest.Load(lpath)
	if err != nil {
		return err
	}
	for id, p := range local.Overrides[host] {
		man.SetOverride(host, id, p)
	}
	return nil
}

// restoreStagingTreeTo restores the snapshot's staging tree into target, deriving the restore
// subpath from the SNAPSHOT's own recorded path (SnapshotPaths) rather than this machine's
// stageRootDir — so a tree pushed by any machine restores flat (by-id/, projects.json at the
// root of target) regardless of where that machine's cache dir lives. This is the fix for the
// cross-machine resume path; every reader of a pushed tree must go through here.
func restoreStagingTreeTo(ctx context.Context, repo restic.Repo, snapshot, target string) error {
	paths, err := repo.SnapshotPaths(ctx, snapshot)
	if err != nil {
		return err
	}
	// Mnemo backs up exactly one path (the pushing machine's staging root), so paths[0] is it.
	return repo.RestoreSubpath(ctx, snapshot, paths[0], target)
}

// restoreStagingTree restores the snapshot's staging tree into a fresh temp dir and returns the
// dir plus a cleanup func. Used by the read-only views (machines/projects) that need the tree
// transiently.
func restoreStagingTree(ctx context.Context, repo restic.Repo, snapshot string) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "mnemo-restore-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if err := restoreStagingTreeTo(ctx, repo, snapshot, tmp); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp, cleanup, nil
}
