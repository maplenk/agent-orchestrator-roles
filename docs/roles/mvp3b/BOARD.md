# Phase 3B manual-failover MVP — orchestrator board

**Contract:** [`../PHASE3B_MVP_CONTRACT.md`](../PHASE3B_MVP_CONTRACT.md) — frozen, nobody edits.
**Branch:** `roles/multi-sub-v1`
**Frozen code:** `backend/internal/domain/failover_contract.go`,
`backend/internal/session_manager/failover_contract.go` — both compile on
`main` already (`go build ./...` green at freeze time).

## Why every agent keeps a todo file

Provider limits are real and may land mid-task. Each agent owns a
`AGENT_<X>_PROGRESS.md` in this directory and **updates it after every
meaningful step**, before moving on. A file is only useful if a *different
model on a different provider* could read it cold and continue: record what is
done, what is in flight, the exact next action, and any decision made along the
way. Chat context does not survive a provider switch; these files do.

Format each agent must keep:

```markdown
## State: <not started | in progress | blocked | done>
## Next action (one sentence, actionable cold)
## Done
- [x] …
## Remaining
- [ ] …
## Decisions / gotchas
- …
## Verification run so far
- `cd backend && go test ./internal/…` → pass/fail
```

## Wave 1 — four agents, disjoint file ownership

| Agent | Slice | Progress file | State |
|---|---|---|---|
| A | Failover core: domain, manager saga, migration 9008, store | `AGENT_A_PROGRESS.md` | not started |
| B | Service + HTTP + OpenAPI + CLI | `AGENT_B_PROGRESS.md` | not started |
| C | Desktop UI: Continue control, locales | `AGENT_C_PROGRESS.md` | not started |
| D | Restart-assignment defect (paused-dead seam) | `AGENT_D_PROGRESS.md` | not started |

Ownership table is §11 of the contract. An agent that needs a file it does not
own **stops and reports** — it does not edit across the line.

## Wave 2 — integration (orchestrator)

Merge order, one build/test gate per step:

1. Agent A — core/storage (`go test ./internal/domain/... ./internal/session_manager/... ./internal/storage/...`)
2. Agent D — lifecycle seams only
3. Agent B — service/API/CLI, then `npm run api` to regenerate artifacts
4. Agent C — frontend, rebased onto the generated `schema.ts`
5. Full gate: `npm run lint`, `go test -race ./...`, `npm run frontend:typecheck`

Then an independent reviewer (a model that did **not** implement the layer)
attacks: incident replay, stale pause clearing, free-form target injection,
capability bypass, pause cleared before ack, double runtime after recovery,
failure enabling an automatic retry, role/template/model drift, restart falsely
claiming assignment delivery.

## Wave 3 — live dogfood

The nine acceptance records in contract §12. `limit_detection_supported` stays
`false`; nothing is promoted by this MVP.
