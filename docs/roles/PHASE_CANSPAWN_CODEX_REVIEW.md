# Phase canSpawn — operator authority transport (Codex re-review)

## Honest boundary

**Host-enforced isolation against same-UID agents is not claimed.** A worker that
can run arbitrary code as the desktop user can still inspect the filesystem
(including `running.json`). This phase hardens **application transport** so:

1. The official CLI cannot auto-upgrade a managed session to operator by unsetting only `AO_SESSION_ID`.
2. Operator secrets are not ambient daemon env inherited by tmux/ConPTY children.
3. Desktop and LAN-mobile spawn work via trusted channels.
4. External CLI with custom `AO_DATA_DIR` still gets the runfile operator token.

Cooperative policy for in-session agents: present session capability;
`canSpawn:false` and terminated sessions fail closed.

## Transport model

| Caller | Trust | Credential |
|--------|--------|------------|
| Desktop renderer | Operator | `X-AO-Operator-Spawn-Token` on both same-URL and rebased fetch paths |
| Mobile LAN | Operator | Password middleware sets `authctx` LAN-authenticated |
| CLI external shell | Operator | `AO_OPERATOR_SPAWN_TOKEN` env **or** runfile when **no** managed-session markers |
| CLI managed session | Agent | Markers: `AO_MANAGED_SESSION` and/or `AO_SESSION_ID` and/or `AO_SPAWN_CAPABILITY` → agent headers only |
| Headerless loopback HTTP | Denied | `SPAWN_AUTH_REQUIRED` |

**Managed-session markers** (injected into agent runtimes only):

- `AO_MANAGED_SESSION=1` (dedicated; not user config)
- `AO_SESSION_ID`
- `AO_SPAWN_CAPABILITY`

**Not a session marker:** `AO_DATA_DIR` (documented external CLI config for custom data dirs).

## CLI rules (`spawnCallerHeaders`)

1. If any managed-session marker is set → agent path only (no runfile operator). Empty session id → no headers (403), not operator upgrade.
2. Else → external: env operator token, else **load operator token from `running.json`**.

## Residual (documented, not “closed”)

Same-UID agent that unsets **all** managed markers and reads `running.json` can forge operator headers. Outside host-enforced isolation without OS sandboxing / separate identity / privileged IPC.

## Tests

```bash
export PATH="/opt/homebrew/opt/go/bin:/opt/homebrew/bin:$PATH"
cd backend && go test ./internal/cli/ ./internal/session_manager/ \
  ./internal/httpd/controllers/ ./internal/service/spawncred/ -count=1 \
  -run 'SpawnCaller|RuntimeEnv|Spawn_|Managed'
```

- External shell with only `AO_DATA_DIR` → runfile operator token  
- Managed session with `AO_MANAGED_SESSION` after unsetting only `AO_SESSION_ID` → no operator  

## Ask Codex

1. Accept canSpawn transport with `AO_MANAGED_SESSION` marker?  
2. Proceed to read-only adapters after accept?
