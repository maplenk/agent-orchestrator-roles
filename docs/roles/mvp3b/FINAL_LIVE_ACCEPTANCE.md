# Final MVP live acceptance

**Status:** PASS

**Frozen code and runner:**
`166e9e6338d0230b1a9c9f5c4976cf79789df188` (`166e9e63`)

**Executed:** 2026-08-08

This is the promoted live result for the final manual-failover and
orchestrator-switch MVP. The complete matrix ran sequentially from fresh
isolated roots against one immutable detached checkout. The acceptance runner
was not edited during evidence collection.

## Isolation and evidence integrity

- Every scenario used an explicit root under `/tmp`, an exact loopback port,
  root-scoped `HOME`, `AO_DATA_DIR`, `AO_RUN_FILE`, provider fixtures, and a
  data-directory tmux namespace. The default `~/.ao` was never read or written.
- The harness self-test, shell syntax check, capability probe, isolation guard,
  scoped runtime-termination negative checks, delay bound, and sanitized
  worker-address negative check passed before the live matrix.
- Twenty sanitized JSON snapshots were captured. Every snapshot reports the
  full frozen SHA above. Record 12 has a separate successful Go capability
  probe rather than a daemon snapshot.
- Raw runtime state, daemon logs, and fixture events remain in the isolated
  roots listed below. They are not committed: repository policy excludes local
  daemon state, builds, logs, and temporary worktrees. Snapshots contain hashes
  and booleans in place of prompt or credential contents.

## Worker Continue records

| Record | Isolated root | Result |
|---|---|---|
| 1 and 3 | `/tmp/ao-mvp-final-166e9e63-r01-r03` | Paused-live Continue completed generation `e6f475f4`; exact failover/switch ledgers, one runtime, cleared pause/pending state, preserved identity/template/permissions/workspace, and rotated launch credential |
| 2 | `/tmp/ao-mvp-final-166e9e63-r02` | Scoped termination removed the sole target runtime; literal AO-namespaced server absence was verified; paused-dead Continue completed generation `881132c2` with one runtime |
| 4 | `/tmp/ao-mvp-final-166e9e63-r04` | Conclusive pre-stop failure retained the pause and live source, recorded one failed attempt, and emitted the exact failure ledgers |
| 5-7 | `/tmp/ao-mvp-final-166e9e63-r05-r07` | Record 5 stopped at `post_stop`; record 6 proved SIGKILL/restart is passive with the same durable facts; record 7 explicitly resumed the same generation `02f312af` and attempt, then acknowledged exactly one runtime |
| 8 | `/tmp/ao-mvp-final-166e9e63-r08` | Atomic `requested` seed survived the crash and re-drove fixed generation `r08-fixed-generation` at sequence 1 with one attempt and runtime |
| 9 | `/tmp/ao-mvp-final-166e9e63-r09` | A duplicate Continue inside the two-second live fence returned `SWITCH_IN_PROGRESS`; the original generation `0be12771` completed with one attempt, rung, and runtime and no stranded `post_stop` |
| 10 | `/tmp/ao-mvp-final-166e9e63-r10` | The sole rung terminal-failed before stop; the next Continue returned `FAILOVER_NO_TARGET` while retaining the same one failed attempt, pause, live source, and runtime |
| 11 | `/tmp/ao-mvp-final-166e9e63-r11` | Ten-second observation in manual mode produced zero attempts and retained the pause |
| 12 | `/tmp/ao-mvp-final-166e9e63-r12` | `TestCapabilityMatrixMVP` passed; limit detection stayed false everywhere and Claude read-only enforcement remained honestly unsupported |

## Orchestrator records

| Scenario | Isolated root | Result |
|---|---|---|
| Strict Codex -> Claude -> Codex | `/tmp/ao-mvp-final-166e9e63-orch-strict` | Generations `803876bb` and `752e2ae9` each recorded exact `requested,pre_stop,post_stop,target_ack` phases; identity/template/permissions/workspace and worker address were preserved, credentials rotated, and the project retained one active owner/runtime with one handoff and no stacking |
| Pending fences and recovery | `/tmp/ao-mvp-final-166e9e63-orch-recovery` | API input and a real mux send were both fenced while generation `917fcdef` was pending; after an injected target-create failure and daemon SIGKILL, boot recovered the same generation with exact `requested,pre_stop,post_stop,failed,target_ack` phases, one owner/runtime, and stable worker address |
| Authorization and Fresh | `/tmp/ao-mvp-final-166e9e63-orch-auth-fresh` | Unauthorized target `pi` returned `SWITCH_TARGET_UNAUTHORIZED` with zero durable, runtime, or native-session effect; same-harness Fresh generation `512804ab` preserved Codex, produced the exact phases, one handoff, and one owner/runtime without stacking |

## Promoted invariant result

The frozen candidate passed all twelve worker records and every orchestrator
record. The evidence demonstrates:

- uncertainty never becomes authoritative death, while literal absence on the
  AO-namespaced server permits the intended paused-dead recovery;
- durable switch/failover ledgers are ordered, idempotent, and generation
  stable across operator retry and crash recovery;
- terminal failure spends a rung exactly once, duplicate Continue is fenced,
  manual mode does not retry automatically, and capability claims remain
  truthful;
- orchestrator input is fenced while ownership is pending, recovery preserves
  one active owner and runtime, and worker routing survives provider changes;
  and
- unauthorized switches have no effect and same-harness Fresh creates one
  handoff without stacking another orchestrator.

This closes the live-acceptance item in the final MVP gate. Repository-wide
build, lint, race, frontend, and generated-API checks remain separately tracked
gate results; this document does not infer them from the live run.
