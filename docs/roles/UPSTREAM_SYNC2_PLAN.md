# Upstream Sync 2 — execution and acceptance plan

**Status:** accepted. All eight steps are done and every required GitHub Actions
check is green; the only action left is merging back to the roles trunk.

**Accepted:** 2026-08-07, at head `dd06d31a` on `roles/upstream-sync-2`.

| Item | Value |
|------|-------|
| Integration branch | `roles/upstream-sync-2` |
| Current checkpoint | `dd06d31a` |
| Upstream pin | `fa799a7a58e2f9ec13d174567aff436ba890ff6a` |
| Roles trunk | `roles/multi-sub-v1` @ `5dc2fcfb` (Sync 2 merged 2026-08-07) |
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
| 5 | Existing sagas | **Done** | Worker Codex→Claude switch passed with ordered ledger and stable role pin. Chat switch/fresh refuses before stopping its controller. Switch and interface-transition sentinels are distinct. The two missing live records were then captured on this merged tree: an in-place orchestrator fresh conversation (`requested → pre_stop → post_stop → target_ack` under ledger kind `orchestrator_fresh_conversation`, same session id and harness, role pin and template artifact intact, pending cleared) and a genuine durable `post_stop` without `target_ack`, recovered on the original generation with exactly one `target_ack`, exactly one target runtime and pending cleared. Evidence: `UPSTREAM_SYNC2_DOGFOOD_STEP5.md`. |
| 6 | Muse | **Done** | Muse is spawn-capable in the registry, binds writable roles and role models/templates, and remains false for read-only, switch, limit detection and failover source/rung capabilities. |
| 7 | Desktop integration | **Safe portion done; transition UI deferred** | Paused live/dead states, Resume versus Restart agent, strict role composer, Muse TUI-only presentation and dead-Chat 409 mapping are covered. Interface-transition UI remains deferred behind the product fence. |
| 8 | CI matrix | **Done** | Every required GitHub Actions job is green on `dd06d31a` (PR #1), re-run after the amended tests; the earlier `7165c942` run predates them: **Go** (build-test incl. `go test -race ./...`, lint, api-drift), **Frontend** (test, renderer-smoke), CLI E2E, e2e-gate, gitleaks, Mobile, React Doctor. The three `-race` timing failures are local-only — they pass on the Ubuntu runner, which is what settles them. The deterministic Vitest failure is gone: that test asserted `instanceof Request`, a pass-through `runtimeFetch` deliberately stopped doing so same-URL requests still receive operator auth, so restoring it would have reopened the spawn-auth defect. Frontend is 1992 pass / 0 fail. |

## Ordered acceptance blockers

Do these before merging the integration branch back to `roles/multi-sub-v1`.
Items 1–4 are closed; item 5, the merge itself, is the only one still open.

1. ~~Fix the stale same-URL expectation in `api-client.test.ts`.~~ **Done**
   (`7165c942`). It asserts the URL and method now. The auth behaviour keeps
   its own sibling test, so removing the header injection still fails loudly.
2. ~~Add wiring-level Chat rollback regressions for both launch failure
   shapes.~~ **Done** (`7165c942`). Each branch asserts three things that are
   easy to conflate: the original cause survives, `ErrLaunchCleanupUnresolved`
   (an `ErrBootUnsafe`) survives, and an active row really is left behind — the
   last so the test fails if the fixture stops reproducing the condition it
   claims to cover. Both mutation-checked independently; before this, errcheck
   was the only thing standing between a discarded return and a silent boot
   hazard.
3. ~~Capture the two missing live Step 5 records on the merged tree.~~ **Done**
   (2026-08-07, `UPSTREAM_SYNC2_DOGFOOD_STEP5.md`). Both were captured on an
   isolated daemon: the orchestrator fresh conversation, and a genuine durable
   `post_stop` without `target_ack` recovered on the original generation. Note
   that `s2-4` was NOT such a specimen: it failed at `pre_stop`, so its source
   was never stopped and `RecoverSwitchFromPostStop` correctly reported nothing
   to recover. The real specimen needed the target launch to fail after the
   source stopped, which is why it had to be manufactured rather than found.
4. ~~Run the required GitHub Actions jobs.~~ **Done.** All green on
   `7165c942` (PR #1, draft). The local `-race` timing failures did not
   reproduce on the Ubuntu runner.
5. ~~Merge `roles/upstream-sync-2` into `roles/multi-sub-v1`.~~ **Done**
   (2026-08-07) as merge commit `5dc2fcfb`, a `--no-ff` merge so the sync
   stays revertable as one unit. Trunk verified after the merge: backend 4486
   pass, frontend 1992 pass / 0 fail, gofmt clean, typecheck clean.

**Sync 2 is closed.** The next work is Phase 3A-2b vendor detection.

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
