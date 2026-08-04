# AO multi-sub roles fork

Local working tree: `/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles`

## Baseline

| Field | Value |
|-------|--------|
| Upstream | `Untrivial-ai/agent-orchestrator` |
| Remote name | `upstream` |
| Pinned SHA | See `AO_BASELINE_SHA.txt` |
| Working branch | `roles/multi-sub-v1` |

Pin before feature work. Own migration numbers on this fork (do not collide with upstream 0042 races from #3548 / #3386).

## Product

Intent-grade **role templates** + daemon-resolved **`ao spawn --role`** multi-subscription routing, durable switch/pause/failover, on top of AO’s UI.

Master plan (session):  
`~/.grok/sessions/.../plan.md` — also summarized in `MASTER_PLAN.md` in this directory.

## Delivery

**Target B** — ~4–6 weeks (full wishlist). Start at Phase 0 (this pin + capability matrix).

## Remotes

```bash
# Add your GitHub fork when ready:
# git remote add origin git@github.com:maplenk/<repo>.git
git fetch upstream
```
