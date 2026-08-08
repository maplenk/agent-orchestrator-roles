# Final roles MVP live-acceptance kit

This kit captures the twelve frozen worker records and the strict in-place
orchestrator switch records against one exact integrated SHA. It drives a real
AO daemon, SQLite store, tmux adapter, HTTP/CLI surface, terminal mux, role map,
switch saga, and boot reconciliation. Provider CLIs are deterministic stubs so
fault timing is evidence rather than luck.

It never uses or reads the default `~/.ao` state. Every command fails unless
`AO_ACCEPTANCE_ROOT` is marked and lives below `/tmp`, `AO_DATA_DIR` is exactly
`$AO_ACCEPTANCE_ROOT/data`, `AO_RUN_FILE` is exactly the root's `running.json`,
`HOME` is exactly `$AO_ACCEPTANCE_ROOT/home`, `AO_ROLE_PROFILES_DIR` is pinned
to the current checkout's `profiles/`, and the data-dir-specific tmux wrapper
is first on `PATH`. The isolated home prevents Claude's required workspace-trust
prelaunch hook from updating the human's `~/.claude.json`; the explicit profile
path keeps that isolation from hiding the repository's frozen role templates.
`crash` additionally checks that the run-file PID's command names the
root-scoped acceptance binary before sending `SIGKILL`.

`sqlite3 -json` emits no bytes for a zero-row query. Snapshot collection
normalizes only that representation to `[]`, so pre-attempt ledger snapshots
and worker-only active-owner snapshots remain valid JSON evidence.

## Integration dependency

Do not collect final records on `be4321d1` alone. The integrated SHA must
contain coordinator fix `e2ed8bf` (both `restartRuntime` and `reconcileLive`
fail closed on `ErrRuntimeUnavailable`), plus the core and surface waves. Record
the resulting immutable SHA in every snapshot. Evidence from any older SHA is
exploratory.

## What the deterministic faults mean

- `keep-destroy`: `list-panes` and `kill-session` fail, while the subsequent
  real `has-session` proves the source is alive. This is a conclusive pre-stop
  failure, not an uncertain probe.
- `fail-create`: the real source destroy completes, then target `new-session`
  fails. This deterministically leaves `post_stop`.
- `delay-destroy`: holds the first Continue inside the live manager fence so a
  concurrent duplicate can be issued without a timing race.
- `seed-requested`: with the isolated daemon stopped, inserts the attempt and
  failover/requested ledger row in one `BEGIN IMMEDIATE` transaction. It models
  a crash after the contract's atomic durable write and before `SwitchWorker`.

The wrappers only react when the scoped marker and exact data path validate.
All provider input evidence records byte counts and SHA-256 digests, never task
or prompt text.

## Common start for one record root

Use a fresh root and port for each independent scenario. Records 5-7
intentionally share one root because they are consecutive observations of the
same generation.

```bash
ACC="$PWD/test/mvp-acceptance/acceptance.sh"
"$ACC" self-test
ROOT="/tmp/ao-mvp-$(git rev-parse --short HEAD)-r01"
"$ACC" init "$ROOT" 43181
source "$ROOT/env.sh"
"$ACC" check-env
"$ACC" build
"$ACC" start
"$ACC" setup-project
WORKER=$("$ACC" spawn worker | tail -1)
```

The root is deliberately not auto-deleted. Preserve it with the evidence. Use
`stop` only after a scenario whose shutdown semantics are not under test;
`crash` is the operation for crash-recovery records.

## Worker record 1 — paused-live Continue

```bash
INC=r01-live
BEFORE=$("$ACC" snapshot r01-before "$WORKER" mvpacc)
"$ACC" pause "$WORKER" "$INC" >"$ROOT/evidence/r01-pause.json"
"$ACC" assert-runtime "$WORKER" 1
"$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r01-continue.json"
GEN=$(jq -r .generationId "$ROOT/evidence/r01-continue.json")
AFTER=$("$ACC" snapshot r01-after "$WORKER" mvpacc)
"$ACC" assert-runtime "$WORKER" 1
"$ACC" assert-attempt "$WORKER" "$INC" 1 acked
"$ACC" assert-ledger "$WORKER" switch "$GEN" requested,pre_stop,post_stop,target_ack
"$ACC" assert-ledger "$WORKER" failover "$GEN" requested,target_ack
jq -e '.session[0].harness=="codex" and .session[0].pause_json=="" and .session[0].switch_pending_json==""' "$AFTER"
```

## Worker record 2 — naturally paused-dead Continue

Start from a fresh root. The source stub exits normally; because it is the only
pane in this data-dir-specific server, the server exits with it. This is the
honest `no server running` path fixed at `be4321d1`.

```bash
INC=r02-dead
"$ACC" pause "$WORKER" "$INC" >"$ROOT/evidence/r02-pause.json"
"$ACC" provider "$WORKER" claude exit
"$ACC" wait-runtime "$WORKER" dead 20
"$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r02-continue.json"
GEN=$(jq -r .generationId "$ROOT/evidence/r02-continue.json")
"$ACC" assert-runtime "$WORKER" 1
"$ACC" assert-attempt "$WORKER" "$INC" 1 acked
"$ACC" assert-ledger "$WORKER" switch "$GEN" requested,pre_stop,post_stop,target_ack
```

## Worker record 3 — exact role/template/permission identity

Use record 1's before/after snapshots. This also requires credential rotation
and keeps worktree/branch identity in the preservation set.

```bash
"$ACC" assert-preserved "$BEFORE" "$AFTER"
```

The assertion compares session/project/kind, role ID, role-map version/hash and
revision, template artifact/hash, resolved permissions/`CanSpawn`, branch,
workspace, and repo path. It separately requires the spawn-capability hash to
change.

## Worker record 4 — terminal pre-stop failure

Start fresh. `keep-destroy` makes Destroy fail while real tmux proves the source
still exists. Continue must fail, roll back pending, retain the pin, and mark
only this rung `failed`.

```bash
INC=r04-prestop
BEFORE=$("$ACC" snapshot r04-before "$WORKER" mvpacc)
"$ACC" pause "$WORKER" "$INC" >"$ROOT/evidence/r04-pause.json"
"$ACC" fault keep-destroy "$WORKER" on
if "$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r04-continue.out" 2>"$ROOT/evidence/r04-continue.err"; then
  echo "expected pre-stop failure" >&2; exit 1
fi
"$ACC" fault keep-destroy "$WORKER" off
"$ACC" wait-attempt "$WORKER" "$INC" failed
AFTER=$("$ACC" snapshot r04-after "$WORKER" mvpacc)
GEN=$(jq -r '.attempts[0].generation_id' "$AFTER")
"$ACC" assert-runtime "$WORKER" 1
"$ACC" assert-attempt "$WORKER" "$INC" 1 failed
"$ACC" assert-ledger "$WORKER" switch "$GEN" requested,pre_stop,failed
"$ACC" assert-ledger "$WORKER" failover "$GEN" requested,failed
jq -e '.session[0].pause_json!="" and .session[0].switch_pending_json=="" and ((.attempts|length)==1)' "$AFTER"
```

Do not use `assert-preserved` here: no successful relaunch means the spawn
credential must not be required to rotate. Compare the identity fields in the
two snapshots directly if reviewing this record manually.

## Worker records 5-7 — post-stop, passive boot, explicit recovery

These three records share one fresh root and one generation.

```bash
INC=r05-poststop
"$ACC" pause "$WORKER" "$INC" >"$ROOT/evidence/r05-pause.json"
"$ACC" fault fail-create "$WORKER" on
if "$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r05-continue.out" 2>"$ROOT/evidence/r05-continue.err"; then
  echo "expected post-stop failure" >&2; exit 1
fi
"$ACC" wait-attempt "$WORKER" "$INC" post_stop
R05=$("$ACC" snapshot r05-poststop "$WORKER" mvpacc)
GEN=$(jq -r '.attempts[0].generation_id' "$R05")
"$ACC" assert-attempt "$WORKER" "$INC" 1 post_stop
"$ACC" assert-runtime "$WORKER" 0
jq -e --arg gen "$GEN" '.session[0].pause_json!="" and .session[0].switch_pending_json!="" and ([.ledger[]|select(.generation_id==$gen and .phase=="target_ack")]|length)==0' "$R05"

"$ACC" crash
"$ACC" start
"$ACC" assert-passive "$WORKER" "$INC" "$GEN"
R06=$("$ACC" snapshot r06-passive-boot "$WORKER" mvpacc)

"$ACC" fault fail-create "$WORKER" off
"$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r07-recover.json"
jq -e --arg gen "$GEN" '.generationId==$gen and .reused==true' "$ROOT/evidence/r07-recover.json"
"$ACC" wait-pending "$WORKER" no
R07=$("$ACC" snapshot r07-recovered "$WORKER" mvpacc)
"$ACC" assert-attempt "$WORKER" "$INC" 1 acked
"$ACC" assert-runtime "$WORKER" 1
[[ "$(jq -r '.session[0].runtime_launch_id' "$R07")" == "$GEN" ]]
[[ "$(jq '.attempts|length' "$R07")" == 1 ]]
[[ "$(jq '[.ledger[]|select(.generation_id==$gen and .phase=="target_ack")]|length' --arg gen "$GEN" "$R07")" == 2 ]]
```

The final count is two acknowledgements for the generation by design: one
switch-saga `target_ack` and one failover-saga `target_ack`, never two in either
kind.

## Worker record 8 — crashed `requested` re-drive

Start fresh, pause, then crash the daemon while the source tmux process
survives. Seed exactly the transaction that completed before the hypothetical
crash; restart stays passive because there is no switch effect yet. The next
Continue must reuse it.

```bash
INC=r08-requested
GEN=r08-fixed-generation
"$ACC" pause "$WORKER" "$INC" >"$ROOT/evidence/r08-pause.json"
"$ACC" crash
"$ACC" seed-requested "$WORKER" "$INC" codex '' 0 "$GEN"
"$ACC" start
"$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r08-continue.json"
jq -e --arg gen "$GEN" '.generationId==$gen and .attemptSeq==1 and .reused==true' "$ROOT/evidence/r08-continue.json"
R08=$("$ACC" snapshot r08-redriven "$WORKER" mvpacc)
[[ "$(jq '.attempts|length' "$R08")" == 1 ]]
"$ACC" assert-attempt "$WORKER" "$INC" 1 acked
"$ACC" assert-runtime "$WORKER" 1
"$ACC" assert-ledger "$WORKER" switch "$GEN" requested,pre_stop,post_stop,target_ack
```

## Worker record 9 — duplicate while the saga is live

Start fresh. The first client blocks inside source Destroy for eight seconds;
the second request lands while `beginSwitch` is held.

```bash
INC=r09-duplicate
"$ACC" pause "$WORKER" "$INC" >"$ROOT/evidence/r09-pause.json"
"$ACC" fault delay-destroy "$WORKER" 8
"$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r09-first.json" 2>"$ROOT/evidence/r09-first.err" &
FIRST=$!
"$ACC" wait-pending "$WORKER" yes 10
if "$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r09-second.out" 2>"$ROOT/evidence/r09-second.err"; then
  echo "second request completed; verify it reports reuse rather than a new attempt" >>"$ROOT/evidence/r09-note.txt"
else
  grep -q SWITCH_IN_PROGRESS "$ROOT/evidence/r09-second.err"
fi
wait "$FIRST"
"$ACC" fault delay-destroy "$WORKER" off
R09=$("$ACC" snapshot r09-complete "$WORKER" mvpacc)
[[ "$(jq '.attempts|length' "$R09")" == 1 ]]
[[ "$(jq '[.ledger[]|select(.kind=="failover" and .phase=="requested")]|length' "$R09")" == 1 ]]
"$ACC" assert-runtime "$WORKER" 1
```

## Worker record 10 — exhausted ladder remains paused

Start fresh. Spend the one worker rung successfully, then pause the moved
session again with the same incident so its durable used-rung set applies.

```bash
INC=r10-exhausted
"$ACC" pause "$WORKER" "$INC" >/dev/null
"$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r10-first.json"
"$ACC" pause "$WORKER" "$INC" >/dev/null
if "$ACC" continue "$WORKER" "$INC" >"$ROOT/evidence/r10-second.out" 2>"$ROOT/evidence/r10-second.err"; then
  echo "expected FAILOVER_NO_TARGET" >&2; exit 1
fi
grep -q FAILOVER_NO_TARGET "$ROOT/evidence/r10-second.err"
R10=$("$ACC" snapshot r10-exhausted "$WORKER" mvpacc)
jq -e '.session[0].pause_json!="" and (.attempts|length)==1' "$R10"
"$ACC" assert-runtime "$WORKER" 1
```

## Worker record 11 — manual means no automatic failover

Start fresh and leave the pin alone during the observation window.

```bash
INC=r11-manual
"$ACC" pause "$WORKER" "$INC" >/dev/null
"$ACC" assert-no-auto "$WORKER" "$INC" 10
R11=$("$ACC" snapshot r11-no-auto "$WORKER" mvpacc)
jq -e '.attempts|length==0' "$R11"
jq -e '.project.config.roleMap.failover.mode=="manual"' "$ROOT/evidence/project-config-response.json"
```

## Worker record 12 — no limit detector promotion

This executes against `capabilities.AllDocumented`, the actual validation
registry, and separately pins Claude's honest read-only cell.

```bash
"$ACC" capabilities | tee "$ROOT/evidence/r12-capabilities.txt"
```

## Strict orchestrator Codex -> Claude -> Codex

Use a fresh root. Spawn the orchestrator first so the worker prompt records its
stable address.

```bash
ORCH=$("$ACC" spawn orchestrator | tail -1)
WORKER=$("$ACC" spawn worker | tail -1)
"$ACC" assert-worker-address "$WORKER" "$ORCH"
O0=$("$ACC" snapshot orch-codex-before "$ORCH" mvpacc)

"$ACC" switch "$ORCH" claude-code >"$ROOT/evidence/orch-to-claude.json"
G1=$(jq -r .generationId "$ROOT/evidence/orch-to-claude.json")
K1=$(jq -r .kind "$ROOT/evidence/orch-to-claude.json")
O1=$("$ACC" snapshot orch-claude "$ORCH" mvpacc)
"$ACC" assert-preserved "$O0" "$O1"
"$ACC" assert-ledger "$ORCH" "$K1" "$G1" requested,pre_stop,post_stop,target_ack
"$ACC" assert-runtime "$ORCH" 1
"$ACC" assert-owner mvpacc "$ORCH"
"$ACC" assert-worker-address "$WORKER" "$ORCH"
jq -e '.session[0].harness=="claude-code" and .session[0].resolved_workspace_writes==1 and .session[0].resolved_can_spawn==1' "$O1"

"$ACC" switch "$ORCH" codex >"$ROOT/evidence/orch-to-codex.json"
G2=$(jq -r .generationId "$ROOT/evidence/orch-to-codex.json")
K2=$(jq -r .kind "$ROOT/evidence/orch-to-codex.json")
O2=$("$ACC" snapshot orch-codex-after "$ORCH" mvpacc)
"$ACC" assert-preserved "$O1" "$O2"
"$ACC" assert-ledger "$ORCH" "$K2" "$G2" requested,pre_stop,post_stop,target_ack
"$ACC" assert-runtime "$ORCH" 1
"$ACC" assert-owner mvpacc "$ORCH"
"$ACC" assert-worker-address "$WORKER" "$ORCH"
jq -e '.session[0].harness=="codex"' "$O2"
```

Both calls must leave the same session ID, project, canonical worktree, branch,
role/template/permission facts, and worker address; change harness/model,
generation, and spawn credential; and have exactly one runtime and owner.

## Orchestrator pending fences and post-stop crash recovery

Start from a fresh Codex orchestrator (and optionally a worker). Force target
creation to fail after source stop, prove both input surfaces are fenced, crash,
then remove the fault before boot. Orchestrator recovery is automatic and must
reuse the pending generation.

```bash
ORCH=$("$ACC" spawn orchestrator | tail -1)
WORKER=$("$ACC" spawn worker | tail -1)
"$ACC" fault fail-create "$ORCH" on
if "$ACC" switch "$ORCH" claude-code >"$ROOT/evidence/orch-poststop.out" 2>"$ROOT/evidence/orch-poststop.err"; then
  echo "expected post-stop switch failure" >&2; exit 1
fi
OP=$("$ACC" snapshot orch-poststop "$ORCH" mvpacc)
GEN=$(jq -r '.session[0].switch_pending_json|fromjson|.generationId' "$OP")
KIND=$(jq -r '.session[0].switch_pending_json|fromjson|.kind' "$OP")
SOURCE_HANDLE=$(jq -r '.session[0].switch_pending_json|fromjson|.sourceRuntimeHandleId' "$OP")
"$ACC" send-expect-fenced "$ORCH"
"$ACC" mux-expect-fenced "$SOURCE_HANDLE"
"$ACC" assert-runtime "$ORCH" 0

"$ACC" crash
"$ACC" fault fail-create "$ORCH" off
"$ACC" start
"$ACC" wait-pending "$ORCH" no 30
OR=$("$ACC" snapshot orch-recovered "$ORCH" mvpacc)
[[ "$(jq -r '.session[0].runtime_launch_id' "$OR")" == "$GEN" ]]
"$ACC" assert-ledger "$ORCH" "$KIND" "$GEN" requested,pre_stop,post_stop,target_ack
"$ACC" assert-runtime "$ORCH" 1
"$ACC" assert-owner mvpacc "$ORCH"
"$ACC" assert-worker-address "$WORKER" "$ORCH"
```

## Unauthorized target and same-harness fresh regression

On a live Codex orchestrator:

```bash
BEFORE=$("$ACC" snapshot orch-before-unauthorized "$ORCH" mvpacc)
if "$ACC" switch "$ORCH" pi >"$ROOT/evidence/orch-unauthorized.out" 2>"$ROOT/evidence/orch-unauthorized.err"; then
  echo "unauthorized target accepted" >&2; exit 1
fi
grep -E 'UNAUTHORIZED|ROLE_SWITCH_TARGET|SWITCH_NOT_SUPPORTED' "$ROOT/evidence/orch-unauthorized.err"
AFTER=$("$ACC" snapshot orch-after-unauthorized "$ORCH" mvpacc)
[[ "$(jq '.ledger|length' "$BEFORE")" == "$(jq '.ledger|length' "$AFTER")" ]]

"$ACC" fresh "$ORCH" >"$ROOT/evidence/orch-fresh.json"
GF=$(jq -r .generationId "$ROOT/evidence/orch-fresh.json")
KF=$(jq -r .kind "$ROOT/evidence/orch-fresh.json")
"$ACC" assert-ledger "$ORCH" "$KF" "$GF" requested,pre_stop,post_stop,target_ack
"$ACC" assert-handoff-count "$ORCH" 1
"$ACC" assert-runtime "$ORCH" 1
"$ACC" assert-owner mvpacc "$ORCH"
```

The exact unauthorized public code may be finalized by the surface wave; it
must be actionable and the durable no-effect assertion is mandatory.

## Final evidence review

Every JSON snapshot contains the code SHA, exact data directory, sanitized
daemon PID/port, project-config hash, prompt hash, full invariant session
fields, attempt rows, ordered ledger rows, active orchestrator rows, tmux socket
name, and tmux session list. It never contains the operator token or plaintext
spawn capability.

Before promotion:

1. Confirm every snapshot names one integrated SHA.
2. Confirm no unexplained ledger phase or duplicate per-kind acknowledgement.
3. Confirm all successful targets have exactly one tmux runtime.
4. Confirm every failure retains the pause and the expected pending/terminal
   attempt state.
5. Confirm the orchestrator session ID, ownership row, worktree, branch, role,
   template, permissions, and worker address never change.
6. Attach the cold-cache backend/frontend/API gate logs from the same SHA.
