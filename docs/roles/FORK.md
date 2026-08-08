# AO multi-sub roles fork

Local working tree: `/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles`

## Baseline

| Field | Value |
|-------|--------|
| Upstream | `Untrivial-ai/agent-orchestrator` |
| Remote name | `upstream` |
| Pinned SHA | See `AO_BASELINE_SHA.txt` |
| Roles trunk | `roles/multi-sub-v1` |
| Active integration | Final roles MVP integrated at `051db38b`; clean gate and live acceptance pending |

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

**Next:** run the clean final gate and worker/orchestrator live acceptance on
one exact SHA. Vendor detection, automatic failover, and Claude read-only are
post-MVP; nothing is promoted by this close-out.

## Remotes

```bash
git remote -v
# origin   https://github.com/maplenk/agent-orchestrator-roles.git
# upstream https://github.com/Untrivial-ai/agent-orchestrator.git
git fetch upstream
```
