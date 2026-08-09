# Target B desktop role-map editor — 2026-08-09

This record starts after the accepted roles MVP. It does not amend or replace
the accepted switch, pause, manual Continue, runtime-probe, starter-role, or
live-acceptance records.

**Atomic role-map API:** `54cdbdd8` (`feat: add atomic project role-map updates`)  
**Desktop editor:** `c7c1f565` (`feat: add desktop role-map editor`)

## Scope and result

The desktop can now inspect and author the project-owned role map without
turning a role edit into a stale whole-config replacement. Healthy project
reads carry a required role-map content revision. The editor saves through a
role-only compare-and-swap endpoint, while ordinary desktop settings saves
supply the inverse optional guard so they cannot restore an older role map.

This slice adds no migration. Migration 9008 remains current and 9009 remains
the next fork migration number. It changes no capability cell:
`limit_detection_supported` remains `false` for every harness, and Claude
`read_only_enforced` remains `false`. No accepted automatic-failover,
switch, pause, or starter-role behavior is promoted or redesigned here.

## Contract and implementation

### Revision and mutation boundary

- Every healthy project response includes a required `roleMapSha256`. A
  degraded/unreadable project remains a distinct response rather than being
  represented by a typed zero config.
- `PUT /api/v1/projects/{id}/role-map` requires
  `expectedRoleMapSha256` plus the replacement role map. Storage performs the
  latest-row read, revision comparison, role-map merge, and config write in one
  transaction.
- A role-only winner preserves unrelated config that another writer committed
  after the editor loaded. Reusing a stale role-map revision returns HTTP 409
  `PROJECT_ROLE_MAP_CONFLICT` and leaves the winner unchanged.
- The ordinary full-settings request accepts an optional
  `expectedRoleMapSha256`. The desktop always sends it. This is the inverse
  fence: a stale full-config writer returns the same stable 409 instead of
  silently restoring an older role map. Callers that omit the field retain the
  existing compatible full-replacement API contract.
- The role-only response returns the saved project and new revision. The
  renderer adopts that response as its next base, retains a visible saved
  acknowledgement, and uses an explicit reload to adopt a newer conflicting
  revision.

### Editor behavior

The healthy-project settings surface edits:

- strict delegation and the selected orchestrator role;
- role ID, template profile, harness, model, and selection hints;
- explicit `workspaceWrites` and `canSpawn` booleans, including authored
  `false` values; and
- ordered per-role failover ladders.

The editor preserves an existing explicit automatic failover mode without
offering to enable automatic behavior in this slice. It preserves ladder order
and exact optional model values. Discard restores the accepted base without a
mutation; reload replaces both the draft and compare-and-swap revision; save
affects only the role map.

Failover validation treats an effective target as harness plus trimmed model.
The current target and every earlier rung are excluded, while the same harness
with a genuinely different model remains valid. This makes the documented
"alternatives after current" rule host-enforced rather than a model
instruction. A strict orchestrator-only map remains valid when its
orchestrator has spawn permission; the desktop does not invent a stronger
worker-role requirement than the daemon.

Rungs have editor-local stable identities. Reordering transfers keyboard focus
to an enabled action on the same moved rung, including first/last-boundary
moves. Save, discard, and reload advance an editor epoch so a replaced ladder
cannot reuse stale DOM identity. Role fieldsets, action names, validation
alerts, visible disabled-action explanations, and described relationships are
accessible. All editor and validation copy is present in the eight renderer
locale catalogs with matching interpolation placeholders.

### Unreadable-project containment

The earlier per-project containment contract remains intact. A malformed or
forward-versioned stored config is still listable as degraded and identity
only. Role-map save, guarded full-settings save, and the other fenced actions
return the distinct HTTP 409 `PROJECT_CONFIG_UNREADABLE`; they do not reinterpret
the row as an empty config. Exact raw SQLite config bytes are preserved.

The OpenAPI document, generated frontend schema, sqlc query, and generated
sqlc code were regenerated from their sources. Clean regeneration produced no
uncommitted drift.

## Load-bearing regression coverage

### Backend

- Domain tests reject rung 1 when it repeats the current target and reject a
  later rung when it repeats any earlier effective target, including
  whitespace-equivalent models. Positive controls retain strict
  orchestrator-only maps and same-harness/different-model alternatives.
- Store tests prove role-only CAS preserves the latest unrelated config,
  rejects a stale writer without mutation, and allows exactly one of two
  concurrent writers to win.
- The inverse settings test commits a role update first, then proves a stale
  full-settings request returns 409 and cannot restore the old role map.
- Controller tests pin the required healthy revision, exact role-only wire
  shape, invalid-revision refusal, conflict details, new winner revision, and
  compatibility of an ordinary full-settings caller that omits the optional
  guard.
- Unreadable-project controller and storage coverage includes the new endpoint
  and guarded settings path, retaining the stable unreadable error and exact
  raw-byte preservation.
- CLI/HTTP DTO drift coverage and generated-schema checks pin the added healthy
  revision and request fields.

### Frontend

- An exact role-save mutation asserts the CAS revision, explicit false
  permissions, role add/remove changes, and reordered ladder while proving no
  orchestrator restart is requested.
- Automatic mode is preserved through reorder and save. The boundary reorder
  test proves focus follows the same rung to its enabled opposite action.
- Duplicate effective targets are rejected before mutation; positive coverage
  accepts strict orchestrator-only and same-harness/different-model maps.
- A single-harness catalog leaves Add rung disabled, shows a visible localized
  explanation, exposes the description relationship, and authors no hidden
  rung.
- Capability refusal keeps the draft, displays the daemon's exact reason, and
  records no successful mutation.
- Conflict coverage keeps revision A's draft, explicitly reloads revision B,
  and proves the next save uses B. Discard coverage changes ladder topology
  and proves restored rows receive fresh DOM identity rather than reusing a
  removed rung.
- Degraded-project coverage exposes identity and refresh only; save, strict
  controls, and other mutations are absent.
- Locale completeness, placeholder parity, and renderer localization coverage
  include every new label, description, and validation message.

## Independent review history

The work was reviewed in small, read-only passes while still uncommitted.
Findings were fixed before the final gates rather than being reclassified:

1. The first editor review found mutable rung keys, incomplete group/action
   names, hard-coded validation copy, and errors that were not announced. The
   keys, fieldsets, action labels, locale catalogs, and alert semantics were
   corrected.
2. The API/editor review found the reverse stale-writer path: a newer role-only
   save could still be erased by an older full-settings draft. The optional
   full-settings CAS guard and a load-bearing inverse-order mutation test were
   added. The same pass found a no-op failover rung and an editor-only strict
   worker-role rule; host validation and positive round-trip controls replaced
   them.
3. Follow-up review found focus loss when a moved action became disabled,
   editor keys that were not rebased after replacing the draft, and a disabled
   Add rung explanation available only through an ineffective title. Focus now
   moves to an enabled same-rung action, editor epochs reset replaced ladders,
   and the explanation is visible and described.
4. Final review narrowed focus lookup to the editor root, required a
   topology-changing DOM-identity regression, and clarified that duplicate
   copy means any earlier rung. After those fixes the independent reviewer
   approved the editor with no remaining P1, P2, or P3 finding.

The final independent reviewer runs passed **47/47** and **36/36** on their
respective focused selections.

## Verification

All full gates ran after the review corrections.

| Gate | Exact result |
|------|--------------|
| Focused backend normal | Independent focused `go test -count=1` selection: **792 passed** |
| Focused backend race | `go test -race -count=1` across domain, project service, SQLite project store, and project controllers: **714 passed in 4 packages** |
| Focused frontend final | Focused Vitest selection: **76/76 passed** |
| Backend build | `cd backend && go build ./...`: **pass** |
| Full backend test | `cd backend && go test ./...`: **pass** |
| Full backend race | `cd backend && go test -race ./...`: **pass** |
| Backend vet | `cd backend && go vet ./...`: **pass** |
| Formatting / diff | `gofmt` applied to every touched Go file; `git diff --check`: **clean** |
| Pinned lint | `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...`: **pass, zero issues** |
| Frontend typecheck | `cd frontend && npm run typecheck`: **pass** |
| Full frontend Vitest | Full renderer Vitest suite: **pass** |
| Frontend build | `cd frontend && npm run build`: **pass** |
| Generated contracts | `npm run api` and `npm run sqlc`, followed by drift check: **pass / clean** |

### Honest fixed failures

Three pre-gate failures are retained as part of the development record:

- the first focused frontend run had two locale-copy expectation failures;
- after epoch remounting was introduced, a later run had two stale test-element
  references because the tests retained DOM nodes that the new contract
  deliberately replaced; and
- one focused backend build failed because a new test was missing its
  `strings` import.

The copy expectations were aligned with the reviewed catalogs, the remount
tests were changed to re-query the current DOM while retaining identity
assertions, and the missing import was added. All were fixed before the full
gates above. None was overwritten or presented as an accepted green run.

## Native Forge Electron acceptance

Native acceptance ran only after the clean gates, using the real Forge
Electron application on `127.0.0.1:3102` and an isolated temporary AO data
directory under `/tmp`. The user's real `~/.ao` was not read or modified.

The accepted walkthrough proved:

- the desktop launched against the isolated daemon and opened the Roles
  settings surface;
- an unsaved edit was discarded exactly, with no mutation;
- a save survived close/reopen;
- a two-rung ladder reordered with stable focus, and the exact new order
  persisted after reopen;
- an unsupported capability combination displayed the daemon's exact refusal
  and did not mutate the project;
- an inverse stale full-settings request returned 409 and left the newer role
  map unchanged;
- a malformed forward-version-99 project stayed listable and identity-only;
  project-menu New session/Remove and sidebar Spawn were disabled, Settings
  exposed refresh only, mutation returned the stable unreadable 409, and its
  raw SQLite config hex was byte-for-byte unchanged;
  and
- a healthy project beside the unreadable row remained fully editable.

The isolated temporary data directory was deleted after acceptance. The Forge
development process reported exit 1 only because it was intentionally stopped
with SIGINT; this was shutdown evidence, not an application failure.

The first healthy-project API create returned `PROJECT_UNBORN` because the
temporary Git repository had no commit. Adding one empty initial commit made
the repository eligible and the acceptance proceeded. That was an explicit
fixture setup correction, not a hidden product retry.

## Accepted result

The desktop role-map editor is implemented, independently reviewed, fully
gated, and accepted in the real native Electron surface at `54cdbdd8` plus
`c7c1f565`. The accepted MVP remains stable, unreadable project containment is
preserved, no migration or capability promotion was introduced, and automatic
failover remains a separate opt-in product slice.
