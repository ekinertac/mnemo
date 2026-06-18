// prune.go implements `mnemo prune` — the ONLY command that deletes data (DESIGN principle 2, §6,
// §9). It wraps `restic forget --prune` with a deliberately unforgiving safety model, because this
// is exactly the operation that cost claude-sync 440 transcripts (retention quietly removing data):
//
//   - No destructive default: with no --keep-* flags the policy is empty and prune REFUSES (errors).
//     You must state, explicitly, what to keep.
//   - Dry-run by default: it shows what WOULD be forgotten/pruned; only --apply actually deletes.
//   - Per-host grouping: retention is applied independently per machine (--group-by host), so
//     pruning one device's lineage can never remove another's snapshots.
//
// forgetArgs (the pure policy→args translation) is the TDD-anchored core; runPrune is the thin I/O
// shell around it. Related: internal/restic.Forget, docs/DESIGN.md §6.
package command

import (
	"context"
	"flag"
	"fmt"
	"strconv"

	"github.com/ekinertac/mnemo/internal/restic"
)

// retention is a restic keep-policy. A dimension counts as set only when >= 1: the flag default is
// -1, and a keep count of 0 ("keep none") is meaningless and dangerous, so mnemo treats 0 as unset
// rather than forwarding `--keep-last 0` to restic. This also makes a zero-value retention{} empty
// (refused), so the safety gate never depends on restic's version-specific handling of 0. within is
// empty when unset.
type retention struct {
	last, daily, weekly, monthly, yearly int
	within                               string
}

func (r retention) empty() bool {
	return r.last < 1 && r.daily < 1 && r.weekly < 1 && r.monthly < 1 && r.yearly < 1 && r.within == ""
}

// forgetArgs translates a retention policy into `restic forget` arguments. It returns an error for
// an empty policy (the safety gate) and appends --dry-run unless apply is true. Grouping is always
// per-host so machines' lineages are pruned independently.
func forgetArgs(r retention, apply bool) ([]string, error) {
	if r.empty() {
		return nil, fmt.Errorf("refusing to prune without an explicit retention policy — pass at least one of " +
			"--keep-last/--keep-daily/--keep-weekly/--keep-monthly/--keep-yearly/--keep-within")
	}
	args := []string{"forget", "--prune", "--group-by", "host"}
	add := func(flag string, n int) {
		if n >= 1 { // 0/negative = unset; never forward a "keep 0" dimension
			args = append(args, flag, strconv.Itoa(n))
		}
	}
	add("--keep-last", r.last)
	add("--keep-daily", r.daily)
	add("--keep-weekly", r.weekly)
	add("--keep-monthly", r.monthly)
	add("--keep-yearly", r.yearly)
	if r.within != "" {
		args = append(args, "--keep-within", r.within)
	}
	if !apply {
		args = append(args, "--dry-run")
	}
	return args, nil
}

func runPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	apply := fs.Bool("apply", false, "actually delete (default: dry-run — only show what would be removed)")
	r := retention{}
	fs.IntVar(&r.last, "keep-last", -1, "keep the last N snapshots per host")
	fs.IntVar(&r.daily, "keep-daily", -1, "keep N daily snapshots per host")
	fs.IntVar(&r.weekly, "keep-weekly", -1, "keep N weekly snapshots per host")
	fs.IntVar(&r.monthly, "keep-monthly", -1, "keep N monthly snapshots per host")
	fs.IntVar(&r.yearly, "keep-yearly", -1, "keep N yearly snapshots per host")
	fs.StringVar(&r.within, "keep-within", "", "keep all snapshots within a duration, e.g. 1y or 30d")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fargs, err := forgetArgs(r, *apply)
	if err != nil {
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
	if *apply {
		fmt.Printf("mnemo: pruning %s (APPLY — this deletes snapshots)\n", desc)
	} else {
		fmt.Printf("mnemo: prune dry-run on %s (nothing deleted; pass --apply to execute)\n", desc)
	}
	return repo.Forget(ctx, fargs)
}
