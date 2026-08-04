# Phase canSpawn — session-scoped spawn credentials (Codex review)

## Status

Migration 0042 durability closed. This slice lands **host canSpawn enforcement**
via session-scoped credentials (not AO_SESSION_ID alone).

## Design

| Actor | Headers | Outcome |
|-------|---------|---------|
| Operator / desktop | none | allowed (loopback) |
| Agent with `AO_SESSION_ID` + valid `AO_SPAWN_CAPABILITY` | both | allowed if role has no pin **or** `canSpawn=true` |
| Agent with session id, bad/missing capability | caller id only | **403 SPAWN_CAPABILITY_INVALID** |
| Role pin `canSpawn=false` + valid capability | both | **403 SPAWN_FORBIDDEN** |

Token: HMAC-SHA256 over `ao-spawn-v1:<sessionID>` with daemon key
`~/.ao/data/spawn-capability.key` (same shape as browser capability, distinct MAC prefix).

## Landed

| Piece | Path |
|-------|------|
| Authority | `backend/internal/service/spawncred/` |
| Env inject | `AO_SPAWN_CAPABILITY` in `session_manager.runtimeEnv` |
| HTTP gate | `authorizeCallerSpawn` on POST `/sessions` and `/orchestrators` |
| CLI | `ao spawn` sends `X-AO-Caller-Session-Id` + `X-AO-Spawn-Capability` when env set |
| Daemon wire | LoadAuthority + Session Manager + APIDeps |

## Tests

```bash
export PATH="/opt/homebrew/opt/go/bin:/opt/homebrew/bin:$PATH"
cd backend && go test ./internal/service/spawncred/ ./internal/httpd/controllers/ \
  ./internal/session_manager/ ./internal/daemon/ -count=1
```

Key cases: operator allow; invalid cap; canSpawn false forbid; canSpawn true allow; legacy role-less allow with valid cap.

## Still open

- Adapter **read_only** launch (`workspaceWrites:false`)
- Capability matrix at config-save
- Strict operational dogfood only after read-only + canSpawn both land

## Ask Codex

1. Accept canSpawn credential gate?
2. Is operator-without-headers still correct on loopback (incl. LAN mobile)?
3. Proceed to read-only adapters next?
