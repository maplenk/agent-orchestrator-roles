# AO multi-sub roles fork

Local working tree: `/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles`

## Baseline

| Field | Value |
|-------|--------|
| Upstream | `Untrivial-ai/agent-orchestrator` |
| Remote name | `upstream` |
| Pinned SHA | See `AO_BASELINE_SHA.txt` |
| Roles trunk | `roles/multi-sub-v1` |
| Evidence integration | `codex/mvp-integration` @ `322f9c18`; merge to the roles trunk is not claimed here |
| Accepted implementation/runner | `166e9e63` |
| Promoted live evidence | `322f9c18` |
| Active gate | Not fully green repository-wide: zero races, SQLite exact 5/5 and package race passed, Chat test race fixed at `f8883529`; static/typecheck/API drift and full frontend (151/151 files, 2040/2040 tests) pass at `6473b134`; ordinary full retains three known untouched wall-clock failures |

Pin before feature work. Own migration numbers on this fork (do not collide with upstream 0042 races from #3548 / #3386).

## Product

Intent-grade **role templates** + daemon-resolved **`ao spawn --role`** multi-subscription routing, durable switch/pause/failover, on top of AO’s UI.

Master plan (design): `MASTER_PLAN.md`
**Status + remaining execution plan:** `REMAINING_PLAN.md` (completed vs open, order, gates).
**Current MVP specification:** `MVP_FINAL_SPEC.md`.
`UPSTREAM_SYNC2_PLAN.md` is closed historical integration evidence.

## Delivery

**Target B** — full wishlist.
- Phase 1 **foundation** accepted: roles, CAS, canSpawn, Codex RO, capability registry, CLI roleMap, template Option A.
- Strict routing/delegation is independent of technical read-only. Both
  permission booleans are explicit; `workspaceWrites:false` remains capability
  gated.
- Claude `read_only_enforced=false` (honest); explicitly read-only roles still
  require Codex or another enforcing harness.
- Phase 2A is accepted; Phase 2B-0/1/2 and the Phase 3A pause/UI boundary landed.
- Phase 3B manual Continue and Phase 2B-3 in-place Codex↔Claude orchestrator
  switch are implemented with API/CLI/desktop surfaces.

**Next:** record the verified MVP/static/API and full frontend gate as
complete, and keep the full repository suite explicitly non-green while
the known fake/kilocode/opencode wall-clock trio fails. Worker/orchestrator live
acceptance is already complete on `166e9e63`. Vendor detection, automatic
failover, and Claude read-only are post-MVP; no capability is promoted by this
close-out.

## Remotes

```bash
git remote -v
# origin   https://github.com/maplenk/agent-orchestrator-roles.git
# upstream https://github.com/Untrivial-ai/agent-orchestrator.git
git fetch upstream
```
