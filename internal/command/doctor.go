// doctor.go implements `mnemo doctor` — a read-only health report (DESIGN §6, §9). It answers
// "is my Mnemo setup actually working?" in one place: is restic installed, is a repo configured
// and reachable, how many snapshots/machines exist, and is the most recent push stale. It mutates
// nothing and exits non-zero if any check fails, so it's usable as a cron/CI canary.
//
// The check aggregation (hasFailure) and the staleness helper (daysSince) are pure and tested; the
// data-gathering is thin restic/manifest I/O. Related: internal/restic.SnapshotCount, root.go's
// loadRepoManifest, docs/DESIGN.md §6.
package command

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/ekinertac/mnemo/internal/restic"
)

type checkStatus int

const (
	statusOK checkStatus = iota
	statusWarn
	statusFail
)

func (s checkStatus) label() string {
	switch s {
	case statusOK:
		return "OK  "
	case statusWarn:
		return "WARN"
	default:
		return "FAIL"
	}
}

type check struct {
	name   string
	status checkStatus
	detail string
}

type doctorReport struct {
	checks []check
}

func (rep *doctorReport) add(name string, status checkStatus, detail string) {
	rep.checks = append(rep.checks, check{name, status, detail})
}

// hasFailure reports whether any check failed — the basis for doctor's non-zero exit.
func (rep *doctorReport) hasFailure() bool {
	for _, c := range rep.checks {
		if c.status == statusFail {
			return true
		}
	}
	return false
}

// staleAfterDays is how old the most recent push may be before doctor warns.
const staleAfterDays = 30

// daysSince parses an RFC3339 timestamp and returns whole days between it and now. ok=false when
// the timestamp can't be parsed (so callers can skip the staleness check rather than mis-warn).
func daysSince(rfc3339 string, now time.Time) (int, bool) {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return 0, false
	}
	return int(now.Sub(t).Hours() / 24), true
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location (overrides $MNEMO_REPO / $RESTIC_REPOSITORY)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	rep := &doctorReport{}

	// 1. restic binary.
	if err := restic.Available(ctx); err != nil {
		rep.add("restic", statusFail, err.Error())
		printDoctor(rep)
		return fmt.Errorf("doctor found problems")
	}
	rep.add("restic", statusOK, "binary found")

	// 2. repo configured.
	repo, desc := resolveRepo(*repoFlag)
	if repo.Repository == "" && strings.HasPrefix(desc, "(") { // "(unset — ...)"
		rep.add("repo config", statusFail, desc)
		printDoctor(rep)
		return fmt.Errorf("doctor found problems")
	}
	rep.add("repo config", statusOK, desc)

	// 3. repo reachable + snapshot count.
	n, err := repo.SnapshotCount(ctx)
	if err != nil {
		rep.add("repo reachable", statusFail, err.Error())
		printDoctor(rep)
		return fmt.Errorf("doctor found problems")
	}
	rep.add("repo reachable", statusOK, fmt.Sprintf("%d snapshot(s)", n))

	// 4. machines + last-push staleness (best-effort — empty repo is fine).
	if man, err := loadRepoManifest(ctx, repo); err == nil {
		newest := ""
		for _, mc := range man.Machines {
			if mc.LastSeen > newest {
				newest = mc.LastSeen
			}
		}
		rep.add("machines", statusOK, fmt.Sprintf("%d recorded", len(man.Machines)))
		if d, ok := daysSince(newest, time.Now()); ok {
			switch {
			case d < 0:
				rep.add("last push", statusWarn, "timestamp is in the future (clock skew?)")
			case d > staleAfterDays:
				rep.add("last push", statusWarn, fmt.Sprintf("%d days ago (> %d)", d, staleAfterDays))
			default:
				rep.add("last push", statusOK, fmt.Sprintf("%d days ago", d))
			}
		}
	} else {
		rep.add("machines", statusWarn, "no manifest yet (no successful push?)")
	}

	printDoctor(rep)
	if rep.hasFailure() {
		return fmt.Errorf("doctor found problems")
	}
	return nil
}

func printDoctor(rep *doctorReport) {
	for _, c := range rep.checks {
		fmt.Printf("  [%s] %-16s %s\n", c.status.label(), c.name, c.detail)
	}
}
