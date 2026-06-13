// map.go implements `mnemo map <identity> <local-path>`: record a per-host override so a project
// that lives at a non-default path on this machine still resumes correctly. Writes to the local
// override store (~/.config/mnemo/projects.json) offline — no restic, no prompts (principle 8).
// pull/projects overlay this store onto the repo manifest, so the override applies on next pull.
//
// Why a local store rather than writing back to restic: overrides are machine-specific and
// need to take effect on the NEXT pull without a push round-trip. Storing them locally (offline)
// means `mnemo map` is instant and never needs network access. Propagating them into the repo
// snapshot is a later concern (DESIGN §6 follow-on).
//
// Related: root.go (localManifestPath, overlayLocalOverrides), pull.go (consumes overrides),
// projects.go (displays resolved paths using the same overlay).
package command

import (
	"flag"
	"fmt"

	"github.com/ekinertac/mnemo/internal/manifest"
)

func runMap(args []string) error {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: mnemo map <identity> <local-path>")
	}
	id, path := fs.Arg(0), fs.Arg(1)
	host, err := hostID()
	if err != nil {
		return err
	}
	mpath, err := localManifestPath()
	if err != nil {
		return err
	}
	man, err := manifest.Load(mpath)
	if err != nil {
		return err
	}
	man.SetOverride(host, id, path)
	if err := man.Save(mpath); err != nil {
		return err
	}
	fmt.Printf("mnemo: mapped %s -> %s on %s\n", id, path, host)
	return nil
}
