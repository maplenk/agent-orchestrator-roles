# Upstream integration plan — before Phase 3A-2b

> **Historical first-sync plan.** This records the integration to `4efd8a10`
> and its then-current 0053–0060 migration assignment. Do not use it as the
> current execution tracker. Sync 2 is tracked in
> [`UPSTREAM_SYNC2_PLAN.md`](UPSTREAM_SYNC2_PLAN.md).

Review recommendation, accepted: stop before 3A-2b and sync upstream first.
Designing the structured-detection seam against the pre-sync architecture risks
building a parallel event path alongside upstream's event-driven usage plumbing
(#2928), and Phase 3 touches exactly the domain/storage/API/lifecycle files that
already overlap.

## Measured state (re-verified, not quoted)

Numbers differ slightly from the review because upstream moved again in between.

| | Review | Measured now |
|---|---|---|
| upstream head | `84276887` | `4efd8a10` |
| behind | 49 | **51** |
| ahead | 67 | **69** |
| files touched on both sides | 37 | **36** |

Merge-base is `742c77bc`, matching `AO_BASELINE_SHA.txt`.

### A trial merge is more tractable than the overlap suggests

`git merge --no-commit --no-ff upstream/main` (run and aborted) conflicts in
**13** files — not 36. Four of those are **generated** and resolve by
regeneration rather than by hand:

- `storage/sqlite/gen/models.go`, `gen/sessions.sql.go` → `npm run sqlc`
- `httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts` → `npm run api`

Leaving **9 hand-merge files**:

| File | Why it collides |
|---|---|
| `session_manager/manager.go` | our boot chain vs upstream #3491 liveness/#3598 migration repair |
| `storage/sqlite/store/session_store.go` | our `sqlc.embed` collapse vs upstream session pinning (#3511) |
| `storage/sqlite/queries/sessions.sql` | same |
| `httpd/controllers/sessions.go`, `dto.go`, `sessions_test.go` | pause endpoints vs pinning fields |
| `httpd/apispec/specgen/build.go` | operation registry, both sides added entries |
| `daemon/daemon.go` | our boot gate vs upstream boot changes |
| `frontend/forge.config.ts` | our `profiles` extraResource + prePackage staging |

## The blocker the ordered plan does not mention

Renumbering the migrations is **not sufficient on its own**, and the failure it
leaves is silent.

goose tracks applied migrations **by version number**. Verified against this
machine's dev database:

```
$ sqlite3 ~/.ao/dev/data/ao.db 'SELECT version_id FROM goose_db_version ORDER BY version_id DESC LIMIT 8'
48 47 46 45 44 43 42 41
```

So on any database that has run this branch, after renumbering roles `0042→0053`:

1. **Upstream's `0042`/`0043`/`0044`/`0047` are silently SKIPPED.** goose sees
   those versions already applied — by *our* migrations — and never runs them.
   The database then lacks `sessions.pinned`, `agent_model_catalog`, review-run
   uniqueness and the batch backfill, while the merged code expects all four.
   Schema/code divergence with no error: the worst failure mode available here.
2. The renumbered roles migrations at `0053+` are treated as unapplied and
   **re-run**, where `ALTER TABLE ... ADD COLUMN` fails on an existing column.
   This one at least fails loudly.

So the integration needs an explicit decision on existing databases:

- **(a) Require a fresh data dir for dev.** Defensible — the fork has not
  shipped, so "existing databases" means developer machines only. The Phase
  2A/2B live dogfood has to be re-run against the merged tree anyway, and that
  wants a clean database.
- **(b) A repair step** that rewrites `goose_db_version` — delete the roles
  version rows, let upstream's run, then re-stamp. Upstream #3598 ("burned
  migration repair") appears to solve this class and should be read first rather
  than reinvented.

Recommendation: **(a)**, with (b) reconsidered only if a non-developer database
turns out to exist.

## Ordered steps

1. Branch `roles/upstream-sync` off `roles/multi-sub-v1`.
2. Merge `upstream/main`; resolve the 9 hand-merge files; regenerate the 4.
3. Renumber roles migrations `0042–0049` → `0053–0060`, preserving order.
4. Decide the existing-database story (above) and write it down.
5. `sqlc` + `api` regeneration, full suite, `-race`, golangci-lint at zero,
   `gofmt -l .` empty.
6. Reconcile upstream's event-driven usage (#2928) with 3A: it is telemetry, not
   structured limit detection, but the seam must consume it rather than open a
   second event path.
7. **Re-run the Phase 2A/2B live dogfood** against the merged tree. The 2B-1
   evidence was captured pre-merge and its `CHECK`-constraint lesson is exactly
   the class a migration renumber can reintroduce.
8. Only then 3A-2b.

## Upstream items that intersect our invariants

Read before resolving, not after:

- **#3491 liveness/migration/session-wipe**, **#3598 burned migration repair** —
  our fail-closed boot chain and `ErrBootUnsafe` family live here.
- **#3511 session pinning** — `SessionRecord`, the row adapters we just
  collapsed, API schema, frontend types.
- **#3443 / #3627 delegate through the project orchestrator** — role-map and
  orchestrator ownership.
- **#3386 adapter-aware model selection**, **#3483**, **#3166** — target-model
  authorization.
- **#3612 review-feedback auto-injection** — must respect the pause fence;
  it is an AO-initiated write and has to route through `sessionguard`.
- **#3351 `ao session resume`** — CLI name collision with pause-incident resume.
  Ours answers an incident id; theirs resumes an agent. Both exist in the
  contract as *different* operations (PHASE3A_PAUSE_CONTRACT §2), so the naming
  must distinguish them.
- **#3548 durable agent switching** (open, dirty) — direct Phase 2A overlap.
