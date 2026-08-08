# Agent B — service, HTTP, OpenAPI, CLI

## State: complete — implementation and scoped verification are green

## Next action
The orchestrator can integrate and commit Agent B's owned files. No code work is
left for Agent B. The generated artifacts are current and repeat regeneration is
idempotent; the coordinator's literal `git diff --exit-code` check exits 1 only
because both generated files are intentional uncommitted Phase 3B changes versus
HEAD, not because regeneration introduced additional drift.

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
- [x] Read the frozen Phase 3B contract, frozen cross-agent types, repository hard rules, and pause/switch service + controller patterns.
- [x] Added the exact narrow `failoverCommander`, Continue/preview service methods, and API-facing outcome without any caller-selected target field.
- [x] Added the four frozen failover error mappings plus fake-commander coverage for validation, mappings, delegation, preview, and unwired-manager behavior.
- [x] Ran `go test ./internal/service/session` from `backend/` (243 tests passed).
- [x] Added the operator-gated Continue route/handler with strict one-field JSON decoding, the shared 4 KiB cap, and the frozen success envelope.
- [x] Added manager-derived failover previews to worker list/get reads and Continue responses, including explicit nullable `failover`/`nextTarget` schema output.
- [x] Registered the OpenAPI operation and clean component names, ran `npm run api`, and verified the generated request, response, empty-string reason enum, and nullability.
- [x] Ran `go test ./internal/httpd/...` from `backend/` (369 tests passed across 6 packages).
- [x] Added `ao session continue --session <id> --incident <id>` with a one-field request, hand-mirrored response, backend-resolved target output, `spawnCallerHeaders()`, and no harness/model flags.
- [x] Added CLI coverage for success/body shape/operator headers, both required flags, forbidden target flags, and daemon error envelopes; `go test ./internal/cli -run '^TestSessionContinue_'` passes 6 tests.

## Remaining
- [x] Read contract + pause service/controller patterns
- [x] `failoverCommander` + `Service.ContinueFailover`
- [x] `toAPIError` arms
- [x] Route + handler + DTOs
- [x] `failover` read-model block
- [x] specgen registry entries
- [x] CLI command
- [x] `npm run api` regeneration
- [x] All listed tests green (the orchestrator added the required out-of-scope telemetry classification entry)

## Decisions / gotchas
- The request DTO and manager request are both one-field (`incidentId`) structures; no target-selection field may be introduced at any layer.
- Continue must type-assert the manager through a local `failoverCommander`, so service/tests compile independently of Agent A.
- The frozen public error table names four new failover codes, including `FAILOVER_RECOVERY_REQUIRED`; `ErrFailoverNotWired` intentionally has no public mapping and follows the existing `ErrSwitchNotWired` precedent, surfacing as a 500 wiring defect.
- Continue authorization and the 4 KiB cap reuse `authorizeOperatorPause` and `maxPauseBodyBytes` exactly.
- Registering `ao session continue` required an out-of-scope telemetry classification entry. The orchestrator added `ao session continue` to `legacyActorlessUserCLICommands` in `internal/telemetrymeta/cli.go`; Agent B did not edit that package.

## Final verification — 2026-08-08

Required scoped command from `backend/`:

```bash
go build ./... && go test ./internal/service/... ./internal/httpd/... ./internal/cli/...
```

- `go build ./...` — PASS (exit 0, no output).
- Aggregate scoped tests — PASS: **1235 tests across 21 packages**.
- `./internal/service/...` — PASS: **551 tests across 14 packages**.
- `./internal/httpd/...` — PASS: **369 tests across 6 packages**.
- `./internal/cli/...` — PASS: **315 tests across 1 package**.

Raw package result lines from the same scoped package set:

```text
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/agent
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/browser
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/chat
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/devimport
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/importer
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/notification
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/pr
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/project
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/review
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/session
?   github.com/aoagents/agent-orchestrator/backend/internal/service/settings [no test files]
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/spawncred
ok  github.com/aoagents/agent-orchestrator/backend/internal/service/usage
ok  github.com/aoagents/agent-orchestrator/backend/internal/httpd
?   github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr [no test files]
ok  github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec
ok  github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec/specgen
ok  github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers
?   github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope [no test files]
ok  github.com/aoagents/agent-orchestrator/backend/internal/cli
```

Generated artifacts:

- `npm run api` from the repository root — PASS.
- Literal `git diff --exit-code backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts` — exit 1, because these two generated files contain the intentional uncommitted Phase 3B API additions versus HEAD.
- A repeat `npm run api` produced byte-identical SHA-256 results for both files, proving the working-tree artifacts are current and regeneration is idempotent. Leave the regenerated files on disk for integration.

## Files changed by Agent B across both sessions

- `backend/internal/service/session/failover.go`
- `backend/internal/service/session/failover_test.go`
- `backend/internal/service/session/service.go` (failover mappings only)
- `backend/internal/httpd/controllers/sessions.go`
- `backend/internal/httpd/controllers/dto.go`
- `backend/internal/httpd/apispec/specgen/build.go`
- `backend/internal/httpd/apispec/openapi.yaml` (generated)
- `backend/internal/cli/session.go`
- `backend/internal/cli/session_test.go`
- `frontend/src/api/schema.ts` (generated)
- `docs/roles/mvp3b/AGENT_B_PROGRESS.md`

## Integration status

No Agent B blocker remains. The orchestrator-owned telemetry classification fix is already present and the scoped build/tests pass with it. Integration only needs to review and commit the uncommitted owned-file changes, including both generated artifacts together.
