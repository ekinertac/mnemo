// log.go implements `mnemo log`: list the snapshots in the repo (DESIGN §6 maps this onto
// `restic snapshots`). It is read-only and a convenient way to confirm a push landed. At M0
// it streams restic's native snapshot table; later milestones may add --json and richer
// columns (host, scope, size) once Mnemo tracks that metadata itself.
package command

import (
	"context"
	"flag"
	"fmt"

	"github.com/ekinertac/mnemo/internal/restic"
)

func runLog(args []string) error {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}

	repo, desc := resolveRepo(*repoFlag)
	fmt.Printf("mnemo: snapshots in %s\n", desc)
	return repo.Snapshots(ctx)
}
