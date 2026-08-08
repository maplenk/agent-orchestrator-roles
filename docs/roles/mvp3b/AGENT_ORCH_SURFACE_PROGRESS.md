# Orchestrator surface progress

**Agent:** surface slice  
**Branch:** `codex/mvp-orch-surface`  
**Base:** `be4321d1`

## Contract decisions

- Reuse the existing `POST /sessions/{id}/switch` and
  `/fresh-conversation` operations and the existing CLI commands.
- Add a curated orchestrator-only session read block. It carries exact
  host-authorized role-map targets and safe pending-generation facts; it never
  exposes the persisted handoff payload.
- Ordinary workers take the existing read path without any project lookup.
- An unreadable orchestrator preview degrades to an explicit unavailable block
  and never fails the session list or offers a target.
- Switch/fresh mutation responses remain independent of preview recomputation.
- Place Switch and Fresh Conversation in the orchestrator session topbar. The
  operations remain distinct from worker Resume, Restart Agent, and Continue.
- Backend authorization remains authoritative. The desktop can submit only an
  exact target received in the read model; the service re-authorizes it.

## Core dependency

Integrate core commit `3c7783e` (after strict-policy commit `10cfcecf`). It adds
`Manager.SwitchOrchestrator`, which holds the project ownership gate before the
session switch fence and re-authorizes the exact target under that gate. This
surface only dispatches to that entry point and maps its typed errors; it does
not alter session-manager/domain strict-policy or runtime code.

## Checkpoints

- [x] Existing API, CLI, read model, and desktop placement mapped.
- [x] Curated switch read model implemented and tested.
- [x] Desktop topbar controls, pending state, errors, and input fencing tested.
- [x] Eight locales and generated API artifacts updated.
- [x] Frontend and affected backend/spec gates green.

## Verification

- Backend session/controller/spec packages: 557 tests passed across four packages.
- Full HTTP/API gate: 381 tests passed across six packages.
- Frontend focused lifecycle surface: 120 tests passed across four files.
- Full frontend Vitest suite passed.
- Frontend TypeScript typecheck passed.
