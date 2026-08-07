# Agent A — failover core

## State: STOPPED by the orchestrator 2026-08-07, mid-migration — contract amended, ready to re-dispatch

## Next action
Re-read amended contract **§5, §6, §6a, §6b** (they changed under you), then
update the `state` CHECK in the already-written 9008 migration to
`('requested','post_stop','acked','failed')` and continue from
`queries/session_failover.sql`.

## ORCHESTRATOR AMENDMENT — read before resuming

An independent durability review caught two contradictions in the frozen
contract **before** you implemented the saga. Both are now fixed in
`PHASE3B_MVP_CONTRACT.md`; the frozen `domain/failover_contract.go` changed with
them and still builds. What this means for your recorded design:

**1. The attempt row and the `requested` ledger row are ONE transaction.**
Contract §6 rule 1. Use the existing `Store.inTx`
(`storage/sqlite/store/store.go:70`) — its callback receives a `*gen.Queries`
that already carries `InsertLifecycleLedger`, so you can write both rows
atomically **without editing `lifecycle_ledger_store.go`**, which you do not
own. The previous wording left a crash window between two separate writes and
specified no repair. Add a test that fails the second insert and proves neither
row exists.

**2. The state machine gained `post_stop`, and `failed` narrowed.**
Contract §6a. Terminal-vs-recoverable is decided by *whether the source was
stopped*: a post-stop failure is **recoverable** and must never be written
`failed`. Idempotence now adopts any **non-terminal** attempt — use the new
`domain.FailoverAttemptState.Terminal()` helper rather than testing for
`requested`. Keying on `requested` alone was reachable as: `failed` attempt
later reaching `target_ack`, an attempt stuck `failed` after a successful
recovery, and a duplicate Continue launching a **second runtime** over an
unrecovered post_stop.

**3. D2 and D3 are SUPERSEDED. You get the `switch.go` hook after all.**
Contract §6b freezes `SwitchRequest.ForceGenerationID string` — empty preserves
today's behaviour exactly. Mint the generation yourself *before* the rule-1
transaction and pass it in, so `FailoverAttempt.GenerationID` is **never empty**.
Your D3 case 3 (adopt a `generation_id = ''` attempt by matching
`to_harness`/`to_model`/`role_id` and ledger ordering) disappears — and it
should, because a wrong guess there is a second runtime. You were right to flag
the seam rather than edit broadly; the answer is that the seam is worth a
three-line behaviour-preserving field, and **this is the one edit to `switch.go`
you are authorized to make**. Add a test proving the empty case is identical to
today.

**4. D4's premise changed.** You chose to put the incident id in the ledger row's
`generation_id` *because the switch generation was unknown at write time*. It is
now known. Re-decide with that fact — a real generation there lets an audit join
ledger to attempt directly. Your ledger-id composition and the
`isSwitchLedgerKind` exclusion still stand either way.

**D1, D5, D6, D7, D8 all survive unchanged** and are good calls. On D6: the
orchestrator will wire `ReconcileFailoverAttempts` into boot at integration —
keep it exported and keep it out of `manager.go`.

**5. You now work in your own git worktree** on branch `roles/3b-agent-a`, not
the shared checkout. Make checkpoint commits as you go. Your two in-flight files
have been moved there for you.

## Brief

Own the durable half of manual failover: the attempt record, rung resolution
against the pinned role, and the manager entry point that drives the **existing**
switch saga.

### You own (create/edit only these)

- `backend/internal/domain/failover.go` + `failover_test.go`
- `backend/internal/session_manager/failover.go` + `failover_test.go`
- `backend/internal/storage/sqlite/migrations/9008_session_failover_attempts.sql`
- `backend/internal/storage/sqlite/queries/session_failover*.sql`
- `backend/internal/storage/sqlite/store/session_failover_store*.go`
- regenerated `backend/internal/storage/sqlite/gen/*` (via `npm run sqlc`, never hand-edited)

You may **read** anything. You may not edit `manager.go`'s restore/relaunch
region (Agent D owns it), `service/`, `httpd/`, `cli/`, `frontend/`, or the two
frozen `failover_contract.go` files.

If the saga genuinely needs a hook inside `switch.go` that cannot live in
`failover.go`, stop and report the exact hook rather than editing broadly.

### Implement

1. **Migration 9008** per contract §5. Do not modify any merged migration. The
   lifecycle ledger needs no migration — `failover` is already in the 9002/9006
   `kind` CHECK.
2. **Store methods**: append an attempt, list attempts for
   `(session_id, incident_id)`, update state + generation. sqlc-generated, not
   hand-written SQL in Go.
3. **`Manager.ContinueFailover(ctx, id, ContinueFailoverRequest) (ContinueFailoverResult, error)`**
   with the frozen signature. Order of operations is contract §6 and is not
   negotiable:
   - load session; refuse non-worker (`ErrNotWorker`), chat
     (`ErrSwitchChatUnsupported`), terminated (`ErrTerminated`);
   - refuse if not paused (`ErrNotPaused`) or if the pin's incident differs from
     the request (`ErrIncidentMismatch`) — never re-read the pin as authority;
   - require a durable role pin (`domain.ErrFailoverRoleRequired`);
   - idempotence check **before** anything durable: latest attempt for this
     incident in `requested` state → adopt it (`Reused: true`), completing via
     `RecoverSwitchFromPostStop` when an incomplete `post_stop` matches its
     generation; a mismatched incomplete `post_stop` is
     `ErrFailoverRecoveryRequired`;
   - bound check against `domain.MaxFailoversPerIncident`;
   - `domain.NextFailoverRung(...)` with `used` = every prior attempt for this
     incident **in any state**;
   - mint the generation, then write the ledger `failover`/`requested` row **and**
     the attempt row in **one `inTx` transaction**, before the saga touches the
     runtime (contract §6 rule 1);
   - call the existing worker switch entry point, passing `ForceGenerationID`
     (contract §6b). One relaunch path only — do not open a second;
   - on `target_ack`: attempt → `acked`, then clear the pin **under a CAS on the
     same incident id**;
   - on **pre-stop** failure: attempt → `failed`, ledger `failed`, pin untouched,
     no retry, nothing scheduled;
   - on **post-stop** failure: attempt → `post_stop`, pin untouched. **Never
     `failed`** — it is recoverable on the same generation (contract §6a).
4. **`Manager.FailoverPreview(ctx, id) (FailoverPreview, error)`** — the read-time
   answer Agent B surfaces. Derived, never stored. Populate `Reason` precisely;
   the desktop renders it.
5. **Crash recovery**: boot's existing post_stop reconcile must leave the attempt
   row consistent (one runtime, one ack). Reconcile by `generation_id`.
6. **Role identity invariance**: `role_id`, `template_artifact_id`,
   `template_sha256` and `resolved_permissions` byte-identical across the move;
   only `resolved_harness`/`resolved_model` change. Assert it in a test.

### Tests you must write (manager + domain level)

- no role pin → refused before any durable write
- no ladder / exhausted ladder → `ErrFailoverNoTarget`, pause intact
- stale incident → refused **before** any ledger, attempt or runtime change
- a free-form target is structurally impossible (there is no field to carry one)
- **pre-stop** launch failure → pause intact, attempt `failed`, no second attempt
- **post-stop** launch failure → pause intact, attempt `post_stop` and **not**
  `failed`; a subsequent Continue adopts it rather than spending a second rung
- atomicity: fail the second insert of the rule-1 transaction, prove **neither**
  the ledger row nor the attempt row exists
- post-stop recovery → one runtime, one `target_ack`, same generation
- duplicate Continue → one attempt row, `Reused: true`, same generation
- `SwitchRequest.ForceGenerationID` empty → byte-identical to today's behaviour
- `role_id` and template artifact unchanged
- attempt bound reached → `ErrFailoverLimitReached`, still paused
- paused-live **and** paused-dead sources both work

### Verify

```bash
cd backend && go build ./... && go test ./internal/domain/... ./internal/session_manager/... ./internal/storage/...
npm run sqlc   # from repo root, after any query/migration change
```

Do not run the full `go test ./...` — other agents are mid-edit in packages you
do not own; the orchestrator runs the full gate at integration.

## Done
- [x] Read the contract, both frozen `failover_contract.go` files, `switch.go`,
      `pause.go`, `manager.go`'s `Store` interface, `rolemap.go`,
      `switch_targets.go`, `session_pause.go`, `lifecycle_ledger.go`,
      the 9002/9003/9007 migrations, `lifecycle_ledger_store.go`, `sqlc.yaml`.

## Remaining
- [x] Read contract + switch.go + pause.go
- [ ] Migration 9008
- [ ] sqlc queries + store methods
- [ ] `domain/failover.go` (helpers beyond the frozen resolution rule)
- [ ] `ContinueFailover`
- [ ] `FailoverPreview`
- [ ] Crash-recovery reconciliation by generation
- [ ] All ten acceptance tests
- [ ] Package tests green

## Decisions / gotchas

### D1 — the store surface is a NARROW OPTIONAL interface, not `Store`
`sessionmanager.Store` (manager.go:258) is implemented by `fakeStore` in
`manager_test.go` (7150 lines, shared with Agent D). Adding methods there would
force an edit to a file I do not own and collide with Agent D.

Instead `failover.go` declares its own `failoverAttemptStore` and type-asserts
`m.store` to it — **exact existing precedent**: `interface_transition.go:95`
does the same with `interfaceTransitionStore`. Unlike that one, a missing
implementation is NOT a silent degrade: Continue returns an error, because a
continuation that cannot record an attempt cannot be made idempotent.

### D2 — the switch generation is minted INSIDE the saga, so the attempt row is written with an empty generation and stamped afterwards
`switchUnderOwnership` calls `m.newSwitchGeneration()` itself; `SwitchRequest`
has no field to inject one, and `switch.go` is not mine. The frozen
`FailoverAttempt.GenerationID` comment ("Empty until the saga has begun")
anticipates exactly this, so no hook is requested.

Consequence: a hard process crash between the attempt write and the stamp leaves
`generation_id = ''` with a real incomplete `post_stop` in the ledger. Handled by
an explicitly bounded adoption rule, not by guessing — see D3.

### D3 — recovery matching, in order
1. `attempt.GenerationID != ""` and it equals the incomplete `post_stop`
   generation → `RecoverSwitchFromPostStop`.
2. `attempt.GenerationID != ""` and it differs → `ErrFailoverRecoveryRequired`.
3. `attempt.GenerationID == ""` (crash before the stamp) → adopt the incomplete
   `post_stop` **only** when its `(to_harness, to_model, role_id)` match the
   attempt's intent and its `requested` ledger row is not older than the attempt
   row. The session is paused and `beginSwitch` fences, so nothing else can have
   authored that generation. Anything else → `ErrFailoverRecoveryRequired`.

### D4 — the failover ledger row's `generation_id` carries the INCIDENT id
Like `appendPauseLedger`, not like `appendSwitchLedger`: the switch generation is
unknown when the `requested` row is written, and a column whose meaning changes
between phases is worse than one that consistently groups the incident. The
switch generation lives on the attempt row and in the ledger payload JSON.
Ledger id is `<session>:<incident>:failover:<seq>:<phase>` (unique per attempt
per phase; `appendPauseLedger`'s `<session>:<incident>:<kind>` would collide
across attempts). `failover` is **not** in `isSwitchLedgerKind`, so these rows
can never be mistaken for a recoverable switch.

### D5 — clearing the pin is the LAST durable act, and the retry path converges
Order on success: attempt → `acked` (+generation), then ledger
`failover`/`target_ack`, then `ClearSessionPauseIfIncident` (CAS on the same
incident). A crash before the clear leaves a paused session whose latest attempt
is `acked`; a retried Continue detects `latest.State == acked &&
latest.GenerationID == rec.Metadata.RuntimeLaunchID` and just finishes the clear,
returning `Reused: true`. Without that branch the retry would start a *second*
continuation for a move that already happened.

### D6 — crash reconciliation is LAZY, at the entry points I own
Boot's `pausedSkip` already excludes paused sessions from automatic post_stop
recovery, and `manager.go`'s reconcile region belongs to Agent D. So
`reconcileFailoverAttempts` runs at the top of both `ContinueFailover` and
`FailoverPreview`: an attempt in `requested` whose generation already has a
durable `target_ack` becomes `acked`; one whose generation has a `failed` row and
no live pending becomes `failed`. It is exported as
`Manager.ReconcileFailoverAttempts` so the orchestrator can additionally wire it
into boot at integration without me editing `manager.go`.

### D7 — sqlc 1.31 SQLite parser bug
`queries/sessions.sql:119-134` documents it: literals (and, it warns,
placeholders) on the RHS of `=` in DELETE/UPDATE `WHERE` clauses get silently
stripped and the truncated tail leaks into the NEXT generated const. The guarded
state update (`WHERE id = ? AND state = ?`) is exactly that shape, so after
`npm run sqlc` the generated const must be READ to confirm it is intact. If it is
mangled, fall back to raw `ExecContext` in the store with a NOTE in the queries
file, exactly as `SetSessionPauseIfAbsent` / `ClearSessionPauseIfIncident` do.

### D8 — `FailoverPreview` reason precedence, and how Agent B decides `null`
Checked in this order: non-worker/chat/terminated → `switch_unsupported`;
no role pin → `no_role_pin`; no ladder for the role → `no_ladder`; not paused →
`not_paused`; bound reached → `limit_reached`; `NextFailoverRung` →
`ladder_exhausted`; source/target `switch_supported=false` → `switch_unsupported`;
otherwise `Available: true`, `Reason: ""`.

Contract §9 wants the block `null` when the session is "not paused **and** has no
ladder". The manager cannot express `null` in a value type, so **Agent B decides
it from the pair it already holds**: `session.Metadata.Pause == nil &&
preview.Reason == FailoverReasonNoLadder` → emit `null`. `NextRungIndex` is `-1`
whenever there is no target.

## Verification run so far
_(none yet)_
