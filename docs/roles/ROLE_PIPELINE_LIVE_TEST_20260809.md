# Role Pipeline Installed-App Replay — 2026-08-09

## Status

**PASS for the Claude native-session collision fix, default starter roles, and
the installed-app Claude→Codex→Claude Switch replay.**

The real packaged Electron app was rebuilt with fix commit `15a4e6f0`, ad-hoc deep-signed,
installed at `/Applications/Agent Orchestrator.app`, and run against the default
`~/.ao/data` / port `3001` path. This is the same local app shape the user runs;
it is not a published or notarized release. The installed bundle reports version
`0.10.3`. The final default-role build from `762ae160` has embedded daemon
SHA-256 `058cc4a82805fb7b96fef0e553620395065584b78f7c56f35dac9833ffe966f1`.

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
- Full backend on the default-role close-out: 4,702 passed; only the known
  untouched aggregate-load timing trio in fake/Kilocode/OpenCode failed, and
  each passed 20/20 in isolation.
- Affected race gate: 1,514 passed across 14 packages.
- `go build ./...`, `go vet ./...`, `gofmt -l .`, cold-cache
  `golangci-lint` v2.12.2, frontend typecheck, and `git diff --check`: clean.
- Full frontend Vitest: 151/151 files and 2,042/2,042 tests passed.
- Packaged Electron build completed under Node 22.14.0; the installed bundle
  passes `codesign --verify --deep --strict` after local ad-hoc signing.

## Installed-app replay

The installed app spawned diagnostic orchestrator `qbapi-2` through its embedded
daemon and real default data directory. AO reserved native Claude id
`9121924d-939b-4796-a51f-22e68ee347d6`, which differs from the collided legacy id
`c9f8f584-9bd7-5081-8ce5-dcfec925c88c`. The durable row, Claude process argv,
tmux pane, and native Electron terminal all agreed on the new identity; Claude
reached its input composer without a collision.

That first replay also exposed a product gap: the native UI rendered Switch but
disabled it with `This orchestrator has no pinned role.` The rebuilt `qbapi`
project had no authored role map after database cleanup, so the MVP switch
surface existed but an ordinary existing repository could not use it without
manual API/CLI configuration.

The failed `qbapi-1` and diagnostic `qbapi-2` sessions were then killed and
cleaned through AO. Their tmux runtimes are gone; the durable terminated history
rows remain as expected.

## Default starter-role fix and final native replay

Commit `762ae160` adds a persisted non-strict starter role map for every new or
active existing project whose role map is zero. The map contains orchestrator,
implementor, UI, reviewer, and verifier roles; preserves configured project
harness/model preferences; and gives a Claude or Codex orchestrator the other
harness as its manual switch target. Non-strict preserves legacy worker CLI
behavior.

On launch of the newly packaged `/Applications` app, the real `qbapi` row was
upgraded from schema version 0/no roles to schema version 1 with
`orchestratorRole=orchestrator`. Its existing Pi worker preference remained Pi;
the orchestrator primary remained Claude Code and gained Codex as the exact
alternate. The already-running `qbapi-3` row was intentionally still unpinned,
but its read model immediately changed from `no_role_pin` to
`available:true`, role `orchestrator`, target Codex.

The native Electron window showed an enabled Switch menu with
`Codex · Provider default`. Selecting it completed in place on the same AO
session id. The saga adopted a complete durable role pin (schema version, map
SHA, template artifact/SHA, writable permission, and spawn permission) and
wrote exactly `requested → pre_stop → post_stop → target_ack`. The return menu
then showed `Claude Code · Provider default`; selecting it completed a second
four-phase generation. Final facts:

- harness returned to Claude Code;
- role id remained `orchestrator` with the same template artifact and map SHA;
- the pending fence was empty;
- exactly one `qbapi-3` tmux runtime remained; and
- Switch remained enabled with Codex as the target.

This verifies both compatibility paths: fresh desktop-created orchestrators
auto-bind the starter orchestrator role, while an already-live provider-default
legacy orchestrator adopts it only during an explicit, authorized Switch.

## Non-issues

- This is not model reasoning or prompt behavior.
- It is not a concurrent-session conflict in Claude.
- Provider availability in the installed app was not the cause; Claude launched
  far enough to reject the reused native UUID.
