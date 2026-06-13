# M2 — Project Identity & Resume-Aware Restore (Design)

> Detailed design for milestone M2. Pairs with `docs/DESIGN.md` (§4.4, §5.2) which now records
> the architectural decision; this spec is the implementation-level design that the
> implementation plan will be built from. Date: 2026-06-13.

## 1. Goal

Make a session pushed from one machine resume on another, keyed on a **path-tokenized
identity** rather than the machine-specific encoded cwd. Concretely, after M2:

- `mnemo push` stages each session under an identity-keyed path (machine-independent for
  under-home projects), not under the raw `projects/<encoded-cwd>/` name.
- `mnemo pull` lays each session back down at the path *this* machine's `claude --resume`
  expects, de-tokenizing `~` to this machine's home (or applying a `mnemo map` override).
- `mnemo map` / `mnemo projects` let the user inspect and override the mapping; unmapped
  sessions are reported, never dropped.

Identity is a **pure, deterministic function of the path** — no git, no manifest, no guessing
(see `docs/DESIGN.md §4.4` for the rationale and the cross-platform matching contract).

## 2. The encoding problem (resolve first — step 0)

Claude Code stores sessions under `~/.claude/projects/<encoded-cwd>/`, where the encoding
replaces path separators with `-` (e.g. `/Users/ekinertac/Code/foo` →
`-Users-ekinertac-Code-foo`). Two facts make this the first thing to nail:

1. **The encoding looks lossy.** `-` is legal in path components, so `~/my-app` and `~/my/app`
   may encode to the same string. Decoding to a real path is therefore ambiguous in general.
2. **We don't have to decode.** Both backup and restore can operate entirely in Claude's
   *encoded* space: identity = the encoded cwd with the machine-specific encoded-home prefix
   replaced by a token. Restore swaps the token back for this machine's encoded-home prefix.
   This sidesteps the ambiguity completely — we transform encoded → identity → encoded and
   never round-trip through a filesystem path.

**Step 0 (investigation, before any identity code):** determine Claude Code's exact cwd
encoding on macOS *and* Windows from real data. The current `~/.claude/projects/` already
contains dir names produced by both machines (sessions were consolidated here). Inspect them to
answer:
- How is `/` encoded? (presumed `-`.) Is there any escaping that makes it reversible?
- How are Windows `\`, the drive `C:`, and the leading separator encoded? (We have
  Windows-origin dirs to read, e.g. ones containing `AppData-Roaming`.)
- Does the encoded-home prefix of a Windows path share a tail with the Mac equivalent for the
  "same" relative project? (This is what makes cross-platform matching work.)

The finding decides one representation choice (§3). If the encoding turns out reversible, we
*may* use a human-readable identity (`path:~/Code/foo`); if lossy, identity stays in
encoded-tail form. **The encoded-space round-trip works either way** and is the safe default.

## 3. Identity resolver — `internal/identity`

Single responsibility: encoded-cwd ⇄ identity ⇄ encoded-cwd, plus the under-home/outside-home
distinction. Pure functions, fully unit-testable (TDD anchor of M2).

Proposed API (representation finalized after step 0):

```go
// Identity is a machine-independent project key, e.g. "home:-Code-foo" (encoded tail) or
// "abs:-opt-services-bar". Stored verbatim in projects.json and accepted by `mnemo map`.
type Identity string

// FromEncoded computes the identity for a Claude projects/<encoded> dir name, given this
// machine's encoded-home prefix. Applies NFC normalization; comparison is case-insensitive.
func FromEncoded(encodedCwd, encodedHome string) Identity

// ToEncoded turns an identity back into THIS machine's encoded-cwd dir name (the inverse of
// FromEncoded for the home case; identity-as-absolute for the abs case).
func ToEncoded(id Identity, encodedHome string) (string, bool)   // bool=false → no valid local mapping

// EncodedHome returns this machine's encoded-home prefix, computed by encoding $HOME the same
// way Claude encodes a cwd. Canonicalizes $HOME first (symlinks, macOS /private, trailing /).
func EncodedHome(home string) string
```

**Representation note (reconciling with `docs/DESIGN.md §4.4`):** the design doc writes
identities in the readable `path:~/Code/foo` form. That is the *conceptual/display* target. The
*stored* form is decided by step 0: if Claude's encoding is reversible we adopt `path:~/…`
verbatim; if it is lossy we store the encoded tail (`home:-Code-foo`) and only render the
readable form best-effort. Either way `mnemo map` accepts the canonical stored form, and
whichever is chosen, DESIGN.md §4.4's examples get aligned to match once step 0 settles it.
This spec uses the encoded-tail form in examples because it is the safe default.

Normalization (correctness, per `docs/DESIGN.md §4.4`):
- canonicalize `$HOME` before deriving the encoded-home prefix;
- Unicode **NFC** on the encoded string (macOS stores filenames NFD);
- case preserved in the stored key, **case-insensitive** comparison when matching;
- a path is "under home" iff its encoded form has the encoded-home prefix followed by a
  separator boundary (avoid `-Users-ekin` matching `-Users-ekinside`).

## 4. `projects.json` — `internal/manifest`

Slimmed per `docs/DESIGN.md §4.4`: identity ⇄ path is a function for the common case, so the
manifest only carries overrides + machine bookkeeping.

```json
{
  "version": 1,
  "machines":  { "darwin-mbp": { "lastSeen": "2026-06-13T21:00:00Z" } },
  "overrides": { "darwin-mbp": { "home:-Code-foo": "/Users/ekinertac/work/foo" } }
}
```

- Lives in the staging tree root → backed up in the repo, deduped, versioned like everything
  else. On push it is read from the latest snapshot (if any), merged with this run, rewritten.
- `internal/manifest`: typed load/save, `SetOverride(host, id, path)`, `Override(host, id)`,
  `TouchMachine(host, ts)`. The timestamp is passed in (no `time.Now()` deep in the logic →
  testable).
- Concurrency across machines is additive: each push only mutates its own host's sub-objects;
  restic dedup makes rewriting the small JSON free. True conflicts (two hosts editing the same
  override) are surfaced by `doctor`, not auto-resolved.

## 5. Identity-keyed staging (push) — extends `internal/stage`

Today stage mirrors `~/.claude` layout. M2 changes only the `projects/` mapping:

- For `projects/<encoded-cwd>/<rest>` durable files, stage them under
  `by-id/<safe(identity)>/<rest>` where `safe()` is a filesystem-safe, reversible encoding of
  the identity string (identity already avoids most special chars; define the escaping in the
  plan).
- `transcripts/`, `plans/`, `tasks/`, `history.jsonl` are **not** project-scoped → stage
  unchanged (they already round-trip fine).
- The classifier (`internal/filter`) is unchanged — filtering happens first, identity-keying
  is a path rewrite applied to the durable `projects/` subset.

This keeps the snapshot machine-independent: `by-id/home:-Code-foo/…` is the same on every
machine, instead of `…/stage/projects/-Users-ekinertac-Code-foo/…`.

## 6. Resume-aware restore (pull) — `internal/restore`

`mnemo pull` restores the snapshot to a temp dir, then lays down into `~/.claude`:

```
for each by-id/<identity>/<rest> in the restored tree:
    localEncoded, ok = resolveLocal(identity, host, manifest)
    if !ok:                       # e.g. a Windows abs identity on a Mac
        move to holding area; record for `projects --unmapped`; continue
    dst = ~/.claude/projects/<localEncoded>/<rest>
    lay down dst honoring --on-conflict (default keep-newer; append-merge for .jsonl is M3)

resolveLocal(identity, host, manifest):
    1. manifest override for (host, identity)      → encode(thatPath)
    2. identity is home:…                          → encodedHome + tail   (always ok)
    3. identity is abs:… and valid on this OS      → the absolute encoded form
    4. else                                        → (_, false)   # unmapped → holding area
```

- Non-project data (`transcripts/`, `plans/`, `tasks/`, `history.jsonl`) lays straight back.
- **Never clobber, never drop:** under-home always resolves (harmless even if the local project
  dir doesn't exist yet); only genuinely unmappable identities hold out, loudly reported.
- M2 keeps `--on-conflict=keep-newer` at file granularity; the append-merge for `.jsonl`
  (`history.jsonl` + same-session) is M3 and slots in at the "lay down" step.

## 7. CLI surface (M2 slice)

- `mnemo map <identity> <local-path>` — write an override for this host (scriptable, no prompt).
- `mnemo projects [--unmapped]` — list identities seen in the repo and how they resolve here;
  `--unmapped` shows the holding-area entries needing a `map`.
- `mnemo machines` — list hosts + lastSeen from the manifest.
- `mnemo pull` gains the lay-down behavior above; `--target` still supported for safe
  dry-restores (M0/M1 behavior) so nothing forces a write into live `~/.claude`.

## 8. Edge cases & limitations (intended behavior, not bugs)

- Windows abs / `C:\…` identity seen on a Mac → unmapped → holding + `map`. (Expected; see
  matching table in `docs/DESIGN.md §4.4`.)
- Same relative path, different home location across machines → matches (the `~` token's job).
- Case differences (`~/Code/Foo` vs `~/code/foo`) unify via case-insensitive compare; a
  deliberate choice that's correct on macOS/Windows and pragmatic on Linux.
- Accented folder names → NFC normalization makes Mac (NFD) and others agree.
- Lossy decode is avoided by staying in encoded space (§2).

## 9. Test plan (TDD targets)

1. `identity`: FromEncoded/ToEncoded round-trip for under-home and absolute; cross-platform
   match (Mac vs Windows encoded inputs → same identity); NFC + case; the prefix-boundary
   guard (`-Users-ekin` ≠ `-Users-ekinside`). Driven by real encoded names gathered in step 0.
2. `manifest`: load/save round-trip, override get/set, machine touch, version handling,
   missing-file = empty manifest.
3. `stage`: project files land under `by-id/<identity>/…`; non-project data unchanged;
   filtering still correct (compose with existing M1 tests).
4. `restore`: resolveLocal precedence (override > home > abs > unmapped); holding-area routing;
   keep-newer conflict default; never-drop invariant.
5. End-to-end: push on a simulated "machine A" home, restore against a different simulated home,
   assert the transcript lands at the re-homed encoded path. (Simulate Windows by feeding a
   Windows-style encoded-home + dir names — no real Windows box needed for the unit/e2e layer;
   a real Mac⇄Windows pass is a manual verification before retiring claude-sync.)

## 10. Build order within M2

0. **Encoding investigation** (§2) — decide identity representation from real data.
1. `internal/identity` (TDD) — the resolver.
2. `internal/manifest` (TDD) — projects.json.
3. Identity-keyed `stage` (TDD) — push side.
4. `internal/restore` + `mnemo pull` lay-down (TDD) — restore side.
5. `mnemo map` / `projects` / `machines` commands.
6. End-to-end simulated cross-home test; then a manual Mac⇄Windows resume check.

Ship M2 before retiring claude-sync (HANDOFF). M3 (append-merge) layers onto the restore
lay-down step without reworking M2.
