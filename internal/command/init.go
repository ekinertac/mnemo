// init.go implements `mnemo init`: create (or confirm) the restic repository Mnemo will
// push snapshots into. At M0 this is a near-passthrough to `restic init`; later milestones
// (DESIGN §6.1) will also materialize ~/.config/mnemo/config.toml and validate connectivity.
//
// Non-interactive by design (principle 8): init never prompts for a password. The caller
// must have supplied it via restic's env mechanisms; if it's missing, restic fails with a
// clear message and we surface it.
package command

import (
	"context"
	"flag"
	"fmt"

	"github.com/ekinertac/mnemo/internal/restic"
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}

	repo, desc := resolveRepo(*repoFlag)
	fmt.Printf("mnemo: initializing restic repo at %s\n", desc)
	if err := repo.Init(ctx); err != nil {
		return err
	}
	fmt.Println("mnemo: repo initialized. Keep your RESTIC_PASSWORD safe — without it the data is unrecoverable.")
	return nil
}
