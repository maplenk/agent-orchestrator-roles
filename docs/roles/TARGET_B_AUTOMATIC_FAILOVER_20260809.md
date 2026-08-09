# Target B opt-in automatic failover — 2026-08-09

This record starts after the accepted roles MVP. It does not amend or replace
the accepted switch, pause ownership, manual Continue, runtime-probe,
starter-role, or live-acceptance records.

**Implementation:** `1c97c55e` (`feat: add dormant automatic failover engine`)

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
