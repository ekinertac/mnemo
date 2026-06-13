// machines.go implements `mnemo machines`: list devices that have pushed, from the repo manifest
// (projects.json) restored from the latest snapshot. Read-only — no writes, no local state changes.
//
// The machine registry in the manifest (Manifest.Machines) is populated by `mnemo push` each time
// a host backs up; `mnemo machines` is the discovery view that lets you see which devices have
// contributed to the shared repo and when each last synced.
//
// We use RestoreSubpath (restic's "snapshotID:subpath" syntax) so the staging tree lands directly
// at the temp dir rather than nested under the full absolute cache path hierarchy.
//
// Related: internal/manifest (Machines field, Load), push.go (TouchMachine writes the registry),
// root.go (resolveRepo, stageRootDir), internal/restic (Available, RestoreSubpath).
package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/restic"
)

func runMachines(args []string) error {
	fs := flag.NewFlagSet("machines", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}
	stageRoot, err := stageRootDir()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "mnemo-machines-")
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
	if len(man.Machines) == 0 {
		fmt.Println("mnemo: no machines recorded yet")
		return nil
	}
	for host, mc := range man.Machines {
		fmt.Printf("  %-20s last seen %s\n", host, mc.LastSeen)
	}
	return nil
}
