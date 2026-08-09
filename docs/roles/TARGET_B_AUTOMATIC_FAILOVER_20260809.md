# Target B opt-in automatic failover — 2026-08-09

This record starts after the accepted roles MVP. It does not amend or replace
the accepted switch, pause ownership, manual Continue, runtime-probe,
starter-role, or live-acceptance records.

**Historical implementation:** `1c97c55e` (`feat: add dormant automatic
failover engine`); the content-equivalent roles-trunk commit is `29becc6d`.
**Historical dormant-product evidence:** `cbf47bf5`.
**Promotion-blocker correction:** `72bca3e4` (`fix: bind automatic failover
pauses to runtime generations`).

## Scope and result

The host now contains the opt-in automatic-failover state machine, but the
feature is deliberately dormant in the production product. The implementation
can take one automatic action for one stable, structured usage-limit incident;
it cannot take a second automatic rung for that incident. A failed first rung
stays paused for the existing explicit manual Continue action.

This is not a capability-promotion record or positive native automatic-failover
acceptance:

- `limit_detection_supported` remains `false` for every production harness;
- Claude `read_only_enforced` remains `false`;
- the production detector registry remains empty, and no production adapter
  supplies a limit event to the Router;
- new actionable automatic maps with failover targets are rejected by host
  validation because their source and target harnesses lack promoted
  structured-limit detection; and
- a legacy stored automatic map is shown as unavailable in the desktop and
  requires an explicit conversion to manual before other role-map changes can
  be saved.

Positive automatic behavior was accepted only in backend tests under injected
test capabilities. It was not accepted as reachable native product behavior.
Manual remains the default and the only production-operable failover mode.

## Host contract

### One-shot incident ownership

- Only an exact durable `usage_limit` pause with `detected_by=structured`, a
  valid structured envelope, and matching pause/envelope harness provenance can
  enter the automatic decision.
- The current project must still opt into automatic mode. Both the durable
  source harness and the selected or already-stored target must pass spawn,
  switch, and structured-limit capability checks immediately before runtime
  work.
- An in-memory session-and-incident fence closes concurrent deliveries before
  the first durable attempt exists. The attempt row and lifecycle ledger remain
  the cross-restart authority.
- Exactly one new rung may be selected for a stable incident. Requested and
  `post_stop` attempts reuse their stored target and generation; an
  acknowledged-but-unpromoted attempt may converge only on that same
  generation. Any terminal attempt suppresses another automatic selection.
- Duplicate ownership conflicts do not mutate the shared attempt. State-CAS
  misses re-read durable attempt and latest-session truth: a proven completed
  move converges, while disappearance, unreadable state, or an unsettled move
  fails loudly without writing a contradictory failure ledger.

### Crash recovery and boot safety

The boot pass covers every durable interruption point without turning recovery
into a retry policy:

- a crash after the structured pause but before the first attempt may take the
  one allowed action;
- a durable `requested` attempt is re-driven on its original rung and
  generation;
- a `post_stop` attempt recovers the already-stopped source on that same
  generation;
- a durable target acknowledgement before session promotion converges the
  existing target and then promotes it; and
- an acknowledged and promoted attempt clears only its exact incident pin.

The launch-capable automatic pass runs only after the board-wide live and reap
safety passes. Any collected `ErrBootUnsafe` finding, including an unresolved
terminated-runtime reap, stops automatic creation/destruction and RestoreAll.
Recovery re-lists latest session state, preserves the exact structured pause
and source provenance, and never infers death from an unknown runtime probe.

### Manual Continue remains unchanged

Automatic mode is only a trigger policy over the accepted Continue saga. The
existing host-owned ladder selection, generation identity, switch transaction,
attempt accounting, target acknowledgement, and incident-bound pause clear are
shared. The manual button remains available after an automatic terminal failure
and is the only authority that may select the next unused rung. No timer,
scheduler, retry loop, model-selected target, or new pause owner was added.

## Product and schema boundaries

The desktop distinguishes policy for new sessions from the policy used when a
current structured-limit action runs. A legacy automatic map renders
`Automatic (unavailable)`, explains why it cannot be saved, and offers an
explicit `Convert to manual` action. Conversion uses the existing atomic
role-map compare-and-swap boundary and preserves the role bindings,
permissions, and all failover ladders.

This slice adds no migration. Migration 9008 remains current and **9009 remains
the next fork migration number**. It changes no API or sqlc contract and adds
no daemon prompt or host/system instruction. API and sqlc regeneration were
therefore not applicable.

## Load-bearing regression coverage

Backend coverage pins:

- exact structured pause, envelope, harness, project mode, and full source plus
  target runtime-capability gates for new, active, `post_stop`, and
  acknowledged-but-unpromoted attempts;
- one durable attempt and one runtime under concurrent duplicate delivery;
- automatic/manual races, including a manual adopter that completes before the
  original owner resumes, with one shared generation and no false failure row;
- Resume revoking an incident-bound switch before runtime work;
- terminal failure remaining paused and never spending a second automatic
  rung, while manual Continue retains authority over the next rung;
- latest-state read failures, missing sessions, CAS-loss truth, and initial
  owner `ErrSwitchInProgress` as non-mutating outcomes;
- crash recovery at pause-only, requested, `post_stop`, target-ack-before-
  promotion, and promoted-before-pin-clear boundaries; and
- the board-wide boot-safety gate preventing all automatic runtime effects when
  a terminated-runtime reap yields `ErrBootUnsafe`.

Frontend coverage pins the unavailable legacy state, explicit manual
conversion, localized policy copy in all eight catalogs, and preservation of
the authored roles and ladders through conversion.

## Independent review and honest fixed failures

The implementation was reviewed in small read-only passes. Findings were fixed
before the final gates rather than reclassified:

- an initial fake-harness second-rung regression and a lifecycle-ledger
  generation mismatch were corrected;
- the first boot-pass insertion exposed build and ordering failures; the pass
  was placed after the complete board-wide live/reap safety gate and before
  RestoreAll;
- reviewer findings tightened automatic/manual race ownership, made the initial
  owner's switch conflict non-mutating, pinned SwitchWorker to the exact
  non-nil incident, and required full source-and-target runtime capabilities;
- structured envelope, durable pause, and current-session provenance checks
  were made exact, and latest-state read errors stopped being collapsed into a
  no-op result; and
- final race review added completion-before-original-resume convergence,
  truthful failure-CAS reconciliation, and positive target-ack-before-promotion
  boot recovery. The final independent backend review reported no remaining
  P1, P2, or P3 finding.

The test and packaging environment also had four visible failures and
environment corrections:

- pinned golangci-lint initially reported two `copyloopvar` findings; both were
  fixed before the final zero-issue run;
- a borrowed `node_modules` tree failed to provide a valid full-Vitest
  environment; that failure is retained and is not presented as an accepted
  suite run;
- `npm run build` failed because this frontend package has no `build` script;
  the canonical `npm run package` command was used and passed; and
- the first two native launches failed because an `--ignore-scripts` dependency
  setup lacked Electron's required metadata. A deliberate Electron override
  supplied the valid runtime for the successful native walkthrough.

## Verification

All final gates ran after the review corrections.

| Gate | Exact result |
|------|--------------|
| Backend build | `cd backend && go build ./...`: **pass** |
| Backend vet | `cd backend && go vet ./...`: **pass** |
| Full backend normal | `cd backend && go test ./...`: **4,804/4,804 passed across 132 packages** |
| Full backend race | `cd backend && go test -race ./...`: **4,804/4,804 passed across 132 packages** |
| Formatting / diff | `gofmt` and `git diff --check`: **clean** |
| Pinned lint | golangci-lint **v2.12.2**: **pass, zero issues** after the two fixed `copyloopvar` findings |
| Focused frontend | Focused renderer Vitest: **48/48 passed** |
| Full frontend Vitest | Full renderer Vitest: **2,060/2,060 passed across 153 files** |
| Frontend typecheck | `cd frontend && npm run typecheck`: **pass** |
| Frontend build script | `npm run build`: **not present; command failed honestly with missing script** |
| Canonical desktop package | `cd frontend && npm run package`: **pass** |
| Generated contracts | API/sqlc regeneration: **not applicable; no contract or query changed** |

## Native Forge Electron acceptance

Native validation ran only after the clean gates, using the real Forge Electron
application with an isolated temporary `HOME` under `/tmp`. The temporary tree
was removed afterward, and the user's real `~/.ao` was not read or modified.

The accepted dormant-product walkthrough proved:

- the Electron application launched its daemon on `127.0.0.1:3002`;
- a legacy automatic role map rendered the `Automatic (unavailable)` warning;
- the operator explicitly selected `Convert to manual` and the role-map PUT
  returned HTTP 200;
- all three role ladders remained present with one rung each after conversion;
- the original manual role map was restored with SHA-256
  `d47e3d91bbbca8724f87b20a34bf88b04495b4227b2397cc5fec0479a6431a8c`;
- the isolated database contained zero failover attempts throughout; and
- the exact daemon child was stopped and the temporary data was deleted.

This walkthrough accepted the unavailable-state warning and explicit safe
conversion, not a positive automatic runtime switch. Positive automatic
acceptance remains implementation-only under injected capabilities until a
real detector, stable incident identity, separate capability promotion,
independent review, and live acceptance exist.

## Accepted result

The opt-in automatic-failover engine is implemented, independently reviewed,
fully gated, and intentionally dormant at `1c97c55e`. It can perform one
host-authorized action per stable structured-limit incident and recover that
same action safely across crashes, while preserving the accepted manual
Continue path. Production capability truth remains unchanged, no production
detector ingress exists, no schema or prompt boundary moved, and the accepted
MVP remains stable.

## Promotion-blocker closure — runtime-generation ownership

The historical implementation and evidence above remain an accepted dormant
engine record, but they were not sufficient for capability promotion. An
independent review found that the Router's runtime-generation guard was used
only while creating the durable pause and was not retained in `pause_json`.
After an explicit **Restart agent** created generation B while preserving a
structured pause observed on generation A, a later boot could reinterpret A's
incident as authority to switch B. A delayed duplicate of A's event could also
reach same-incident idempotence without that ownership being re-established.
This was not production-reachable because all limit-
detection capabilities were false, the detector registry was empty, and the
Router had no production caller.

Correction `72bca3e4` closes that promotion blocker without promoting the
feature:

- each newly authored structured usage-limit pause requires the complete
  harness, runtime-launch, and no-switch-pending guard, and stores the exact
  observed runtime launch ID in the existing internal JSON pause column;
- legacy structured pins with no observed generation stay readable, visible,
  explicitly resumable/restartable, and manually continuable, but they cannot
  start a new automatic attempt and are never backfilled or upgraded by a
  later same-incident delivery;
- both the initial-read and lost-CAS same-incident paths converge only when the
  current harness/generation, stored harness/generation, and switch exclusion
  all match the original observation;
- before the first automatic attempt, current runtime generation must equal
  the pause's observed generation, and the switch saga repeats that check on
  its authoritative read under switch ownership before runtime effects;
- Restart agent and automatic continuation exclude one another across the
  first-attempt check, durable append, and switch handoff; and
- durable `requested`, `post_stop`, and acknowledged recovery remains governed
  by the existing attempt target and switch generation. Manual Continue keeps
  its accepted authority over legacy pins and later rungs.

No migration was added: `pause_json` is additive JSON and **9009+ remains the
next fork migration number**. The internal generation field is deliberately
absent from the public pause DTO, so no API or frontend schema changed. No sqlc
query, prompt, detector registry, Router ingress, capability cell, or Claude
read-only value changed.

### Generation-binding regression and review record

Load-bearing tests cover exact JSON/store round-trip and generic-update
preservation, raw legacy decode/list behavior, complete structured-guard
requirements, stale duplicates before the first read and after a lost CAS,
legacy explicit Resume/Restart/manual Continue, and the exact reported crash
sequence: bind generation A, explicitly Restart to generation B while
preserving the pin, construct a fresh manager, and prove boot reconciliation
writes zero attempts and performs zero runtime create/destroy against B.
Additional ownership and authoritative-read mutations prove Restart is refused
while automatic continuation owns the first-attempt window, and requested and
`post_stop` recovery still converges on its already-durable generation.

Independent implementation review found and fixed two P2 gaps in the first
correction draft. The lost-CAS same-incident path initially compared only the
incident ID; it now uses the same exact ownership predicate as the ordinary
idempotence path. The original Restart regression had also replaced the sole
legacy case with a generation-bound fixture that omitted the required evidence
envelope, while explicit legacy Resume was not pinned. The final table tests
use valid generation-bound and legacy structured pins, preserve either binding
exactly across Restart, and separately prove legacy Resume. Three final read-
only implementation reviews reported no remaining P1, P2, or P3 finding.

| Correction gate | Exact result |
|-----------------|--------------|
| Focused backend normal | domain/session-manager/SQLite store: **1,002/1,002 passed** |
| Focused backend race | domain/session-manager/SQLite store: **1,002/1,002 passed** |
| Full backend normal | `go test ./...`: **4,835/4,835 passed across 132 packages** |
| Full backend race | `go test -race ./...`: **4,835/4,835 passed across 132 packages** |
| Build / vet / format | `go build ./...`, `go vet ./...`, `gofmt -d`, and `git diff --check`: **pass/clean** |
| Pinned lint | golangci-lint **v2.12.2**: **0 issues** |
| Frontend focused | pause/restart/role-editor renderer coverage: **164/164 passed across 5 files** |
| Frontend typecheck | `npm run typecheck`: **pass** |
| Full frontend Vitest | **2,060/2,060 passed across 153 files** |
| Electron package | `npm run package`: **pass**, including production Vite bundles and arm64 Forge packaging |
| Generated contracts | API/sqlc regeneration: **not applicable; no contract or query changed** |

The first frontend invocation in the isolated worktree failed before testing
because dependencies were absent. A symlinked dependency tree then passed
typecheck and all 164 focused tests, but the first full run also produced one
Vite allowed-root failure. Replacing the links with temporary physical copies
removed that setup error, but five landing-document tests still failed because
the isolated checkout had no installed dependency tree for the separately
locked `frontend/src/landing` package. (`cheerio` is correctly declared in that
package's manifest and lockfile.) Installing that nested lockfile exactly with
`npm ci --ignore-scripts` produced the green 2,060/2,060 result above. All
temporary dependency trees were excluded from the slice and deleted after the
native check.

### Native Electron negative acceptance

Only after the clean correction gates, the real native Forge Electron app was
launched twice against an isolated acceptance root, `HOME`, `AO_DATA_DIR`,
`AO_RUN_FILE`, tmux socket, and port 3002. The bundled daemon was built from
exact correction commit `72bca3e42ec41ba450e17a8505a317311eadfb25`; the
production Forge package command also completed successfully.

The native daemon registered the deterministic acceptance project and spawned
worker `mvpacc-1` on Claude generation
`7a039b87-04bc-4f2b-9b00-2b33ed5e1906`. In the isolated database, the test
converted the operator pause into a valid structured usage-limit pin bound to
that exact generation and retained a legacy automatic role map. The tmux
runtime was then terminated deliberately; because a paused session does not
reinterpret an unavailable probe as exited, the isolated fixture's activity
fact was explicitly marked `exited` before invoking the real bodyless
`POST /api/v1/sessions/mvpacc-1/resume-agent` route. Restart succeeded and
created generation `3186f46d-0dec-4303-9773-642a7e2a7bdb` while preserving the
original A-bound pause and creating zero failover attempts.

After stopping and relaunching the same native Electron app, startup logged
that automatic action was skipped for the paused session. Before/after
snapshots proved all of the following:

- B's runtime launch ID was unchanged;
- the A-bound `pause_json` was byte-for-byte unchanged;
- failover attempts remained **0**;
- failover ledger rows remained **0**; and
- the same B runtime remained alive.

The renderer loaded the isolated project/session through the native window and
the daemon served the expected pause/failover view. macOS screen-capture
permission prevented a screenshot, so this record claims native process,
renderer-request, API, SQLite, and runtime evidence—not visual-control proof.
Both native launches were stopped with scoped SIGINT (Forge's expected exit 1),
port 3002 was verified closed, and the temporary roots were deleted. The real
default `~/.ao` tree was never read or modified.

This is negative blocker acceptance only. Positive automatic native acceptance
still requires a real detector, production ingress, a separate capability-
promotion commit, clean gates, independent review, and live proof.

The correction closes the known runtime-generation ownership blocker. It does
**not** approve promotion: checklist status remains partial and every dormant
truth recorded above remains unchanged.
