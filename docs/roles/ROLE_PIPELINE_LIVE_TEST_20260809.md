# Role Pipeline Installed-App Replay — 2026-08-09

## Status

**In progress. Do not treat this run as a clean PASS yet.**

The real packaged Electron app was rebuilt from `ef91d6b4`, ad-hoc deep-signed,
installed at `/Applications/Agent Orchestrator.app`, and run against the default
`~/.ao/data` / port `3001` path. This is the same local app shape the user runs;
it is not a published or notarized release.

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

The failed row and pane were left intact during diagnosis. Cleanup and an
installed-app replay are required after the fix is gated.

## Non-issues

- This is not model reasoning or prompt behavior.
- It is not a concurrent-session conflict in Claude.
- Provider availability in the installed app was not the cause; Claude launched
  far enough to reject the reused native UUID.
