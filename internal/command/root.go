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

	"github.com/ekinertac/mnemo/internal/config"
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
	case "verify":
		err = runVerify(rest)
	case "prune":
		err = runPrune(rest)
	case "doctor":
		err = runDoctor(rest)
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
  mnemo verify   [--repo PATH] [--read-data]                       check repo integrity (restic check)
  mnemo prune    [--repo PATH] --keep-* [--apply]                  apply retention (dry-run unless --apply)
  mnemo doctor   [--repo PATH]                                     health report (restic, repo, machines)

config:
  config file:    ~/.config/mnemo/config.json  (override with $MNEMO_CONFIG)
                  may set: repo, host, exclude globs, and secret references (command/file/env)
  repo location:  --repo  >  $MNEMO_REPO  >  $RESTIC_REPOSITORY  >  config "repo"
  secrets:        env wins over config; config refs fetch from e.g. the OS keychain, never plaintext
  repo password:  $RESTIC_PASSWORD / $RESTIC_PASSWORD_FILE / $RESTIC_PASSWORD_COMMAND, or config
                  (never a CLI flag — mnemo never prompts)
`)
}

// resolveRepo determines the restic repo to operate on and returns a restic.Repo (with the repo
// location and any config-resolved secret env), a human-readable description, and an error if
// config can't be loaded or a configured secret can't be fetched. Repo-location precedence
// (DESIGN §6.1): --repo flag → $MNEMO_REPO → $RESTIC_REPOSITORY → config `repo` → unset. Secret
// env (RESTIC_PASSWORD, AWS_*, …) is resolved from config for anything not already in the
// environment, so a user need not `source` a creds file before running mnemo.
func resolveRepo(flagRepo string) (restic.Repo, string, error) {
	cfg, err := loadConfigCached()
	if err != nil {
		return restic.Repo{}, "", err
	}
	env, err := cfg.ResolveEnv()
	if err != nil {
		return restic.Repo{}, "", err
	}

	var repository, desc string
	switch {
	case flagRepo != "":
		repository, desc = flagRepo, flagRepo+" (--repo)"
	case os.Getenv("MNEMO_REPO") != "":
		repository, desc = os.Getenv("MNEMO_REPO"), os.Getenv("MNEMO_REPO")+" ($MNEMO_REPO)"
	case os.Getenv("RESTIC_REPOSITORY") != "":
		// Leave Repository empty: restic reads RESTIC_REPOSITORY from the inherited env itself.
		repository, desc = "", os.Getenv("RESTIC_REPOSITORY")+" ($RESTIC_REPOSITORY)"
	case cfg.Repo != "":
		repository, desc = cfg.Repo, cfg.Repo+" (config)"
	default:
		repository, desc = "", "(unset — set --repo, $MNEMO_REPO, $RESTIC_REPOSITORY, or config `repo`)"
	}
	return restic.Repo{Repository: repository, Env: env}, desc, nil
}

// cachedConfig holds the once-loaded config for this process (a CLI run is one-shot, so loading
// once is fine and avoids re-running secret commands at every call site).
var cachedConfig *config.Config

func loadConfigCached() (*config.Config, error) {
	if cachedConfig != nil {
		return cachedConfig, nil
	}
	c, err := config.Load(configFilePath())
	if err != nil {
		return nil, err
	}
	cachedConfig = c
	return c, nil
}

// configFilePath is where Mnemo's config file lives: $MNEMO_CONFIG, else ~/.config/mnemo/config.json
// (DESIGN §6.1 — see mnemoConfigDir for why not os.UserConfigDir).
func configFilePath() string {
	if p := os.Getenv("MNEMO_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(mnemoConfigDir(), "config.json")
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

// hostID is this machine's identity for snapshot tags and the manifest. Precedence (DESIGN §6.1):
// $MNEMO_HOST → config `host` → OS hostname. The env override lets tests simulate multiple devices
// on one box (two pushes with different MNEMO_HOST values) without needing two machines.
func hostID() (string, error) {
	if h := os.Getenv("MNEMO_HOST"); h != "" {
		return h, nil
	}
	if cfg, err := loadConfigCached(); err == nil && cfg.Host != "" {
		return cfg.Host, nil
	}
	return os.Hostname()
}

// manifestStagePath is where projects.json lives inside the staging tree. The manifest is
// written into the staging root before restic runs so it is versioned inside the snapshot,
// making every backup self-describing (DESIGN §4.3).
func manifestStagePath(stageRoot string) string { return filepath.Join(stageRoot, "projects.json") }

// nowRFC3339 is the single point where wall-clock time enters the command layer. Keeping time
// out of the internal/* packages keeps them deterministically testable with injected timestamps.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// mnemoConfigDir is Mnemo's config directory: $XDG_CONFIG_HOME/mnemo, else ~/.config/mnemo. We do
// NOT use os.UserConfigDir() because on macOS it returns ~/Library/Application Support, whereas
// DESIGN §6.1 (and cross-platform muscle memory) specifies ~/.config/mnemo. Using the XDG path
// explicitly keeps config.json and the local override store in one predictable place on every OS.
func mnemoConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "mnemo")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "mnemo") // last-resort relative path
	}
	return filepath.Join(home, ".config", "mnemo")
}

// localManifestPath is this host's local override/bookkeeping store, written by `mnemo map`
// (offline, no restic) and overlaid onto the repo manifest at pull/projects time so an override
// applies immediately. The directory is created if absent.
func localManifestPath() (string, error) {
	dir := mnemoConfigDir()
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

// loadRepoManifest restores just projects.json from the latest snapshot so push can accumulate
// machine bookkeeping across pushes. Best-effort: returns an error (caller starts fresh) when
// there is no snapshot yet. It uses --include to restore only the manifest file rather than the
// whole staging tree — keeps this fast even for large repos.
//
// Restic behavior (verified empirically): `restic restore snapshotID:/abs/stage --include
// projects.json --target tmp` strips the /abs/stage prefix and places projects.json directly
// under tmp, yielding tmp/projects.json. No nested absolute-path hierarchy under tmp.
func loadRepoManifest(ctx context.Context, repo restic.Repo) (*manifest.Manifest, error) {
	paths, err := repo.SnapshotPaths(ctx, "latest")
	if err != nil {
		// No snapshot yet (first push) — start fresh.
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "mnemo-manifest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := repo.RestoreSubpathInclude(ctx, "latest", paths[0], "projects.json", tmp); err != nil {
		return nil, err
	}
	// restic strips the snapshotSubpath prefix, so projects.json lands flat at tmp/projects.json.
	return manifest.Load(filepath.Join(tmp, "projects.json"))
}
