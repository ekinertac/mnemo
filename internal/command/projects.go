// projects.go implements `mnemo projects [--unmapped]`: list the project identities present in
// the latest snapshot and how each resolves to a local path on THIS machine, using the SAME
// resolver as restore (restore.ResolveLocal) so the view never lies about where a pull would
// land things. --unmapped shows only identities that don't resolve to an existing local project.
//
// Why use restore.ResolveLocal here instead of reimplementing: having one resolution path
// (not two) means the display is guaranteed to match what pull actually does. If the resolver
// changes, both commands change together.
//
// The host-local override store (~/.config/mnemo/projects.json) is overlaid before resolving,
// so overrides written by `mnemo map` are reflected immediately without a push round-trip.
//
// We use RestoreSubpath (restic's "snapshotID:subpath" syntax) so the staging tree lands directly
// at the temp dir rather than nested under the full absolute cache path hierarchy.
//
// Related: internal/restore (ResolveLocal), root.go (overlayLocalOverrides, stageRootDir),
// map.go (writes overrides), internal/identity (EncodedHome, Identity), internal/manifest (Load).
package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/restic"
	"github.com/ekinertac/mnemo/internal/restore"
)

func runProjects(args []string) error {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location")
	unmapped := fs.Bool("unmapped", false, "show only identities that don't resolve to an existing local project")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	host, err := hostID()
	if err != nil {
		return err
	}
	stageRoot, err := stageRootDir()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "mnemo-projects-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	repo, _ := resolveRepo(*repoFlag)
	if err := repo.RestoreSubpath(ctx, "latest", stageRoot, tmp); err != nil {
		return err
	}
	man, err := manifest.Load(filepath.Join(tmp, "projects.json"))
	if err != nil {
		return err
	}
	if err := overlayLocalOverrides(man, host); err != nil {
		return err
	}
	encHome := identity.EncodedHome(home)
	entries, err := os.ReadDir(filepath.Join(tmp, "by-id"))
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("mnemo: no project sessions in the latest snapshot")
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		enc, ok := restore.ResolveLocal(identity.Identity(id), host, encHome, man)
		exists := false
		if ok {
			if _, err := os.Stat(filepath.Join(home, ".claude", "projects", enc)); err == nil {
				exists = true
			}
		}
		if *unmapped && exists {
			continue
		}
		switch {
		case !ok:
			fmt.Printf("  %-34s unmapped (no local resolution; use: mnemo map %s <path>)\n", id, id)
		case exists:
			fmt.Printf("  %-34s -> %s (present)\n", id, enc)
		default:
			fmt.Printf("  %-34s -> %s (not present locally)\n", id, enc)
		}
	}
	return nil
}
