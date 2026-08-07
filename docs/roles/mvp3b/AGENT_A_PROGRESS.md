# Agent A — failover core

## State: not started

## Next action
Read `docs/roles/PHASE3B_MVP_CONTRACT.md` in full, then
`backend/internal/session_manager/switch.go` and `pause.go`, before writing the
9008 migration.

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
   - write ledger `failover`/`requested` **and** the attempt row before the saga
     touches the runtime;
   - call the existing worker switch entry point. One relaunch path only — do
     not open a second;
   - on `target_ack`: attempt → `acked`, then clear the pin **under a CAS on the
     same incident id**;
   - on failure: attempt → `failed`, ledger `failed`, pin untouched, no retry,
     nothing scheduled.
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
- launch failure → pause intact, attempt `failed`, no second attempt
- post-stop recovery → one runtime, one `target_ack`, same generation
- duplicate Continue → one attempt row, `Reused: true`, same generation
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
_(nothing yet)_

## Remaining
- [ ] Read contract + switch.go + pause.go
- [ ] Migration 9008
- [ ] sqlc queries + store methods
- [ ] `domain/failover.go` (helpers beyond the frozen resolution rule)
- [ ] `ContinueFailover`
- [ ] `FailoverPreview`
- [ ] Crash-recovery reconciliation by generation
- [ ] All ten acceptance tests
- [ ] Package tests green

## Decisions / gotchas
_(record anything a cold reader would need)_

## Verification run so far
_(none)_
