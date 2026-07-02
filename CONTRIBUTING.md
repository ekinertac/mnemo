# Contributing to Mnemo

Thanks for your interest in improving Mnemo. This is a small, focused tool, and contributions
are welcome — bug reports, fixes, docs, and well-scoped features.

## Getting set up

You need [Go](https://go.dev) (see `go.mod` for the minimum version) and
[`restic`](https://restic.net) on your `PATH`.

```sh
git clone https://github.com/ekinertac/mnemo.git
cd mnemo
go build ./...
go test ./...          # fast, offline unit suite (no restic needed)
go test -tags e2e ./...  # end-to-end tests (needs the restic binary)
```

## Ground rules

Mnemo has a few deliberate conventions. Please keep to them:

- **Standard library only.** No third-party Go dependencies. If you think you need one, open an
  issue first — there's almost always a stdlib path.
- **Tests first.** New behavior and bug fixes come with tests. The pure logic (identity, filter,
  merge, config, retention policy) is unit-tested; integration lives behind the `e2e` build tag.
- **Every source file starts with a comment block** explaining what it's responsible for, where it
  fits, and any non-obvious constraints. Comments explain *why*, not *what*.
- **Sessions-only is a hard boundary.** Mnemo never syncs config/capabilities (MCP, skills, agents,
  plugins, settings). See `docs/DESIGN.md` §4.1 before touching the filter.
- **Additive by default.** Nothing but an explicit `prune` may ever delete remote data.
- **No interactive prompts.** Every command must run to completion or fail with a clear message —
  safe for cron, CI, and hooks.

## Submitting a change

1. Fork and branch from `master`.
2. Keep the change focused; one logical change per PR.
3. Run `gofmt -w .`, `go vet ./...`, and `go test ./...` — CI runs the same.
4. Write a commit message that explains *why* the change was made, not just what changed.
5. Open a PR describing the problem and the approach. Link any related issue.

## Architecture

`docs/DESIGN.md` is the source of truth for how Mnemo works and why each decision was made —
the identity model, the ephemeral filter, append-merge, and the restic boundary. Read it before
proposing anything structural.
