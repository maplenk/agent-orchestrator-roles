# Role Pipeline Installed-App Replay — 2026-08-09

## Status

**PASS for the Claude native-session collision fix and installed-app replay.**

The real packaged Electron app was rebuilt with fix commit `15a4e6f0`, ad-hoc deep-signed,
installed at `/Applications/Agent Orchestrator.app`, and run against the default
`~/.ao/data` / port `3001` path. This is the same local app shape the user runs;
it is not a published or notarized release. The installed bundle reports version
`0.10.3`; its embedded daemon SHA-256 is
`6c20701411820ee8a70b032ac327018f9610d6da8279ded914461b2e13ae5df6`.

## AO issue found

### Claude native session id collided after the AO database was replaced

Spawning `qbapi-1` failed before Claude reached its input prompt:

> `Session ID c9f8f584-9bd7-5081-8ce5-dcfec925c88c is already in use.`

The UUID was not present in the new AO database and no concurrent Claude process
owned it. It was a Claude transcript created on 2026-07-27 for the same canonical
workspace and still present under `~/.claude/projects`. AO had generated the
native UUID deterministically from the reusable display id `qbapi-1`; replacing
the AO database reset the project counter and regenerated the same UUID.

Required contract:

1. A new AO TUI session incarnation reserves a provider-native id before process
   launch and stores it durably on the session row.
2. Reusing an AO display id after database replacement must not reuse the old
   provider-native id.
3. Restart/recovery of the same durable row must retain its reserved id.
4. Cross-harness switch and same-harness Fresh must reserve a fresh target id and
   must not fall through to native resume.
5. Pre-MVP rows may retain the deterministic fallback for compatibility.

## Fix and gates

Commit `15a4e6f0` makes a fresh AO TUI incarnation allocate and durably persist a
provider-native id before launching Claude. Restarts keep that id; cross-harness
switches and same-harness Fresh force a new target identity. The deterministic
Claude id remains only as a compatibility fallback for legacy callers that do
not provide a durable id.

Verification:

- Focused identity regressions: 5 passed.
- Full affected normal and race suites: 642 passed each.
- Full backend: 4,695 passed; only the two known untouched three-second auth
  timing failures in Kilocode and OpenCode occurred, and both passed 20/20 in
  isolation.
- `go build ./...`, `go vet ./...`, `gofmt -l .`, cold-cache
  `golangci-lint` v2.12.2, frontend typecheck, and `git diff --check`: clean.
- Packaged Electron build completed under Node 22.14.0; the installed bundle
  passes `codesign --verify --deep --strict` after local ad-hoc signing.

## Installed-app replay

The installed app spawned diagnostic orchestrator `qbapi-2` through its embedded
daemon and real default data directory. AO reserved native Claude id
`9121924d-939b-4796-a51f-22e68ee347d6`, which differs from the collided legacy id
`c9f8f584-9bd7-5081-8ce5-dcfec925c88c`. The durable row, Claude process argv,
tmux pane, and native Electron terminal all agreed on the new identity; Claude
reached its input composer without a collision.

The native UI also rendered Switch and Fresh Conversation. Switch was disabled
with the explicit reason `This orchestrator has no pinned role.` This was an
accurate setup result: the rebuilt `qbapi` project has no authored role bindings
after the database cleanup, so there is no authorized target to advertise. It is
not a switch-surface regression; a switchable installed-app replay must create
the orchestrator through a configured semantic role.

The failed `qbapi-1` and diagnostic `qbapi-2` sessions were then killed and
cleaned through AO. Their tmux runtimes are gone; the durable terminated history
rows remain as expected.

## Non-issues

- This is not model reasoning or prompt behavior.
- It is not a concurrent-session conflict in Claude.
- Provider availability in the installed app was not the cause; Claude launched
  far enough to reject the reused native UUID.
