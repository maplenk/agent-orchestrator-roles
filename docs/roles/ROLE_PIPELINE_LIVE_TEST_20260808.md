# Role Pipeline Live Test — 2026-08-08

## Result

**The installed-app pipeline completed only with manual interventions and did not pass.**

The role pins held throughout:

| Role | Session | Harness / effective model | Outcome |
|---|---|---|---|
| orchestrator | `fixture-2` | Claude Code / Opus 5, xhigh | coordinated, inspected implementation evidence, and spawned verification |
| implementor | `fixture-5` | Grok / Grok 4.5, high | implemented, live-tested, committed, and left a clean tree |
| verifier | `fixture-6` | Codex / `gpt-5.6-sol`, xhigh | independently verified read-only; **not approved** |

The normal path failed because fresh AO worker worktrees were not trusted by Grok. After
the user authorized two scoped trust entries, the same pinned role succeeded. The Codex
verifier then completed, but AO exposed no durable verifier report to the orchestrator;
its terminal verdict had to be captured by the host. That manual relay was not completed
before cleanup and would not have converted the run to PASS.

## Environment

- AO checkout branch: `roles/multi-sub-v1`
- Installed-app build commit: `0466cf98`
- Embedded app version: `0.10.3` (local ad-hoc test package, not a signed release)
- Daemon: installed app, `127.0.0.1:3001`, default `~/.ao/data`
- Fixture: `/private/tmp/ao-pelican-installed-20260808/fixture`
- Fixture baseline: `923270048eea76c74947b75e0e3197794230a991`
- Implementation commit: `09321f2`
- Project id: `fixture`
- Strict role map: Claude orchestrator, Grok implementor, read-only Codex verifier;
  manual failover with no orchestrator alternate rung

The installed app initially failed against the pre-existing real database because the
original fork migration history used versions 42–49 without corresponding 9000-series
ledger rows. That database was preserved under
`/private/tmp/ao-real-db-backup-20260808-1944`; the migration repair is commit
`0466cf98`. The UI run used a fresh database.

## What worked

- The real packaged Electron app, embedded daemon, default data directory, default tmux
  socket, and native project-import UI were used.
- Refreshing the project-agent cache made the globally installed and authorized Claude,
  Grok, and Codex providers selectable.
- The strict role-pinned orchestrator expanded the required one-sentence human brief,
  inspected the fixture, and did not implement the game itself.
- After folder trust was added, Grok received the complete bounded implementor brief in
  the normal role spawn and worked in its AO worktree.
- Grok committed exactly `index.html`, `styles.css`, and `game.js`; the worktree was clean.
- Independent host execution reported 4 tests, 4 passed, 0 failed.
- The installed Browser inspector rendered the game at narrow width. Grok exercised the
  real preview and confirmed input, scoring, game-over, and restart after debugging the
  initial implementation.
- The Codex verifier stayed read-only, reran the tests, checked scope and cleanliness,
  and produced an evidence-backed `not approved` verdict.

## Verifier result

The disposable game itself was not approved:

- criteria 1, 2, 3, 5, 6, and 8 passed;
- criterion 4 failed because rendered sand and pipe-cap geometry disagreed with logical
  collision hitboxes;
- criterion 7 failed because `createInitialState()` and `restart()` used `Math.random()`,
  contradicting the fixture's pure/deterministic API contract;
- a real preview from the verifier remained unavailable because its sandbox could not
  connect to AO's loopback daemon.

These are fixture findings proving the verifier did useful independent work. They are
not AO product defects and the disposable fixture is not being repaired.

## AO issues recorded

| Priority | Issue | Evidence / required direction |
|---|---|---|
| P1 — fixed | Original 42–49 fork migration history made the replaced app fail with duplicate `role_id`. | Repaired in `0466cf98`; real database copy migrated through 9008 with `integrity_check=ok`. |
| P1 | Grok fresh-worktree trust is not prepared by AO. | `fixture-3` and `fixture-4` terminated in about three seconds before task delivery. Adding exact trust entries for AO's worktree root and the disposable fixture made `fixture-5` launch. AO must prepare its own managed worktree safely or expose an explicit host-owned approval step. |
| P1 | Verifier result has no durable return channel to the orchestrator. | `fixture-6` finished read-only with a detailed verdict, but `fixture-2` could see only idle state. Requiring a read-only verifier to commit a report is invalid. The host needs a durable final-result artifact/message that does not require workspace writes. |
| P1 | Read-only verifier cannot exercise AO's live Browser surface. | `ao preview index.html` failed with `dial tcp 127.0.0.1:3001: connect: operation not permitted`. Provide a host-mediated preview/browser operation that preserves workspace read-only enforcement. |
| P2 | Project-agent availability opens with stale `Needs install` labels. | The daemon reported Claude/Grok/Codex installed and authorized. The dialog initially disagreed and blocked selection; pressing **Refresh agents** corrected every relevant entry. |
| P2 | Natural orchestrator/verifier briefs can exceed the spawn-prompt ceiling. | The first verifier brief was refused and Claude had to condense it. Keep the typed refusal, but make the host/system prompt budget explicit and provide a file/artifact-backed assignment path. |
| P2 | Codex MCP approval labelled “for this session” is tool-granular. | `search_graph` and `get_code_snippet` each interrupted the verifier separately. Review managed MCP approval policy; do not weaken it silently. |
| P2 | `ao doctor` does not probe Grok although the harness is supported and globally authorized. | Both the orchestrator and host confirmed Grok in PATH while doctor omitted it. |
| Build tooling | Electron Forge packaging under Node 26 stalled during finalization; Node 22 completed normally. | Pin or enforce the supported packaging Node version. |

Model judgment, phrasing, and implementation choices are deliberately not classified as
AO bugs. System-prompt hardening is warranted only where a host contract must be explicit,
such as prohibiting write-based report workarounds for read-only roles and keeping role
briefs within the transport budget.

## Cleanup

The user requested complete Pelican cleanup after evidence capture.

- `fixture-2`, `fixture-5`, and `fixture-6` were terminated; all six Pelican sessions
  were passed through AO cleanup; project `fixture` was unregistered.
- No `fixture-*` tmux runtime, AO worktree, prompt directory, or project registration
  remains.
- The two Grok trust entries added during diagnosis were removed.
- `/private/tmp/ao-pelican-installed-20260808` and the older
  `/private/tmp/ao-pelican-ui-20260808.xOGidh` test root were deleted.
- The installed app and daemon were stopped; `~/.ao/running.json` is absent.
- The fresh test database was removed from active AO state and preserved recoverably at
  `/private/tmp/ao-pelican-db-cleanup-20260808/ao.db`. No active
  `~/.ao/data/ao.db` remains.
- The pre-MVP database backups under `/private/tmp/ao-real-db-backup-20260808-1944`
  were deliberately left untouched.
