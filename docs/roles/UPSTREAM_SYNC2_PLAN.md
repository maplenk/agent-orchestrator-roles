# Upstream Sync 2 — execution and acceptance plan

**Status:** merged and tested on the throwaway integration branch; acceptance is
still open before merging back to the roles trunk.

| Item | Value |
|------|-------|
| Integration branch | `roles/upstream-sync-2` |
| Current checkpoint | `e01702b6` |
| Upstream pin | `fa799a7a58e2f9ec13d174567aff436ba890ff6a` |
| Roles trunk awaiting merge | `roles/multi-sub-v1` @ `1f80bdb5` |
| Upstream distance at checkpoint | 0 commits behind the pin |
| Fork migrations | `9000`–`9007`; next fork migration is `9008+` |

This is the authoritative step tracker for the second upstream integration.
`UPSTREAM_SYNC_PLAN.md` and `UPSTREAM_SYNC_DOGFOOD.md` describe the first sync
and remain historical evidence; do not rewrite their recorded SHAs or migration
numbers to look current.

## Why Sync 2 needed a new migration range

Upstream allocated migration `0053` to Muse and Chat added `0066`–`0079` after
the first roles sync had already assigned `0053`–`0060` to fork migrations.
Goose identifies a migration by number, so renumbering without repairing old
fork histories would silently skip upstream's migration 53.

The fork migrations now live at `9000`–`9007`. The pre-goose repair identifies
an old fork database from its physical schema fingerprint, not from version 53
alone: on an upstream database, 53 legitimately means Muse. The repair moves
old fork history to the 9000 range before goose evaluates the upstream chain.

## Steps 1–8

| Step | Gate | State | Evidence / remaining work |
|-----:|------|-------|---------------------------|
| 1 | Fresh migration | **Done** | Fresh database applied 69 versions: upstream 0053, Chat 0066–0079, fork 9000–9007. Role, pause, switch and Chat columns coexist; Muse is admitted by the harness constraint. |
| 2 | Legacy repair | **Done** | A real pre-sync fork database was repaired. Its roles data survived, upstream 53 became Muse, 9000–9007 were recorded, and a second boot was idempotent. |
| 3 | Strict role + Chat | **Done** | Read-only roles are refused before artifacts, worktrees or controllers exist; writable role model/template pins reach Chat; explicit harness/model/mode overrides are rejected on role-pinned requests. Spawn and interface-transition preflights run before destructive work. |
| 4 | Pause boundaries | **Done** | Chat and TUI automatic sends are fenced, user sends remain allowed, boot does not restart paused sessions, paused-dead sessions distinguish Resume from Restart agent, and paused lifecycle observations retain the active ownership row. |
| 5 | Existing sagas | **Implementation pass; live acceptance incomplete** | Worker Codex→Claude switch passed with ordered ledger and stable role pin. Chat switch/fresh refuses before stopping its controller. Switch and interface-transition sentinels are distinct. Still capture on this merged tree: (a) orchestrator fresh conversation and (b) a genuine durable `post_stop` without `target_ack` recovered on the original generation. |
| 6 | Muse | **Done** | Muse is spawn-capable in the registry, binds writable roles and role models/templates, and remains false for read-only, switch, limit detection and failover source/rung capabilities. |
| 7 | Desktop integration | **Safe portion done; transition UI deferred** | Paused live/dead states, Resume versus Restart agent, strict role composer, Muse TUI-only presentation and dead-Chat 409 mapping are covered. Interface-transition UI remains deferred behind the product fence. |
| 8 | CI matrix | **Run; acceptance open** | `gofmt`, build, vet, golangci-lint, backend tests, API drift and TypeScript checks pass locally. `-race` found no data races but has three timing failures reproduced on upstream. Full Vitest has one deterministic fork failure. Required GitHub Actions have not yet supplied the final Ubuntu acceptance signal. |

## Ordered acceptance blockers

Do these before merging the integration branch back to `roles/multi-sub-v1`:

1. Fix the stale same-URL expectation in
   `frontend/src/renderer/lib/api-client.test.ts`. Production intentionally
   injects operator headers on same-URL requests; the deterministic test still
   expects the pre-fix `Request` identity.
2. Add wiring-level Chat rollback regressions for both launch failure shapes:
   initial-turn or `MarkSpawned` failure followed by a failed termination must
   preserve `ErrLaunchCleanupUnresolved` and must not leave an untracked
   controller or a false live row.
3. Capture the two missing live Step 5 records on the merged tree:
   orchestrator fresh conversation and genuine post-stop recovery without a
   pre-existing `target_ack`.
4. Run the required GitHub Actions jobs. Treat the local race timing comparison
   as diagnostic evidence, not as a substitute for the repository's Ubuntu
   required checks.
5. Merge `roles/upstream-sync-2` into `roles/multi-sub-v1`, then update this
   file and `REMAINING_PLAN.md` with the merge SHA.

## After Sync 2

The product critical path resumes in this order:

1. **Phase 3A-2b vendor detection:** obtain sanitized, stable vendor fixtures;
   implement one adapter detector at a time; promote
   `limit_detection_supported` only in a separate, evidence-backed change.
   The registry is intentionally empty today.
2. **Phase 3B:** manual continue on an authorized failover rung, then bounded
   opt-in automatic failover. Preserve `role_id`; default remains manual.
3. **Phase 1-B Claude read-only:** genuine enforcement plus negative runtime
   tests. This unblocks strict-project Phase 2B-3 cross-harness orchestrator
   switching.
4. **Desktop role-map editor:** the strict composer consumes a role map, but
   creating or editing that map is still API/CLI-only.

## Non-claims

- `limit_detection_supported` remains false for every harness; no vendor limit
  detector has been promoted.
- Phase 2B-3 remains blocked on Claude read-only enforcement.
- Interface-transition UI is not accepted merely because the backend exists.
- A local matrix with upstream-equivalent timing failures is not a green
  required CI run.
