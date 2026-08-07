# Agent B — service, HTTP, OpenAPI, CLI

## State: not started

## Next action
Read `docs/roles/PHASE3B_MVP_CONTRACT.md`, then
`backend/internal/service/session/pause.go` and the pause/switch handlers in
`httpd/controllers/sessions.go`, and mirror those patterns exactly.

## Brief

Expose Continue over HTTP and the CLI without letting a client choose a target.

### You own (create/edit only these)

- `backend/internal/service/session/failover.go` + `failover_test.go`
- the new failover arm inside `toAPIError` in `service/session/service.go`
- the route + handler in `httpd/controllers/sessions.go`
- `httpd/controllers/dto.go` (request/response + the `failover` read-model block)
- `httpd/apispec/specgen/build.go` (operation registry + `schemaNames` entries)
- generated `backend/internal/httpd/apispec/openapi.yaml` and `frontend/src/api/schema.ts`
- `backend/internal/cli/session*.go` + its tests

You must **not** edit `session_manager/` (Agent A), the frozen
`failover_contract.go` files, or the frontend beyond the generated `schema.ts`.

### Compile independently of Agent A

Declare your own narrow commander interface, exactly as
`service/session/pause.go` does with `pauseCommander`:

```go
type failoverCommander interface {
	ContinueFailover(ctx context.Context, id domain.SessionID, req sessionmanager.ContinueFailoverRequest) (sessionmanager.ContinueFailoverResult, error)
	FailoverPreview(ctx context.Context, id domain.SessionID) (sessionmanager.FailoverPreview, error)
}
```

Both types are already frozen and compile today. Type-assert `s.manager` and
return `sessionmanager.ErrFailoverNotWired` when it does not satisfy the
interface. Your tests use a fake commander — you never need A's implementation.

### Implement

1. **`Service.ContinueFailover(ctx, sessionID, incidentID)`** — validate the
   incident with the existing `validIncident` helper (same 400 codes as pause),
   delegate, map errors through `toAPIError`.
2. **`toAPIError` arms** for `domain.ErrFailoverNoTarget` →
   `FAILOVER_NO_TARGET` (409), `domain.ErrFailoverRoleRequired` →
   `FAILOVER_ROLE_REQUIRED` (400), `domain.ErrFailoverLimitReached` →
   `FAILOVER_LIMIT_REACHED` (409),
   `sessionmanager.ErrFailoverRecoveryRequired` → `FAILOVER_RECOVERY_REQUIRED`
   (409). Everything else already maps (`SWITCH_*`, `SESSION_NOT_PAUSED`,
   `PAUSE_INCIDENT_MISMATCH`, `SESSION_TERMINATED`, `NOT_A_WORKER`). An
   unmapped sentinel surfacing as a 500 is the specific defect this arm exists
   to prevent.
3. **`POST /api/v1/sessions/{sessionId}/continue`** — register beside
   `/pause` and `/resume`. Gate with **`authorizeOperatorPause`, reused
   verbatim**; do not write a second helper with the same semantics. Cap the
   body with `maxPauseBodyBytes`.
4. **Request DTO carries `incidentId` only.** No harness, no model, no role.
   Assert in a test that a body containing `targetHarness` cannot steer the
   result — the field must not exist, so `DisallowUnknownFields` (or the
   equivalent decode path used here) rejects it.
5. **Read model**: add the `failover` block from contract §9 to the session view,
   sourced from `FailoverPreview`. Do not re-derive the ladder in the service.
6. **CLI** `ao session continue --session <id> --incident <id>` — thin client
   over the HTTP route, hand-mirrored DTO, prints the backend-resolved target.
   No `--harness`/`--model` flag may exist. Usage errors exit 2, daemon errors
   exit 1, `spawnCallerHeaders()` no-upgrade rules as for switch.
7. **Regenerate** `npm run api` and commit both artifacts with the Go change.

### Tests you must write

- auth: LAN ok; valid operator token ok; agent capability headers →
  `PAUSE_AGENT_FORBIDDEN`; nothing → `PAUSE_AUTH_REQUIRED`; bad operator token →
  `OPERATOR_CREDENTIAL_INVALID`
- missing/oversized/invalid-charset incident → the two 400 codes
- stale incident → `PAUSE_INCIDENT_MISMATCH`
- not paused → `SESSION_NOT_PAUSED`
- no target / role missing / limit reached / recovery required → the four new codes
- in-progress → `SWITCH_IN_PROGRESS`
- happy path returns the resolved target, generation and `reused`
- CLI: happy path, missing flags (exit 2), daemon error envelope (exit 1)
- spec/route parity: `cd backend && go test ./internal/httpd/...`

### Verify

```bash
cd backend && go build ./... && go test ./internal/service/... ./internal/httpd/... ./internal/cli/...
npm run api          # from repo root
```

## Done
_(nothing yet)_

## Remaining
- [ ] Read contract + pause service/controller patterns
- [ ] `failoverCommander` + `Service.ContinueFailover`
- [ ] `toAPIError` arms
- [ ] Route + handler + DTOs
- [ ] `failover` read-model block
- [ ] specgen registry entries
- [ ] CLI command
- [ ] `npm run api` regeneration committed
- [ ] All listed tests green

## Decisions / gotchas
_(record anything a cold reader would need)_

## Verification run so far
_(none)_
