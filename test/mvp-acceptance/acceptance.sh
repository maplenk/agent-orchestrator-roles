#!/bin/bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/../.." && pwd -P)"

# The tmux adapter bounds every CLI call at five seconds. The live duplicate
# fault must hold the project/session fence long enough for a second request,
# but it must release before CommandContext kills the wrapper instead of
# reaching the real kill-session.
tmux_call_timeout_seconds=5
duplicate_delay_seconds=2

die() { printf 'mvp-acceptance: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }
safe_atom() { [[ "$1" =~ ^[A-Za-z0-9._-]+$ ]] || die "unsafe identifier: $1"; }

usage() {
	cat <<'EOF'
Usage: acceptance.sh <command> [args]

Environment lifecycle:
  init ABS_TMP_ROOT [PORT]       Create a new isolated root and env.sh
  check-env                      Refuse any unscoped/default AO paths
  self-test                      Check harness JSON/profile prerequisites
  build                          Build the current checkout as ROOT/bin/ao
  start                          Start the isolated daemon and wait for readyz
  crash                          SIGKILL only the validated isolated daemon PID
  stop                           Ask only the isolated daemon to stop cleanly
  setup-project                  Init/register repo and install frozen role map

Session actions:
  spawn worker|orchestrator      Spawn with the host-resolved role
  pause SESSION INCIDENT         Operator pause through the loopback API
  continue SESSION INCIDENT      Continue through the CLI, JSON response
  switch SESSION HARNESS [MODEL] Authorized switch through the CLI
  fresh SESSION                  Same-harness fresh conversation
  send-expect-fenced SESSION     Require API input to return SWITCH_IN_PROGRESS
  mux-expect-fenced TERMINAL_ID  Require mux data to hit the durable fence

Deterministic fault controls:
  fault keep-destroy SESSION on|off
  fault fail-create SESSION on|off
  fault delay-destroy SESSION SECONDS|off
  provider SESSION codex|claude hold|exit|fail
  terminate-runtime SESSION       Kill the sole validated AO-namespaced runtime
  seed-requested SESSION INCIDENT TARGET_HARNESS TARGET_MODEL RUNG GENERATION

Evidence and assertions:
  snapshot LABEL SESSION PROJECT
  wait-attempt SESSION INCIDENT STATE [TIMEOUT]
  wait-phase SESSION KIND GENERATION PHASE [TIMEOUT]
  wait-pending SESSION yes|no [TIMEOUT]
  wait-runtime SESSION alive|dead [TIMEOUT]
  assert-preserved BEFORE_JSON AFTER_JSON
  assert-runtime SESSION 0|1
  assert-owner PROJECT SESSION
  assert-ledger SESSION KIND GENERATION PHASE_CSV
  assert-attempt SESSION INCIDENT SEQ STATE
  assert-passive SESSION INCIDENT GENERATION
  assert-no-auto SESSION INCIDENT SECONDS
  assert-handoff-count SESSION COUNT
  assert-worker-address WORKER ORCHESTRATOR
  capabilities
EOF
}

init_root() {
	[[ $# -ge 1 ]] || die "init requires ABS_TMP_ROOT"
	local root="$1" port="${2:-43173}"
	[[ "$root" == /* ]] || die "root must be absolute"
	[[ "$root" =~ ^[A-Za-z0-9._/-]+$ ]] || die "root contains unsupported characters"
	case "$root" in
	/tmp/* | /private/tmp/*) ;;
	*) die "root must be below /tmp or /private/tmp" ;;
	esac
	[[ "$port" =~ ^[0-9]+$ ]] || die "port must be numeric"
	((port >= 1024 && port <= 65535)) || die "port outside 1024..65535"
	if [[ -e "$root" && ! -f "$root/.ao-mvp-acceptance" ]]; then
		die "existing root lacks acceptance marker: $root"
	fi
	mkdir -p "$root/data" "$root/evidence" "$root/control/provider" "$root/control/tmux" "$root/bin" "$root/repo" "$root/home"
	root="$(cd "$root" && pwd -P)"
	printf 'ao-mvp-acceptance-v1\n' >"$root/.ao-mvp-acceptance"
	local real_tmux go_cache go_mod_cache
	real_tmux="${AO_ACCEPTANCE_REAL_TMUX:-$(command -v tmux)}"
	[[ "$real_tmux" != "$script_dir/stubs/tmux" ]] || die "PATH already points at the acceptance tmux wrapper"
	[[ "$real_tmux" != *"'"* && "$real_tmux" != *$'\n'* ]] || die "real tmux path cannot be safely exported"
	go_cache="$(go env GOCACHE 2>/dev/null || true)"
	go_mod_cache="$(go env GOMODCACHE 2>/dev/null || true)"
	[[ "$go_cache" != *"'"* && "$go_mod_cache" != *"'"* ]] || die "Go cache path cannot be safely exported"
	cat >"$root/env.sh" <<EOF
export AO_ACCEPTANCE_ROOT='$root'
export AO_DATA_DIR='$root/data'
export AO_RUN_FILE='$root/running.json'
export AO_PORT='$port'
export AO_ACCEPTANCE_BIN='$root/bin/ao'
export AO_ACCEPTANCE_REAL_TMUX='$real_tmux'
export AO_ROLE_PROFILES_DIR='$repo_root/profiles'
export HOME='$root/home'
export GOCACHE='$go_cache'
export GOMODCACHE='$go_mod_cache'
export PATH='$script_dir/stubs':\$PATH
EOF
	printf 'created %s\nsource %s/env.sh\n' "$root" "$root"
}

guard() {
	need jq; need sqlite3; need curl; need shasum; need tmux
	local root="${AO_ACCEPTANCE_ROOT:-}" data="${AO_DATA_DIR:-}" run="${AO_RUN_FILE:-}"
	[[ -n "$root" && -n "$data" && -n "$run" ]] || die "source an acceptance env.sh first"
	[[ "$root" == /* && "$data" == /* && "$run" == /* ]] || die "all AO paths must be absolute"
	case "$root" in
	/tmp/* | /private/tmp/*) ;;
	*) die "AO_ACCEPTANCE_ROOT must be below /tmp or /private/tmp" ;;
	esac
	[[ -f "$root/.ao-mvp-acceptance" ]] || die "missing acceptance marker: $root"
	local canonical_root
	canonical_root="$(cd "$root" && pwd -P)"
	[[ "$data" == "$canonical_root/data" ]] || die "AO_DATA_DIR must equal $canonical_root/data"
	[[ "$run" == "$canonical_root/running.json" ]] || die "AO_RUN_FILE must equal $canonical_root/running.json"
	[[ "${AO_ACCEPTANCE_BIN:-}" == "$canonical_root/bin/ao" ]] || die "AO_ACCEPTANCE_BIN is outside the root"
	[[ "${HOME:-}" == "$canonical_root/home" ]] || die "HOME must equal $canonical_root/home to isolate provider config"
	[[ "${AO_ROLE_PROFILES_DIR:-}" == "$repo_root/profiles" ]] || die "AO_ROLE_PROFILES_DIR must equal $repo_root/profiles"
	[[ -d "$AO_ROLE_PROFILES_DIR" ]] || die "role profiles directory not found: $AO_ROLE_PROFILES_DIR"
	[[ "${AO_PORT:-}" =~ ^[0-9]+$ ]] || die "AO_PORT must be numeric"
	[[ -n "${AO_ACCEPTANCE_REAL_TMUX:-}" && -x "${AO_ACCEPTANCE_REAL_TMUX}" ]] || die "real tmux is not executable"
	[[ "$(command -v tmux)" == "$script_dir/stubs/tmux" ]] || die "acceptance tmux wrapper is not first on PATH"
}

db_path() { guard; [[ -f "$AO_DATA_DIR/ao.db" ]] || die "database not created: $AO_DATA_DIR/ao.db"; printf '%s\n' "$AO_DATA_DIR/ao.db"; }

# sqlite3 -json prints an empty string, rather than [], when a SELECT returns no
# rows. Snapshot fields are passed through jq --argjson, so normalize that one
# valid empty-result representation without rewriting non-empty JSON.
sqlite_json() {
	local db="${1:-}" sql="${2:-}" out
	[[ -n "$db" && -n "$sql" ]] || die "sqlite_json requires DB and SQL"
	out="$(sqlite3 -json "$db" "$sql")"
	[[ -n "$out" ]] || out='[]'
	printf '%s\n' "$out"
}

self_test() {
	need sqlite3
	[[ -f "$repo_root/profiles/orchestrator.md" && -f "$repo_root/profiles/implementor.md" ]] ||
		die "required acceptance role profiles are missing"
	[[ "$(sqlite_json :memory: 'SELECT 1 AS value WHERE 0;')" == '[]' ]] ||
		die "zero-row sqlite JSON did not normalize to []"
	[[ "$(sqlite_json :memory: 'SELECT 1 AS value;')" == '[{"value":1}]' ]] ||
		die "non-empty sqlite JSON changed during normalization"
	validate_runtime_termination_target mvpacc-1 mvpacc-1 ao-0123456789ab mvpacc-1
	if (validate_runtime_termination_target mvpacc-1 mvpacc-2 ao-0123456789ab mvpacc-2) >/dev/null 2>&1; then
		die "runtime termination scope accepted a mismatched durable handle"
	fi
	if (validate_runtime_termination_target mvpacc-1 mvpacc-1 default mvpacc-1) >/dev/null 2>&1; then
		die "runtime termination scope accepted a non-AO socket"
	fi
	if (validate_runtime_termination_target mvpacc-1 mvpacc-1 ao-0123456789ab $'mvpacc-1\nother') >/dev/null 2>&1; then
		die "runtime termination scope accepted a shared namespace"
	fi
	((duplicate_delay_seconds < tmux_call_timeout_seconds)) ||
		die "duplicate delay must remain below the tmux command timeout"
	grep -Eq 'defaultTimeout[[:space:]]*=[[:space:]]*5 \* time.Second' "$repo_root/backend/internal/adapters/runtime/tmux/tmux.go" ||
		die "tmux default timeout changed; revalidate the acceptance duplicate delay"
	local address_check="$script_dir/stubs/provider-address-check"
	[[ "$($address_check mvpacc-1 '--system=report-to-mvpacc-1')" == true ]] ||
		die "provider address checker missed an argv address"
	[[ "$($address_check wrong-id '--system=report-to-mvpacc-1')" == false ]] ||
		die "provider address checker accepted the wrong expected id"
	printf 'acceptance harness self-test passed\n'
}

daemon_pid() {
	guard
	[[ -f "$AO_RUN_FILE" ]] || die "run file missing: $AO_RUN_FILE"
	local pid
	pid="$(jq -er '.pid | select(type == "number" and . > 1)' "$AO_RUN_FILE")" || die "invalid isolated run-file pid"
	printf '%s\n' "$pid"
}

daemon_port() {
	guard
	[[ -f "$AO_RUN_FILE" ]] || die "run file missing"
	jq -er '.port | select(type == "number" and . >= 1024 and . <= 65535)' "$AO_RUN_FILE"
}

build_binary() {
	guard; need go
	(cd "$repo_root/backend" && go build -o "$AO_ACCEPTANCE_BIN" ./cmd/ao)
	git -C "$repo_root" rev-parse HEAD >"$AO_ACCEPTANCE_ROOT/evidence/build-sha.txt"
	printf 'built %s at %s\n' "$AO_ACCEPTANCE_BIN" "$(cat "$AO_ACCEPTANCE_ROOT/evidence/build-sha.txt")"
}

start_daemon() {
	guard
	[[ -x "$AO_ACCEPTANCE_BIN" ]] || die "run build first"
	if [[ -f "$AO_RUN_FILE" ]]; then
		local stale
		stale="$(jq -r '.pid // 0' "$AO_RUN_FILE" 2>/dev/null || printf 0)"
		if [[ "$stale" =~ ^[0-9]+$ ]] && ((stale > 1)) && kill -0 "$stale" 2>/dev/null; then
			die "isolated daemon already live as pid $stale"
		fi
	fi
	"$AO_ACCEPTANCE_BIN" daemon >>"$AO_ACCEPTANCE_ROOT/evidence/daemon.log" 2>&1 &
	printf '%s\n' "$!" >"$AO_ACCEPTANCE_ROOT/launcher.pid"
	local deadline=$((SECONDS + 30))
	while ((SECONDS < deadline)); do
		if [[ -f "$AO_RUN_FILE" ]]; then
			local port
			port="$(jq -r '.port // 0' "$AO_RUN_FILE" 2>/dev/null || printf 0)"
			if [[ "$port" =~ ^[0-9]+$ ]] && curl -fsS "http://127.0.0.1:$port/readyz" >/dev/null 2>&1; then
				printf 'daemon ready pid=%s port=%s\n' "$(daemon_pid)" "$port"
				return
			fi
		fi
		sleep 0.1
	done
	die "daemon did not become ready; inspect $AO_ACCEPTANCE_ROOT/evidence/daemon.log"
}

crash_daemon() {
	local pid; pid="$(daemon_pid)"
	# Resolve the executable before destructive action. The PID must be the one
	# published by the isolated run file and its command must name our binary.
	local cmd
	cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
	[[ "$cmd" == *"$AO_ACCEPTANCE_BIN"* ]] || die "pid $pid does not run $AO_ACCEPTANCE_BIN: $cmd"
	kill -KILL "$pid"
	local deadline=$((SECONDS + 10))
	while kill -0 "$pid" 2>/dev/null && ((SECONDS < deadline)); do sleep 0.1; done
	if kill -0 "$pid" 2>/dev/null; then die "isolated daemon pid $pid survived SIGKILL"; fi
	printf 'crashed isolated daemon pid=%s; stale run file intentionally retained\n' "$pid"
}

stop_daemon() {
	guard
	local pid; pid="$(daemon_pid)"
	local cmd; cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
	[[ "$cmd" == *"$AO_ACCEPTANCE_BIN"* ]] || die "pid $pid does not run isolated binary"
	"$AO_ACCEPTANCE_BIN" stop >/dev/null
	printf 'stopped isolated daemon pid=%s\n' "$pid"
}

setup_project() {
	guard; [[ -x "$AO_ACCEPTANCE_BIN" ]] || die "build first"
	local repo="$AO_ACCEPTANCE_ROOT/repo"
	if [[ ! -d "$repo/.git" ]]; then
		git -C "$repo" init -b main >/dev/null
		git -C "$repo" config user.name "AO MVP Acceptance"
		git -C "$repo" config user.email "ao-mvp-acceptance@invalid"
		printf '# isolated AO MVP acceptance repository\n' >"$repo/README.md"
		git -C "$repo" add README.md
		git -C "$repo" commit -m "chore: initialize acceptance fixture" >/dev/null
	fi
	if ! "$AO_ACCEPTANCE_BIN" project get mvpacc --json >/dev/null 2>&1; then
		"$AO_ACCEPTANCE_BIN" project add --path "$repo" --id mvpacc --name "MVP Acceptance"
	fi
	local config
	config="$(jq -c . "$script_dir/role-map.json")"
	"$AO_ACCEPTANCE_BIN" project set-config mvpacc --config-json "$config" --json \
		>"$AO_ACCEPTANCE_ROOT/evidence/project-config-response.json"
	printf 'project mvpacc configured from %s\n' "$script_dir/role-map.json"
}

spawn_session() {
	guard
	local kind="${1:-}" role name
	case "$kind" in
	worker) role=implementor; name=Worker ;;
	orchestrator) role=orchestrator; name=Orchestrator ;;
	*) die "spawn kind must be worker or orchestrator" ;;
	esac
	if [[ "$kind" == worker ]]; then
		local db owner_count owner_file orchestrator
		db="$(db_path)"; owner_file="$AO_ACCEPTANCE_ROOT/control/expected-orchestrator"
		owner_count="$(sqlite3 "$db" "SELECT COUNT(*) FROM sessions WHERE project_id='mvpacc' AND kind='orchestrator' AND is_terminated=0;")"
		((owner_count <= 1)) || die "worker launch found multiple active orchestrators"
		if ((owner_count == 1)); then
			orchestrator="$(sqlite3 "$db" "SELECT id FROM sessions WHERE project_id='mvpacc' AND kind='orchestrator' AND is_terminated=0;")"
			safe_atom "$orchestrator"
			printf '%s\n' "$orchestrator" >"$owner_file"
		elif [[ -e "$owner_file" ]]; then
			rm "$owner_file"
		fi
	fi
	"$AO_ACCEPTANCE_BIN" spawn --project mvpacc --kind "$kind" --role "$role" \
		--name "$name" --prompt "Hold for deterministic MVP acceptance." --skip-agent-check
	local db; db="$(db_path)"
	sqlite3 "$db" "SELECT id FROM sessions WHERE project_id='mvpacc' AND kind='$kind' ORDER BY num DESC LIMIT 1;"
}

operator_token() {
	guard
	jq -er '.operatorSpawnToken | select(type == "string" and length > 0)' "$AO_RUN_FILE"
}

pause_session() {
	guard; local sid="${1:-}" incident="${2:-}"; safe_atom "$sid"; safe_atom "$incident"
	local port token; port="$(daemon_port)"; token="$(operator_token)"
	curl -fsS -X POST "http://127.0.0.1:$port/api/v1/sessions/$sid/pause" \
		-H 'Content-Type: application/json' -H "X-AO-Operator-Spawn-Token: $token" \
		--data "$(jq -nc --arg id "$incident" '{incidentId:$id,reason:"operator"}')"
	printf '\n'
}

continue_session() { guard; safe_atom "${1:-}"; safe_atom "${2:-}"; "$AO_ACCEPTANCE_BIN" session continue --session "$1" --incident "$2" --json; }
switch_session() { guard; safe_atom "${1:-}"; safe_atom "${2:-}"; local args=(session switch --session "$1" --harness "$2" --json); [[ -z "${3:-}" ]] || args+=(--model "$3"); "$AO_ACCEPTANCE_BIN" "${args[@]}"; }
fresh_session() { guard; safe_atom "${1:-}"; "$AO_ACCEPTANCE_BIN" session fresh --session "$1" --json; }

send_expect_fenced() {
	guard; safe_atom "${1:-}"
	local out="$AO_ACCEPTANCE_ROOT/evidence/send-fence-$1.txt"
	if "$AO_ACCEPTANCE_BIN" send --session "$1" --message MVP_FENCE_MUST_NOT_REACH_PROVIDER >"$out" 2>&1; then
		die "send unexpectedly crossed switch fence"
	fi
	grep -q 'SWITCH_IN_PROGRESS' "$out" || die "send failed without SWITCH_IN_PROGRESS; see $out"
	printf 'API input fenced; response captured at %s\n' "$out"
}

mux_expect_fenced() {
	guard; safe_atom "${1:-}"; need go
	local port; port="$(daemon_port)"
	(cd "$repo_root/backend" && AO_ACCEPTANCE_MUX_URL="ws://127.0.0.1:$port/mux" \
		AO_ACCEPTANCE_TERMINAL_ID="$1" go test ./test/mvp_acceptance -run '^TestLiveMuxInputFence$' -count=1 -v)
}

fault_control() {
	guard
	local kind="${1:-}" sid="${2:-}" value="${3:-}" file
	safe_atom "$sid"
	case "$kind" in
	keep-destroy | fail-create) file="$AO_ACCEPTANCE_ROOT/control/tmux/$kind.$sid" ;;
	delay-destroy) file="$AO_ACCEPTANCE_ROOT/control/tmux/$kind.$sid" ;;
	*) die "unknown fault: $kind" ;;
	esac
	if [[ "$value" == off ]]; then
		[[ -e "$file" ]] && rm "$file"
		printf '%s fault disabled for %s\n' "$kind" "$sid"
		return
	fi
	case "$kind" in
	keep-destroy | fail-create) [[ "$value" == on ]] || die "$kind expects on|off"; printf 'on\n' >"$file" ;;
	delay-destroy)
		[[ "$value" =~ ^[1-9][0-9]*$ ]] || die "delay expects positive seconds|off"
		((value < tmux_call_timeout_seconds)) || die "delay must be below tmux's ${tmux_call_timeout_seconds}s command timeout"
		printf '%s\n' "$value" >"$file"
		;;
	esac
	printf '%s fault enabled for %s\n' "$kind" "$sid"
}

provider_control() {
	guard
	local sid="${1:-}" provider="${2:-}" mode="${3:-}"
	safe_atom "$sid"
	case "$provider" in codex | claude) ;; *) die "provider must be codex or claude" ;; esac
	case "$mode" in hold | exit | fail) ;; *) die "mode must be hold, exit, or fail" ;; esac
	printf '%s\n' "$mode" >"$AO_ACCEPTANCE_ROOT/control/provider/$sid.$provider"
	printf 'provider %s/%s -> %s\n' "$sid" "$provider" "$mode"
}

tmux_socket() {
	guard
	local digest
	digest="$(printf %s "$AO_DATA_DIR" | shasum -a 256 | awk '{print $1}')"
	printf 'ao-%s\n' "${digest:0:12}"
}

validate_runtime_termination_target() {
	local sid="${1:-}" handle="${2:-}" socket="${3:-}" names="${4:-}"
	safe_atom "$sid"; safe_atom "$handle"
	[[ "$handle" == "$sid" ]] || die "runtime handle $handle does not exactly match session $sid"
	[[ "$socket" =~ ^ao-[0-9a-f]{12}$ ]] || die "refusing non-AO tmux namespace: $socket"
	[[ "$names" == "$handle" ]] || die "runtime termination requires sole namespace session $handle; found: ${names:-<none>}"
}

# Record 2 needs an authoritatively runtime-dead paused session, but an agent
# process exit alone leaves AO's keep-alive shell running. Resolve the durable
# handle from the isolated database, require it to be the only session on the
# data-dir-derived AO socket, and kill that exact handle. Starting an empty
# server once after the kill makes tmux's stable subsequent answer the literal
# "no server running" classification under test, without leaving a server or
# session alive.
terminate_runtime() {
	guard
	local sid="${1:-}" db handle socket names out lower status=0
	safe_atom "$sid"; db="$(db_path)"
	handle="$(sqlite3 "$db" "SELECT runtime_handle_id FROM sessions WHERE id='$sid';")"
	[[ -n "$handle" ]] || die "session has no durable runtime handle: $sid"
	socket="$(tmux_socket)"
	names="$("$AO_ACCEPTANCE_REAL_TMUX" -L "$socket" list-sessions -F '#{session_name}')" ||
		die "cannot inspect AO tmux namespace $socket"
	validate_runtime_termination_target "$sid" "$handle" "$socket" "$names"
	"$AO_ACCEPTANCE_REAL_TMUX" -L "$socket" kill-session -t "=$handle"
	"$AO_ACCEPTANCE_REAL_TMUX" -L "$socket" start-server
	out="$("$AO_ACCEPTANCE_REAL_TMUX" -L "$socket" has-session -t "=$handle" 2>&1)" || status=$?
	((status != 0)) || die "runtime $handle still exists after scoped termination"
	lower="$(printf %s "$out" | tr '[:upper:]' '[:lower:]')"
	[[ "$lower" == *"no server running"* ]] ||
		die "tmux did not produce authoritative namespaced absence: $out"
	[[ "$(runtime_count "$sid")" == 0 ]] || die "runtime $handle remained listed after scoped termination"
	printf 'terminated exact isolated runtime session=%s socket=%s; literal no-server absence verified\n' "$handle" "$socket"
}

runtime_count() {
	guard; local sid="${1:-}"; safe_atom "$sid"
	local socket names; socket="$(tmux_socket)"
	names="$("$AO_ACCEPTANCE_REAL_TMUX" -L "$socket" list-sessions -F '#{session_name}' 2>/dev/null || true)"
	printf '%s\n' "$names" | awk -v id="$sid" '$0 == id {n++} END {print n+0}'
}

snapshot() {
	guard
	local label="${1:-}" sid="${2:-}" project="${3:-}"
	safe_atom "$label"; safe_atom "$sid"; safe_atom "$project"
	local db; db="$(db_path)"
	local session attempts ledger project_row owner runtime_names prompt_hash config_hash run_pid=0 run_port=0
	session="$(sqlite_json "$db" "SELECT id,project_id,num,kind,harness,role_id,role_map_schema_version,role_map_sha256,role_config_revision,template_artifact_id,template_sha256,resolved_model,resolved_workspace_writes,resolved_can_spawn,spawn_capability_hash,activity_state,is_terminated,branch,workspace_path,workspace_repo_path,runtime_handle_id,runtime_launch_id,agent_session_id,switch_pending_json,pause_json,created_at,updated_at FROM sessions WHERE id='$sid';")"
	[[ "$session" != '[]' ]] || die "session not found: $sid"
	attempts="$(sqlite_json "$db" "SELECT id,incident_id,seq,role_id,from_harness,from_model,to_harness,to_model,rung_index,generation_id,state,created_at,updated_at FROM session_failover_attempts WHERE session_id='$sid' ORDER BY seq;")"
	ledger="$(sqlite_json "$db" "SELECT rowid,id,kind,phase,generation_id,from_harness,to_harness,from_model,to_model,role_id,source_native_session_id,target_native_session_id,created_at FROM lifecycle_ledger WHERE session_id='$sid' ORDER BY rowid;")"
	project_row="$(sqlite_json "$db" "SELECT id,path,kind,length(COALESCE(config,'')) AS config_bytes FROM projects WHERE id='$project';")"
	owner="$(sqlite_json "$db" "SELECT id,harness,runtime_handle_id,runtime_launch_id FROM sessions WHERE project_id='$project' AND kind='orchestrator' AND is_terminated=0 ORDER BY num;")"
	prompt_hash="$(sqlite3 "$db" "SELECT prompt FROM sessions WHERE id='$sid';" | shasum -a 256 | awk '{print $1}')"
	config_hash="$(sqlite3 "$db" "SELECT COALESCE(config,'') FROM projects WHERE id='$project';" | shasum -a 256 | awk '{print $1}')"
	local socket; socket="$(tmux_socket)"
	runtime_names="$("$AO_ACCEPTANCE_REAL_TMUX" -L "$socket" list-sessions -F '#{session_name}' 2>/dev/null || true)"
	if [[ -f "$AO_RUN_FILE" ]]; then
		run_pid="$(jq -r '.pid // 0' "$AO_RUN_FILE" 2>/dev/null || printf 0)"
		run_port="$(jq -r '.port // 0' "$AO_RUN_FILE" 2>/dev/null || printf 0)"
	fi
	local out="$AO_ACCEPTANCE_ROOT/evidence/$label.json"
	jq -n --arg sha "$(git -C "$repo_root" rev-parse HEAD)" --arg dataDir "$AO_DATA_DIR" \
		--arg projectConfigSha256 "$config_hash" --arg promptSha256 "$prompt_hash" --arg socket "$socket" \
		--arg runtimeNames "$runtime_names" --argjson daemonPid "$run_pid" --argjson daemonPort "$run_port" \
		--argjson session "$session" --argjson attempts "$attempts" --argjson ledger "$ledger" \
		--argjson project "$project_row" --argjson activeOrchestrators "$owner" \
		'{sha:$sha,dataDir:$dataDir,daemon:{pid:$daemonPid,port:$daemonPort},projectConfigSha256:$projectConfigSha256,promptSha256:$promptSha256,tmux:{socket:$socket,sessions:($runtimeNames|split("\n")|map(select(length>0)))},session:$session,attempts:$attempts,ledger:$ledger,project:$project,activeOrchestrators:$activeOrchestrators}' >"$out"
	printf '%s\n' "$out"
}

wait_sql() {
	local sql="$1" timeout="${2:-20}"; [[ "$timeout" =~ ^[1-9][0-9]*$ ]] || die "timeout must be positive"
	local db; db="$(db_path)"; local deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do
		if [[ "$(sqlite3 "$db" "$sql" 2>/dev/null || true)" == 1 ]]; then return; fi
		sleep 0.05
	done
	die "timed out waiting for SQL predicate: $sql"
}

wait_attempt() { safe_atom "${1:-}"; safe_atom "${2:-}"; safe_atom "${3:-}"; wait_sql "SELECT EXISTS(SELECT 1 FROM session_failover_attempts WHERE session_id='$1' AND incident_id='$2' AND state='$3');" "${4:-20}"; }
wait_phase() { safe_atom "${1:-}"; safe_atom "${2:-}"; safe_atom "${3:-}"; safe_atom "${4:-}"; wait_sql "SELECT EXISTS(SELECT 1 FROM lifecycle_ledger WHERE session_id='$1' AND kind='$2' AND generation_id='$3' AND phase='$4');" "${5:-20}"; }
wait_pending() { safe_atom "${1:-}"; local want="${2:-}" expr; case "$want" in yes) expr="switch_pending_json != ''" ;; no) expr="switch_pending_json = ''" ;; *) die "want yes|no" ;; esac; wait_sql "SELECT EXISTS(SELECT 1 FROM sessions WHERE id='$1' AND $expr);" "${3:-20}"; }

wait_runtime() {
	guard; safe_atom "${1:-}"; local want="${2:-}" timeout="${3:-20}" expected
	case "$want" in alive) expected=1 ;; dead) expected=0 ;; *) die "want alive|dead" ;; esac
	local deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do [[ "$(runtime_count "$1")" == "$expected" ]] && return; sleep 0.1; done
	die "timed out waiting for runtime $1 -> $want"
}

assert_preserved() {
	local before="${1:-}" after="${2:-}"; [[ -f "$before" && -f "$after" ]] || die "snapshot missing"
	local filter='.[0].session[0] as $a | .[1].session[0] as $b | ["id","project_id","kind","role_id","role_map_schema_version","role_map_sha256","role_config_revision","template_artifact_id","template_sha256","resolved_workspace_writes","resolved_can_spawn","branch","workspace_path","workspace_repo_path"] | all(.[]; $a[.] == $b[.])'
	jq -e -s "$filter" "$before" "$after" >/dev/null || die "identity/template/permission/workspace drift"
	[[ "$(jq -r '.session[0].spawn_capability_hash' "$before")" != "$(jq -r '.session[0].spawn_capability_hash' "$after")" ]] || die "spawn capability hash did not rotate"
	printf 'preserved identity/template/permissions/workspace; credential rotated\n'
}

assert_runtime() { guard; safe_atom "${1:-}"; [[ "${2:-}" =~ ^[01]$ ]] || die "runtime count must be 0|1"; [[ "$(runtime_count "$1")" == "$2" ]] || die "runtime count for $1 is $(runtime_count "$1"), want $2"; }

assert_owner() {
	guard; safe_atom "${1:-}"; safe_atom "${2:-}"; local db; db="$(db_path)"
	local got; got="$(sqlite3 "$db" "SELECT group_concat(id, ',') FROM sessions WHERE project_id='$1' AND kind='orchestrator' AND is_terminated=0;")"
	[[ "$got" == "$2" ]] || die "active orchestrator set=$got want=$2"
}

assert_ledger() {
	guard; safe_atom "${1:-}"; safe_atom "${2:-}"; safe_atom "${3:-}"
	local want="${4:-}" db got; db="$(db_path)"
	got="$(sqlite3 "$db" "SELECT group_concat(phase, ',') FROM (SELECT phase FROM lifecycle_ledger WHERE session_id='$1' AND kind='$2' AND generation_id='$3' ORDER BY rowid);")"
	[[ "$got" == "$want" ]] || die "ledger phases=$got want=$want"
}

assert_attempt() {
	guard; safe_atom "${1:-}"; safe_atom "${2:-}"; [[ "${3:-}" =~ ^[1-9][0-9]*$ ]] || die "seq invalid"; safe_atom "${4:-}"
	local db got; db="$(db_path)"
	got="$(sqlite3 "$db" "SELECT generation_id || '|' || state || '|' || COUNT(*) FROM session_failover_attempts WHERE session_id='$1' AND incident_id='$2' AND seq=$3;")"
	[[ "$got" == *"|$4|1" ]] || die "attempt assertion failed: $got"
}

assert_passive() {
	guard; safe_atom "${1:-}"; safe_atom "${2:-}"; safe_atom "${3:-}"; local db; db="$(db_path)"
	[[ "$(sqlite3 "$db" "SELECT COUNT(*) FROM sessions WHERE id='$1' AND pause_json!='' AND switch_pending_json!='';")" == 1 ]] || die "pause/pending not retained"
	[[ "$(sqlite3 "$db" "SELECT COUNT(*) FROM session_failover_attempts WHERE session_id='$1' AND incident_id='$2' AND generation_id='$3' AND state='post_stop';")" == 1 ]] || die "attempt not post_stop"
	[[ "$(sqlite3 "$db" "SELECT COUNT(*) FROM lifecycle_ledger WHERE session_id='$1' AND generation_id='$3' AND phase='target_ack';")" == 0 ]] || die "boot reached target_ack automatically"
	assert_runtime "$1" 0
}

assert_no_auto() {
	guard; safe_atom "${1:-}"; safe_atom "${2:-}"; local seconds="${3:-5}"; [[ "$seconds" =~ ^[1-9][0-9]*$ ]] || die "seconds invalid"
	local db before after; db="$(db_path)"; before="$(sqlite3 "$db" "SELECT COUNT(*) FROM session_failover_attempts WHERE session_id='$1' AND incident_id='$2';")"
	sleep "$seconds"
	after="$(sqlite3 "$db" "SELECT COUNT(*) FROM session_failover_attempts WHERE session_id='$1' AND incident_id='$2';")"
	[[ "$before" == "$after" ]] || die "automatic failover created an attempt ($before->$after)"
	[[ "$(sqlite3 "$db" "SELECT COUNT(*) FROM sessions WHERE id='$1' AND pause_json!='';")" == 1 ]] || die "pause cleared automatically"
}

assert_handoff_count() {
	guard; safe_atom "${1:-}"; [[ "${2:-}" =~ ^[0-9]+$ ]] || die "count invalid"
	local db got marker='Host-compiled handoff'; db="$(db_path)"
	got="$(sqlite3 "$db" "SELECT (length(prompt)-length(replace(prompt,'$marker','')))/length('$marker') FROM sessions WHERE id='$1';")"
	[[ "$got" == "$2" ]] || die "handoff marker count=$got want=$2"
}

assert_worker_address() {
	guard; safe_atom "${1:-}"; safe_atom "${2:-}"; local db; db="$(db_path)"
	local expected_file="$AO_ACCEPTANCE_ROOT/control/expected-orchestrator"
	local evidence="$AO_ACCEPTANCE_ROOT/evidence/provider-addresses.log"
	[[ -f "$expected_file" && "$(tr -d '[:space:]' <"$expected_file")" == "$2" ]] ||
		die "worker launch did not pin expected orchestrator $2"
	[[ -f "$evidence" ]] || die "provider address evidence missing: $evidence"
	awk -F '\t' -v worker="$1" -v orchestrator="$2" \
		'$2 == worker && $3 == orchestrator && $4 == "confirmed=true" { found=1 } END { exit !found }' "$evidence" ||
		die "worker $1 launch did not confirm orchestrator address $2"
	local project; project="$(sqlite3 "$db" "SELECT project_id FROM sessions WHERE id='$1';")"
	assert_owner "$project" "$2"
}

seed_requested() {
	guard
	local sid="${1:-}" incident="${2:-}" target="${3:-}" model="${4:-}" rung="${5:-}" gen="${6:-}"
	safe_atom "$sid"; safe_atom "$incident"; safe_atom "$target"; safe_atom "$gen"
	[[ "$model" =~ ^[A-Za-z0-9._:-]*$ ]] || die "unsafe model"; [[ "$rung" =~ ^[0-9]+$ ]] || die "rung invalid"
	if [[ -f "$AO_RUN_FILE" ]]; then
		local pid; pid="$(jq -r '.pid // 0' "$AO_RUN_FILE" 2>/dev/null || printf 0)"
		if [[ "$pid" =~ ^[0-9]+$ ]] && ((pid > 1)) && kill -0 "$pid" 2>/dev/null; then die "seed requires daemon stopped"; fi
	fi
	local db; db="$(db_path)"
	[[ "$(sqlite3 "$db" "SELECT COUNT(*) FROM sessions WHERE id='$sid' AND json_extract(pause_json,'$.incidentId')='$incident';")" == 1 ]] || die "session is not paused for incident"
	[[ "$(sqlite3 "$db" "SELECT COUNT(*) FROM session_failover_attempts WHERE session_id='$sid' AND incident_id='$incident' AND state IN ('requested','post_stop');")" == 0 ]] || die "active attempt already exists"
	local seq; seq="$(sqlite3 "$db" "SELECT COALESCE(MAX(seq),0)+1 FROM session_failover_attempts WHERE session_id='$sid' AND incident_id='$incident';")"
	sqlite3 "$db" <<SQL
BEGIN IMMEDIATE;
INSERT INTO lifecycle_ledger(id,session_id,project_id,kind,phase,generation_id,from_harness,to_harness,from_model,to_model,role_id,payload_json,created_at)
SELECT '$sid:$incident:failover:$seq:requested',id,project_id,'failover','requested','$gen',harness,'$target',resolved_model,'$model',role_id,
       json_object('incidentId','$incident','attemptSeq',$seq,'rungIndex',$rung,'state','requested'),CURRENT_TIMESTAMP
FROM sessions WHERE id='$sid';
INSERT INTO session_failover_attempts(id,session_id,project_id,incident_id,seq,role_id,from_harness,from_model,to_harness,to_model,rung_index,generation_id,state,created_at,updated_at)
SELECT '$sid:$incident:$seq',id,project_id,'$incident',$seq,role_id,harness,resolved_model,'$target','$model',$rung,'$gen','requested',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP
FROM sessions WHERE id='$sid';
COMMIT;
SQL
	printf 'seeded atomic requested attempt seq=%s generation=%s\n' "$seq" "$gen"
}

capabilities() { need go; (cd "$repo_root/backend" && go test ./test/mvp_acceptance -run '^TestCapabilityMatrixMVP$' -count=1 -v); }

cmd="${1:-}"; shift || true
case "$cmd" in
init) init_root "$@" ;;
check-env) guard; printf 'isolated environment accepted: %s\n' "$AO_ACCEPTANCE_ROOT" ;;
self-test) self_test ;;
build) build_binary ;;
start) start_daemon ;;
crash) crash_daemon ;;
stop) stop_daemon ;;
setup-project) setup_project ;;
spawn) spawn_session "$@" ;;
pause) pause_session "$@" ;;
continue) continue_session "$@" ;;
switch) switch_session "$@" ;;
fresh) fresh_session "$@" ;;
send-expect-fenced) send_expect_fenced "$@" ;;
mux-expect-fenced) mux_expect_fenced "$@" ;;
fault) fault_control "$@" ;;
provider) provider_control "$@" ;;
terminate-runtime) terminate_runtime "$@" ;;
snapshot) snapshot "$@" ;;
wait-attempt) wait_attempt "$@" ;;
wait-phase) wait_phase "$@" ;;
wait-pending) wait_pending "$@" ;;
wait-runtime) wait_runtime "$@" ;;
assert-preserved) assert_preserved "$@" ;;
assert-runtime) assert_runtime "$@" ;;
assert-owner) assert_owner "$@" ;;
assert-ledger) assert_ledger "$@" ;;
assert-attempt) assert_attempt "$@" ;;
assert-passive) assert_passive "$@" ;;
assert-no-auto) assert_no_auto "$@" ;;
assert-handoff-count) assert_handoff_count "$@" ;;
assert-worker-address) assert_worker_address "$@" ;;
seed-requested) seed_requested "$@" ;;
capabilities) capabilities ;;
-h | --help | help | '') usage ;;
*) die "unknown command: $cmd" ;;
esac
