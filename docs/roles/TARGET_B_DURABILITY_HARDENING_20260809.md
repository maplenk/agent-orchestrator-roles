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

**Base:** `0d1430a5431582f25580c2818c5658ef49f2d008`  
**Implementation:** `586d1156` (`fix: disambiguate mixed fork migration history`)

### Accepted contract

The pre-goose fork repair now snapshots, inside one transaction and before any
write, the latest applied truth for original 42–49, abandoned 53–60, and fork
9000–9007; all eight physical fork effects; and the physical Muse CHECK.

- Complete original 42–49 + all fork effects + lone 53 + Muse CHECK maps the
  fork identities to 9000–9007 while retaining the exact original 53 ledger
  row (`id`, timestamp, applied state, and count).
- Complete original 42–49 + complete stale 53–60 still frees all 53–60 rows,
  even if Muse text is physically present, and records every 900x exactly once.
- A corresponding 900x present on function entry protects the old-number row
  as upstream-owned; already-repaired histories remain idempotent.
- Partial old blocks, unsafe partial new masks, lone 53 without a Muse effect,
  and complete-original histories whose incomplete fork schema could mutate an
  unprotected old row fail with a wrapped ambiguity sentinel before writes.
- A burned upstream 42–53 ledger with no corresponding fork effects retains
  the existing safe pairwise/no-delete path so later schema reconciliation can
  repair the skipped upstream effect.
- Any SQL failure after an insert begins rolls the whole repair transaction
  back to exact ordered ledger equality.

No existing SQL migration was modified. No 9009 migration was necessary: this
is pre-goose interpretation of prior ledger identities, and the 9000-series
rows are already the durable repaired marker. Migration 9008 remains current.
No capability cell changed.

### Load-bearing regression coverage

- The direct full-original/lone-Muse fixture preserves the same 53 row through
  three repair passes and records 9000–9007 exactly once.
- The end-to-end historical database test captures the genuine Muse row before
  `migrate`, proves the same row survives, confirms the current ledger through
  9008, inserts a Muse session, and passes `PRAGMA integrity_check`.
- Complete original + complete stale 53–60 cleanup is tested with Muse text and
  then compared row-for-row across a second repair pass.
- Partial 53–54, a lone 53 without Muse, a 7/8 physical-effect population, and
  a full old block with only 9000 pre-recorded all refuse with an exact
  before/after ledger comparison and no leaked 900x rows.
- A `BEFORE DELETE` trigger forces a failure after 9000 insertion; the test
  proves the insert is rolled back and the exact ordered ledger is restored.
- Old-only partial history, clean upstream Muse, rolled-back latest rows, fresh
  databases, already-repaired plus later Muse, and burned upstream histories
  remain controls.

Independent review first found that incomplete fork effects could bypass the
block classifier, plus missing partial-new-mask and mid-transaction rollback
tests. All were fixed. The full package then exposed the pre-existing burned
upstream 42–53 population; the refusal was narrowed to an unprotected old row
whose corresponding fork effect is physically present, which blocks deletion
without rejecting safe no-effect reconciliation. A final independent audit
approved that boundary with no P1/P2 findings.

### Verification

| Gate | Exact result |
|------|--------------|
| Focused SQLite normal | `go test -count=1 ./internal/storage/sqlite`: **49 passed** |
| Focused SQLite race | `go test -race -count=1 ./internal/storage/sqlite`: **49 passed** |
| Build / vet / format | `go build ./...`, `go vet ./...`, `gofmt -l .`, and `git diff --check`: **pass / clean** |
| Pinned lint | `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...`: **0 issues** |
| Frontend/API | Not applicable: no controller, DTO, OpenAPI, generated client, or frontend file changed |

The first widened full SQLite package run after the review fix is retained: 47
tests passed and the two established burned-Muse reconciliation tests failed
because the refusal was initially too broad. After narrowing it to histories
that could actually delete an old row, the exact package passed 49/49. This was
a code correction, not a retry to hide the earlier result.

The single exact-head repository-wide backend run is also retained: **4717
passed, 3 failed, 51 skipped in 132 packages**. Its failures are only the known
wall-clock trio, with fake measured at 5.496805958s and the Kilocode/OpenCode
tests reaching their existing three-second command deadlines. That run was not
repeated.

### Isolated destructive dogfood

Dogfood used only
`AO_DATA_DIR=/tmp/ao-hardening-b-dogfood.quYHVv`, an isolated run file, and
port 39431. A real daemon first created a current database. After shutdown,
its 9000–9007 rows were rewritten to 42–49 while genuine Muse 53 remained.
The daemon then restarted successfully on that exact mixed population.

Live inspection proved the Muse row stayed `id=47`, applied, with timestamp
`2026-08-09 06:08:36`; 9000–9008 were each present exactly once; 54–60 were
absent; and `PRAGMA integrity_check` returned `ok`. A real Muse session insert
succeeded and `/api/v1/sessions` returned it. The isolated daemon stopped
cleanly; no installed-app or real `~/.ao/data` state was touched.
