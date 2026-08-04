# Phase canSpawn — operator authority transport (Codex re-review)

## Honest boundary

**Host-enforced isolation against same-UID agents is not claimed.** A worker that
can run arbitrary code as the desktop user can still inspect the filesystem
(including `running.json`). This phase hardens **application transport** so:

1. The official CLI cannot auto-upgrade a session to operator by unsetting env.
2. Operator secrets are not ambient daemon env inherited by tmux/ConPTY children.
3. Desktop and LAN-mobile spawn work again via trusted channels.

Cooperative policy for in-session agents remains: present session capability;
`canSpawn:false` and terminated sessions fail closed.

## Transport model

| Caller | Trust | Credential |
|--------|--------|------------|
| Desktop renderer | Operator | `X-AO-Operator-Spawn-Token` on **both** same-URL and rebased fetch paths (default port 3001 fixed) |
| Mobile LAN | Operator | Password middleware sets `authctx` LAN-authenticated; **no** operator header |
| CLI external shell | Operator | `AO_OPERATOR_SPAWN_TOKEN` env **or** runfile when **no** session markers (`AO_SESSION_ID` / `AO_SPAWN_CAPABILITY` / `AO_DATA_DIR`) |
| CLI / agent session-adjacent | Agent | Session markers present → agent headers only; never runfile operator upgrade |
| Headerless loopback HTTP | Denied | `SPAWN_AUTH_REQUIRED` |

## P1 fixes this slice

1. **CLI unset-env escalation** — `spawnCallerHeaders` never loads operator token from `running.json`. Unset `AO_SESSION_ID` without `AO_OPERATOR_SPAWN_TOKEN` → no headers → 403.
2. **Env inheritance** — daemon does **not** `os.Setenv` operator token; `runtimeEnv` clears `AO_OPERATOR_SPAWN_TOKEN` and `AO_BROWSER_RUNTIME_TOKEN`.
3. **Desktop spawn** — `parseRunFile` keeps `operatorSpawnToken`; main attaches it on ready status; renderer `runtimeFetch` sets header on POST sessions/orchestrators.
4. **Mobile spawn** — LAN auth → `authctx.WithLANAuthenticated` → authorizeCallerSpawn allows.

## Residual (documented, not “closed”)

Same-UID agent that **reads** `running.json` and forges operator headers is outside this boundary without OS sandboxing / separate identity / privileged IPC with approval.

## Tests

```bash
export PATH="/opt/homebrew/opt/go/bin:/opt/homebrew/bin:$PATH"
cd backend && go test ./internal/service/spawncred/ ./internal/httpd/... \
  ./internal/session_manager/ ./internal/cli/ ./internal/daemon/ \
  ./internal/authctx/ ./internal/storage/sqlite/store/ -count=1
```

Frontend: `daemon-discovery` parseRunFile includes operatorSpawnToken.

## Ask Codex

1. Accept app-level transport with explicit cooperative same-UID residual?
2. Ready for read-only adapters only after this accept?
