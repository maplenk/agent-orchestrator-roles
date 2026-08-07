# Phase 3B manual-failover MVP — orchestrator board

**Contract:** [`../PHASE3B_MVP_CONTRACT.md`](../PHASE3B_MVP_CONTRACT.md) — frozen, nobody edits.
**Branch:** `roles/multi-sub-v1`
**Frozen code:** `backend/internal/domain/failover_contract.go`,
`backend/internal/session_manager/failover_contract.go` — frozen at `daee190a`
on `roles/multi-sub-v1` (`go build ./...` green there), amended in place after
the durability review and green again.

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

**Wrapper status** (the harness agent) and **delegation status** (the model that
actually writes the code) are tracked separately, because they fail
independently: a wrapper can be alive and healthy while its delegate is refusing
calls.

| Agent | Slice | Wrapper | Delegation | Progress file | Wrapper status |
|---|---|---|---|---|---|
| A | Failover core: domain, manager saga, migration 9008, store | Claude Opus 5 | — (direct) | `AGENT_A_PROGRESS.md` | **stopped 2026-08-07** pending contract amendment |
| B | Service + HTTP + OpenAPI + CLI | Claude Opus 5 wrapper → `gpt-5.6-sol` via CLIProxy | unconfirmed | `AGENT_B_PROGRESS.md` | running |
| C | Desktop UI: Continue control, locales | Claude Opus 5 | — (direct) | `AGENT_C_PROGRESS.md` | running |
| D | Restart-assignment defect (paused-dead seam) | Claude Opus 5 wrapper → `gpt-5.6-sol` via CLIProxy | unconfirmed | `AGENT_D_PROGRESS.md` | running |

`codex-implementor` is a Claude Opus 5 agent whose only tool is Bash; it shells
out to `~/.claude/bin/cliproxy-run gpt-5.6-sol`. So B and D are **thin-Claude,
not zero-Claude** — the wrapper spends Claude tokens passing the brief through
and reporting back, and only the implementation runs on Codex. Any UI that
labels these rows shows the wrapper's model, which is why the two columns exist.

**Delegation status is `unconfirmed` until an agent reports which model did the
work.** A live `claude -p --model gpt-5.6-sol` process and a listening proxy on
`127.0.0.1:8317` are evidence the path works, not proof either slice completed
through it. One process snapshot is not a basis for reassignment.

Providers are deliberately mixed, not uniform. Claude capacity is the scarce
resource on this run, so the two slices needing the deepest repo judgment (A's
ordering/recovery invariants, C's design-system fidelity) hold it directly.

**If a delegation reports overload or makes no progress: move B to Grok, not D.**
B is broader but pattern-driven and sits behind frozen interfaces, so a provider
change costs little. D touches restart/lifecycle correctness and stays on Codex.
The old agent must be **stopped before** re-dispatch — two writers on one slice
is a worse failure than a slow one.

Agents do **not** commit. The orchestrator commits at each integration step, so
a half-finished agent leaves working-tree changes and a progress file, never a
broken commit.

Ownership table is §11 of the contract. An agent that needs a file it does not
own **stops and reports** — it does not edit across the line.

## Wave 2 — integration (orchestrator)

### Isolation, and the compromise forced by timing

The first version of this plan was incoherent: with all four agents sharing one
dirty checkout, a "gate after merging A" actually tests A+B+C+D's uncommitted
work, and any `git add -A` integration commit silently captures another slice.

The correct shape is per-agent worktrees with checkpoint commits, cherry-picked
in order and verified from a clean checkout. B, C and D were already live in the
shared tree when the review landed, and stopping healthy agents to retrofit
isolation costs more than it buys. So:

- **Agent A** — re-dispatched into its own git worktree on branch
  `roles/3b-agent-a`, with checkpoint commits. Its two in-flight untracked files
  move there. Cherry-picked first.
- **B, C, D** — stay in the shared tree. Their ownership is disjoint **by path**,
  so slices remain separable even though the tree is shared.
- **Integration never runs `git add -A`.** Each slice is committed from *its own
  declared paths only*, then that commit is verified **from a clean checkout** —
  a throwaway worktree at that exact commit, where the gate runs. That is what
  makes the per-slice gate mean something rather than testing everyone's WIP.

Residual risk, stated: a slice that writes outside its declared paths is caught
only by the clean-checkout gate. That gate is the reason it exists, and
`git status` is reviewed against the ownership table before each commit.

### Order

1. Agent A — core/storage, cherry-picked from `roles/3b-agent-a`
2. Agent D — lifecycle seams only
3. Agent B — service/API/CLI, then `npm run api` to regenerate artifacts
4. Agent C — frontend, rebased onto the generated `schema.ts`

Gate after each, run in a clean worktree at that commit:
`go build ./...` + the packages that slice owns.

### Full gate before independent review

```bash
gofmt -l backend/                       # must print nothing
cd backend && go vet ./... && go test -race ./...
npm run lint                            # go test ./... + golangci-lint v2.12.2
npm run frontend:typecheck
npm --prefix frontend test              # vitest run — the UI slice's real coverage
npm run api && git diff --exit-code \
  backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
```

The last two are not optional extras: the UI slice is mostly Vitest, so
typecheck alone would gate a slice by its least informative check, and the
api-drift job is what CI fails on if the generated artifacts were not committed
with the Go change.

Then an independent reviewer (a model that did **not** implement the layer)
attacks: incident replay, stale pause clearing, free-form target injection,
capability bypass, pause cleared before ack, double runtime after recovery,
failure enabling an automatic retry, role/template/model drift, restart falsely
claiming assignment delivery.

## Wave 3 — live dogfood

The nine acceptance records in contract §12. `limit_detection_supported` stays
`false`; nothing is promoted by this MVP.
