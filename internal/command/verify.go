// verify.go implements `mnemo verify` — repository integrity checking (DESIGN §6, §9). It is a
// thin, read-only wrapper over `restic check`: restic owns integrity (Merkle-tree structure,
// pack/index consistency), so Mnemo just exposes it. --read-data escalates to re-reading every
// pack (slow, bandwidth-heavy on remotes), for when structural checks aren't enough.
package command

import (
	"context"
	"flag"
	"fmt"

	"github.com/ekinertac/mnemo/internal/restic"
)

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	readData := fs.Bool("read-data", false, "also re-read and re-hash every pack (thorough but slow)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}
	repo, desc := resolveRepo(*repoFlag)
	fmt.Printf("mnemo: verifying %s%s\n", desc, map[bool]string{true: " (--read-data, full)", false: ""}[*readData])
	return repo.Check(ctx, *readData)
}
