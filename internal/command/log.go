// log.go implements `mnemo log`: list the snapshots in the repo (DESIGN §6 maps this onto
// `restic snapshots`). It is read-only and a convenient way to confirm a push landed. It streams
// restic's native snapshot table, then prints a clarifying footer: restic's per-snapshot "size"
// is the *logical* restore size, which users reasonably mistake for bytes uploaded/stored. Because
// restic is content-addressed, snapshots share unchanged content — each push transfers only what
// changed, and all snapshots together occupy far less than the sum of their sizes. The footer
// shows the real deduped footprint so the numbers can't mislead.
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

	repo, desc, err := resolveRepo(*repoFlag)
	if err != nil {
		return err
	}
	fmt.Printf("mnemo: snapshots in %s\n", desc)
	if err := repo.Snapshots(ctx); err != nil {
		return err
	}

	// Clarify the size column: it's logical, not transferred/stored. Show the real footprint when
	// available (best-effort — never fail `log` just because stats couldn't be read).
	fmt.Println()
	fmt.Println("Note: each snapshot's Size above is its logical restore size (what it reconstructs),")
	fmt.Println("not bytes uploaded or stored. Snapshots are content-addressed and share unchanged data,")
	fmt.Println("so every push transfers only what changed and all snapshots together cost far less.")
	if total, err := repo.RawDataSize(ctx); err == nil {
		fmt.Printf("Actual storage in B2 for ALL snapshots combined (deduped + compressed): %s\n", humanBytes(total))
	}
	return nil
}
