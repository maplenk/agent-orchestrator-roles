# Target B durability hardening — 2026-08-09

This record starts after the accepted roles MVP. It does not amend or replace
any accepted switch, pause, Continue, starter-role, runtime-probe, or live-test
record.

## Hardening A — per-project stored-config containment

**Base:** `a657ca635cdfe0ef9b0a40dfaf5fa8d8dd6efb65`  
**Implementation:** `8bb1e3af` (`fix: contain unreadable project configs`)

### Accepted contract

- SQLite `NULL` remains the only unset config representation.
- Empty text, JSON `null`, malformed JSON, unknown fields, trailing JSON, and a
  nonzero unsupported role-map schema version make only that project entry
  unreadable.
- List and get surfaces retain registry metadata and expose the row as
  degraded without exposing decoder details or raw config.
- Raw stored bytes are never rewritten by containment.
- Upsert, settings/config update, workspace write/import, archive/remove, and
  dev import refuse an existing unreadable target. Dev import preflights the
  complete source and target set before its first write.
- Boot starter-role seeding and tracker intake skip unreadable config; they do
  not consume its typed zero value.

No SQL migration was required or edited. No capability flag changed;
`limit_detection_supported=false` and Claude `read_only_enforced=false` remain
unchanged.

### Load-bearing regression coverage

- Storage decoding rejects empty text, JSON null, unknown top-level fields,
  malformed JSON, and forward role-map schema versions.
- List returns a healthy and unreadable row together; strict get classifies the
  unreadable row; display get preserves its registry metadata.
- A table mutation test proves upsert, workspace upsert/import, settings
  update, and archive all fail with the typed unreadable-config sentinel and
  leave exact config/path/name/archive state unchanged.
- Service and real-controller tests prove degraded list/get, stable 409
  `PROJECT_CONFIG_UNREADABLE` responses, healthy-project role-map seeding, and
  exact raw-byte preservation.
- A sole unreadable active project suppresses Scratch boot seeding.
- Dev import rejects an unreadable archived target before importing an earlier
  healthy source row.
- Tracker intake skips an enabled-looking unreadable row while continuing
  healthy rows.
- Renderer tests prove the degraded label is visible and spawn, New session,
  and Remove are disabled while Settings remains available.

Independent review found three gaps before acceptance: empty non-NULL text was
still treated as zero config, dev import could discover an unreadable archived
target after an earlier write, and a sole unreadable active project lacked an
explicit Scratch-seeding regression. All three were fixed and covered before
the implementation commit.

### Verification

| Gate | Exact result |
|------|--------------|
| Focused Go | `go test -count=1` across storage/project/devimport/tracker/controllers/apispec: **585 passed in 7 packages** |
| Focused Go race | Same package set with `-race -count=1`: **585 passed in 7 packages** |
| Build / vet / format | `go build ./...`, `go vet ./...`, `gofmt -l .`, and `git diff --check`: **pass / clean** |
| Pinned lint | `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...`: **0 issues** |
| Frontend typecheck | `npm run frontend:typecheck`: **pass** |
| Full Vitest, clean locked tree plus externally supplied undeclared landing-script modules | **151/151 files, 2043/2043 tests pass** |
| API generation | `npm run api`: **pass**, deterministic SHA-256 `0170d921…eaeba` (OpenAPI) and `a4b3a608…de0a9` (schema) |

The first clean-lock full Vitest invocation was not overwritten: 2038 tests
passed and five landing-script tests failed before execution because the
pre-existing script imports undeclared `cheerio` and `node-html-markdown`
packages. A direct no-save install perturbed locked versions and produced an
unrelated NotificationCenter timing failure (2042 passed, one failed), so that
run was classified invalid rather than treated as green. After restoring exact
`npm ci`, the two missing script packages were supplied from an external temp
dependency directory without changing the lock tree; the authoritative run
then passed 2043/2043. The missing declarations remain test-infrastructure
hygiene work and are not hidden by this record.

The ordinary repository-wide backend run is also recorded without retrying it
green: **4711 passed, 3 failed, 51 skipped in 132 packages**. The failures are
exactly the already-documented wall-clock trio:

- fake `TestFullLifecycleSpawnToTermination` (5.391595834s)
- Kilocode `TestAuthStatusUnknownWhenKeyOnlyComesFromInteractiveShell`
  (`context deadline exceeded`)
- OpenCode `TestOpenCodeAuthStatusUnknownWithZeroCredentials`
  (`context deadline exceeded`)

### Isolated destructive dogfood and native UI

Dogfood used only
`AO_DATA_DIR=/tmp/ao-hardening-a-dogfood.xE3V25` with an isolated run file and
ports 39421/39422. A real daemon database was seeded with a healthy row and a
forward-versioned row carrying a future field. The live API returned both rows,
returned the bad row as degraded, rejected config/settings/delete with the
stable 409, and retained the exact SQLite config hex, display name, and active
state.

After the clean slice gates, the native Electron checkout launched against the
same isolated database. Accessibility inspection showed Broken and Healthy
together, the exact unreadable status, disabled Broken orchestrator spawn, and
an action menu with New session and Remove disabled while Project settings
remained available. Healthy and Scratch retained enabled spawn actions. Only
the checkout processes were stopped; the user's installed app and real
`~/.ao/data` daemon were not modified.

Two native-launch attempts are retained as environment evidence: the isolated
`npm ci` omitted Electron's postinstall binary metadata. The successful launch
used the already-installed identical Electron 33.4.11 distribution after the
version match was verified; no repository file changed to mask that setup
failure.

## Hardening B — mixed migration history

Pending. This section will be completed by the next independently committed
slice; it must retain a genuine lone Muse 53, preserve complete stale 53–60
cleanup and already-repaired idempotence, and refuse ambiguous histories before
any ledger mutation.
