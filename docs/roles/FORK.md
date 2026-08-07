# AO multi-sub roles fork

Local working tree: `/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles`

## Baseline

| Field | Value |
|-------|--------|
| Upstream | `Untrivial-ai/agent-orchestrator` |
| Remote name | `upstream` |
| Pinned SHA | See `AO_BASELINE_SHA.txt` |
| Roles trunk | `roles/multi-sub-v1` |
| Active integration | `roles/upstream-sync-2` (accepted 2026-08-07; until it merges to the trunk) |

Pin before feature work. Own migration numbers on this fork (do not collide with upstream 0042 races from #3548 / #3386).

## Product

Intent-grade **role templates** + daemon-resolved **`ao spawn --role`** multi-subscription routing, durable switch/pause/failover, on top of AO’s UI.

Master plan (design): `MASTER_PLAN.md`
**Status + remaining execution plan:** `REMAINING_PLAN.md` (completed vs open, order, gates).
**Current integration steps:** `UPSTREAM_SYNC2_PLAN.md`.

## Delivery

**Target B** — full wishlist.
- Phase 1 **foundation** accepted: roles, CAS, canSpawn, Codex RO, capability registry, CLI roleMap, template Option A.
- Phase 1 **strict dogfood exit** remains open (see `REMAINING_PLAN.md`).
- Claude `read_only_enforced=false` (honest); RO orch/reviewer use **Codex**.
- Phase 2A is accepted; Phase 2B-0/1/2 and the Phase 3A pause/UI boundary landed.
**Next:** merge the accepted Sync 2 branch to the roles trunk, then implement a vendor-backed Phase 3A-2b detector. Phase 2B-3 remains blocked on Claude read-only.

## Remotes

```bash
git remote -v
# origin   https://github.com/maplenk/agent-orchestrator-roles.git
# upstream https://github.com/Untrivial-ai/agent-orchestrator.git
git fetch upstream
```
