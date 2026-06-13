// machines.go implements `mnemo machines`: list devices that have pushed, from the repo manifest
// (projects.json) restored from the latest snapshot. Read-only — no writes, no local state changes.
//
// The machine registry in the manifest (Manifest.Machines) is populated by `mnemo push` each time
// a host backs up; `mnemo machines` is the discovery view that lets you see which devices have
// contributed to the shared repo and when each last synced.
//
// The staging tree is restored via restoreStagingTree (root.go), which derives the subpath from
// the snapshot's own recorded path — not this machine's stageRootDir. That is what makes
// cross-machine listing work: a snapshot pushed from /Users/A/Library/Caches/... restores
// correctly on a machine whose cache lives at /home/B/.cache/....
//
// Related: internal/manifest (Machines field, Load), push.go (TouchMachine writes the registry),
// root.go (resolveRepo, restoreStagingTree), internal/restic (Available).
package command

import (
	"context"
	"flag"
	"fmt"
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
	repo, _ := resolveRepo(*repoFlag)
	tmp, cleanup, err := restoreStagingTree(ctx, repo, "latest")
	if err != nil {
		return err
	}
	defer cleanup()
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
