# Final installed-app role-pipeline acceptance — 2026-08-08

## Result

**PASS.** The real installed Agent Orchestrator app completed the normal strict
role pipeline against its default data directory:

`one-sentence human brief -> Claude orchestrator -> Grok implementor -> Codex verifier -> orchestrator verdict`

The first full run exposed one AO ownership defect at the final boundary: a
terminal-only verifier report did not wake the idle orchestrator. That run
required one host nudge and therefore was not accepted as a clean pass. The
system-prompt contract was hardened in `f4b28012`, the installed app was
replaced, and a fresh smoke proved that the orchestrator retained ownership,
polled durable verifier state, retrieved the terminal report itself, and gave
the verdict without any host message.

## Environment

- Branch: `roles/multi-sub-v1`
- Full role-chain implementation: `2a88007d`
- Final report-ownership fix and installed smoke: `f4b28012`
- Installed app: `/Applications/Agent Orchestrator.app`
- Embedded version: `0.10.3` (local ad-hoc test package, not a published release)
- Embedded daemon SHA-256: `225babaca65c9c1b32e2cf9296575570a36c9836a6a511423d3d9d675bf77a08`
- Daemon: installed app, `127.0.0.1:3001`, default `~/.ao/data`
- Disposable fixture: `/private/tmp/ao-pelican-mvp-final-20260808/fixture`
- Fixture baseline: `337a7c55101bea3f7f1931179d41534ae561eff1`
- Initial implementation: `ad3806a`
- Accepted fixture fix: `0e3faad`
- Full-chain project: `pelican-mvp-final`
- No-nudge report smoke project: `pelican-report-smoke`

The actual app the user would run was replaced only after the code gate. The
prior installed app is recoverable at
`/private/tmp/ao-installed-app-backup-20260808-f4b28012-previous/Agent Orchestrator.app`.

## Full role chain

The human sent only:

> Build a tiny Pelican Pedal browser game. Coordinate implementation and verification.

The host-expanded strict roles were:

| Role | Session | Harness / effective model | Prompt bytes | Result |
|---|---|---|---:|---|
| orchestrator | `pelican-mvp-final-1` | Claude Code / Opus 5, xhigh | 0 | Coordinated without implementing |
| implementor | `pelican-mvp-final-2` | Grok / Grok 4.5, high | 2,853 | Implemented and committed normally |
| verifier | `pelican-mvp-final-3` | Codex / `gpt-5.6-sol`, xhigh, read-only | 3,600 | Produced an evidence-backed terminal report |

All sessions retained the same durable role-map SHA
`8328b7...`; no free-form harness substitution occurred.

### Provider and spawn path

- The native provider dialog refreshed automatically and showed Claude, Codex,
  and Grok as installed.
- `ao doctor` included Grok.
- Grok launched with the host-owned trust mode and received the assignment at
  its normal input prompt. There was no welcome/trust-screen paste and no
  manual approval.
- The natural verifier brief fit the 4,096-byte transport ceiling and launched
  in the normal role spawn.
- The read-only verifier left its worktree clean and communicated through its
  terminal report; the host-side `ao session output` boundary made that report
  retrievable without asking it to write a file.

### Real UI evidence

The installed app's actual Browser inspector rendered the fixture, not a mock
or external browser. The run visibly proved:

- input started the game;
- score increased to `1` after clearing a pipe;
- collision reached game-over while retaining score `1`;
- restart returned to score `0` and the start prompt; and
- the narrow layout remained usable in the native Browser panel.

The fixture test suite passed `3/3`. The Grok worktree was clean and contained
only the contracted product files. The final fixture branch contained
`0e3faad`, which fixed duplicate canvas activation and bounded animation-frame
catch-up without modifying the fixed tests.

## Final report-ownership proof

The full chain exposed a real AO gap: after the verifier became idle, the
orchestrator had previously yielded and needed a host message before reading
the report. The runtime prompt and shipped orchestrator profile disagreed about
who retained responsibility for terminal-only reviewers/verifiers.

`f4b28012` makes that responsibility explicit. A replacement-app smoke then
used a new strict project with Claude orchestrator `pelican-report-smoke-1` and
read-only Codex verifier `pelican-report-smoke-2`:

1. Claude spawned the verifier itself.
2. Claude kept a bounded ten-second durable-state poll active.
3. Codex finished and became idle with its terminal report.
4. Claude detected the idle state without any user or host message.
5. Claude ran `ao session output pelican-report-smoke-2`, reconciled the report
   with committed Git evidence, and emitted the final verdict.

Both sessions ended idle. No code was changed during this smoke. This is the
load-bearing evidence that the fix is behavioral rather than only a prompt
snapshot test.

## Issue classification

| Classification | Finding | Resolution |
|---|---|---|
| AO — fixed | Fresh Grok worktrees stopped on the trust screen. | Grok launch and restore now use its supported trust mode; the normal full run required no approval. |
| AO — fixed | Grok was absent from `ao doctor`. | Grok is now probed. |
| AO — fixed | The native provider sheet opened with stale availability. | The sheet refreshes on open. |
| AO — fixed | Natural role prompts could exceed the transport budget. | The 4,096-byte budget is host-visible and prompts are bounded; the normal verifier spawn used 3,600 bytes. |
| AO — fixed | A read-only terminal report had no host retrieval path. | `ao session output` is available through manager, service, HTTP, OpenAPI, and CLI. |
| AO — fixed | The orchestrator yielded after a terminal-only verifier and needed a manual wake-up. | `f4b28012` retains ownership and requires bounded durable-state polling plus output retrieval. Replacement-app smoke passed with zero nudges. |
| Local packaging | The locally packaged app required a final ad-hoc deep signature before macOS accepted the replaced bundle. | The installed bundle now passes `codesign --verify --deep --strict`. This is a local packaging-loop issue; published release signing/notarization remains a separate gate. |
| Accepted limitation | The read-only Codex verifier could not connect directly to AO's loopback Browser surface. | Host-side installed UI play supplied the live evidence; the verifier remained technically read-only and performed static/test verification. A host-mediated verifier Browser bridge is post-MVP. |

The following were deliberately **not** classified as AO defects:

- model reasoning, phrasing, or tool-selection behavior;
- Codex asking separately for MCP tool approvals;
- the disposable game's own implementation bugs; and
- fixture/project setup mistakes corrected before the accepted run.

System-prompt hardening was used only where AO needed a precise ownership or
transport contract.

## Code and test gate

On final code `f4b28012`:

- prompt ownership regressions pass;
- full `internal/session_manager` passes normally and under `-race`;
- gofmt, `go vet ./...`, pinned golangci-lint v2.12.2, and diff-check pass;
- frontend typecheck and the full frontend suite remained green at
  `2042/2042` for the installed full-chain implementation; and
- OpenAPI generation is stable.

The ordinary full backend run remains honestly red only on the same untouched
aggregate-load wall-clock trio in fake, Kilocode, and OpenCode. Each exact test
passes `20/20` in isolation on final code. No MVP package failure or data race
was found.

## Cleanup

After evidence capture:

- both projects' sessions were killed and removed through AO;
- both project registrations and empty AO worktree directories were removed;
- no Pelican tmux runtime, active AO data path, or Grok trust entry remains;
- the installed app and daemon were stopped and port `3001` was closed;
- the active `~/.ao/data/ao.db` was removed from the live install and preserved
  at `/private/tmp/ao-pelican-mvp-final-db-20260808-f4b28012/ao.db`; and
- disposable fixture, provider-session, lint, and configuration artifacts were
  moved to `/private/tmp/ao-pelican-files-backup-20260808-f4b28012`.

The backups make cleanup reversible while leaving the installed AO data path
empty of Pelican state.
