# Phase canSpawn — session-scoped spawn credentials (Codex re-review)

## Verdict history

1. First slice rejected: optional headers, global HMAC key in `AO_DATA_DIR`, no terminated check.
2. **This redesign** addresses the three P1 privilege-escalation paths.

## Security model

| Path | Credential | Source |
|------|------------|--------|
| Operator / desktop / CLI outside session | `X-AO-Operator-Spawn-Token` | Daemon-launch secret in `running.json` only — **never** session env |
| Agent session | `X-AO-Caller-Session-Id` + `X-AO-Spawn-Capability` | Random per-session token; **only SHA-256 hash** on session row |
| Headerless | — | **403 SPAWN_AUTH_REQUIRED** (no operator fallthrough) |

Additional agent checks after valid capability:

- Terminated caller → **403 SPAWN_SESSION_TERMINATED**
- Role pin with `canSpawn=false` → **403 SPAWN_FORBIDDEN**
- Capability rotated on restore/relaunch (old token hash rewritten)

## What closed the P1s

1. **Worker omit headers** — headerless is denied. Operator requires distinct runfile token not injected as `AO_*` into sessions.
2. **Global minting key in data dir** — removed. Session tokens are `crypto/rand`; only hash stored (`spawn_capability_hash`, migration 0043). No `spawn-capability.key`.
3. **Terminated retain authority** — gate rejects `IsTerminated` before role/canSpawn.

## Residual risk (honest)

Same OS user can still read `~/.ao/running.json` if they know the path (not injected). Full process isolation needs UID separation; this redesign closes **application-level** escalate paths Codex listed.

LAN mobile: password middleware authenticates the client; spawn still needs operator token (mobile client should use runfile/desktop-issued operator context — follow-up if mobile spawn is required).

## Tests

```bash
export PATH="/opt/homebrew/opt/go/bin:/opt/homebrew/bin:$PATH"
cd backend && go test ./internal/service/spawncred/ ./internal/httpd/controllers/ \
  ./internal/session_manager/ ./internal/cli/ ./internal/daemon/ \
  ./internal/storage/sqlite/store/ -count=1
```

Includes: headerless reject; operator allow/deny; agent canSpawn true/false; terminated reject; bad capability.

## Ask Codex

1. Accept redesigned canSpawn boundary for app-level enforcement?
2. Proceed to read-only adapters next?
