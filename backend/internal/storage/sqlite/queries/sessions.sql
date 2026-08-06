-- name: NextSessionNum :one
SELECT COALESCE(MAX(num), 0) + 1 AS next FROM sessions WHERE project_id = ?;

-- name: InsertSession :exec
INSERT INTO sessions (
    id, project_id, num, issue_id, kind, harness,
    role_id, role_map_schema_version, role_map_sha256, role_config_revision,
    template_artifact_id, template_sha256, resolved_model,
    resolved_workspace_writes, resolved_can_spawn, spawn_capability_hash, display_name,
    activity_state, activity_last_at, first_signal_at, is_terminated,
    branch, workspace_path, workspace_repo_path, diff_base_sha, diff_base_ref, runtime_handle_id,
    runtime_launch_id, agent_session_id, prompt, switch_pending_json, pause_json,
    preview_url, preview_revision, terminate_on_pr_merge, cleanup_generation,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateSession :exec
UPDATE sessions SET
    issue_id = ?, kind = ?, harness = ?,
    role_id = ?, role_map_schema_version = ?, role_map_sha256 = ?, role_config_revision = ?,
    template_artifact_id = ?, template_sha256 = ?, resolved_model = ?,
    resolved_workspace_writes = ?, resolved_can_spawn = ?, spawn_capability_hash = ?, display_name = ?,
    activity_state = ?, activity_last_at = ?, first_signal_at = ?, is_terminated = ?,
    branch = ?, workspace_path = ?, workspace_repo_path = ?, diff_base_sha = ?, diff_base_ref = ?, runtime_handle_id = ?,
    runtime_launch_id = ?, agent_session_id = ?, prompt = ?, switch_pending_json = ?,
    preview_url = ?, preview_revision = ?, terminate_on_pr_merge = ?,
    cleanup_generation = ?, updated_at = ?
WHERE id = ?;

-- name: GetSession :one
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, switch_pending_json, pause_json,
    created_at, updated_at, display_name, first_signal_at, preview_url,
    preview_revision, cleanup_generation, runtime_launch_id,
    workspace_repo_path, terminate_on_pr_merge, diff_base_sha, diff_base_ref,
    role_id, role_map_schema_version, role_map_sha256, role_config_revision,
    template_artifact_id, template_sha256, resolved_model,
    resolved_workspace_writes, resolved_can_spawn, spawn_capability_hash
FROM sessions WHERE id = ?;

-- name: GetSessionByRuntimeHandleID :one
-- Terminal mux keys panes by runtime handle (tmux session name), not always SessionID.
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, switch_pending_json, pause_json,
    created_at, updated_at, display_name, first_signal_at, preview_url,
    preview_revision, cleanup_generation, runtime_launch_id,
    workspace_repo_path, terminate_on_pr_merge, diff_base_sha, diff_base_ref,
    role_id, role_map_schema_version, role_map_sha256, role_config_revision,
    template_artifact_id, template_sha256, resolved_model,
    resolved_workspace_writes, resolved_can_spawn, spawn_capability_hash
FROM sessions WHERE runtime_handle_id = ? LIMIT 1;

-- name: GetSessionByPendingSourceHandle :one
-- After source destroy, RuntimeHandleID is cleared but pending still records the
-- pre-stop handle for terminal ownership fencing.
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, switch_pending_json, pause_json,
    created_at, updated_at, display_name, first_signal_at, preview_url,
    preview_revision, cleanup_generation, runtime_launch_id,
    workspace_repo_path, terminate_on_pr_merge, diff_base_sha, diff_base_ref,
    role_id, role_map_schema_version, role_map_sha256, role_config_revision,
    template_artifact_id, template_sha256, resolved_model,
    resolved_workspace_writes, resolved_can_spawn, spawn_capability_hash
FROM sessions
WHERE switch_pending_json != ''
  AND json_extract(switch_pending_json, '$.sourceRuntimeHandleId') = ?
LIMIT 1;

-- name: ListSessionsByProject :many
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, switch_pending_json, pause_json,
    created_at, updated_at, display_name, first_signal_at, preview_url,
    preview_revision, cleanup_generation, runtime_launch_id,
    workspace_repo_path, terminate_on_pr_merge, diff_base_sha, diff_base_ref,
    role_id, role_map_schema_version, role_map_sha256, role_config_revision,
    template_artifact_id, template_sha256, resolved_model,
    resolved_workspace_writes, resolved_can_spawn, spawn_capability_hash
FROM sessions WHERE project_id = ? ORDER BY num;

-- name: ListAllSessions :many
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, switch_pending_json, pause_json,
    created_at, updated_at, display_name, first_signal_at, preview_url,
    preview_revision, cleanup_generation, runtime_launch_id,
    workspace_repo_path, terminate_on_pr_merge, diff_base_sha, diff_base_ref,
    role_id, role_map_schema_version, role_map_sha256, role_config_revision,
    template_artifact_id, template_sha256, resolved_model,
    resolved_workspace_writes, resolved_can_spawn, spawn_capability_hash
FROM sessions ORDER BY project_id, num;


-- name: RenameSession :execrows
UPDATE sessions SET display_name = ?, updated_at = ? WHERE id = ?;

-- name: SetSessionPreviewURL :execrows
-- preview_revision is bumped on every call (even when preview_url is unchanged)
-- so a repeated `ao preview <same-url>` still trips the sessions_cdc_update
-- trigger and the desktop browser panel re-navigates / refreshes.
UPDATE sessions SET preview_url = ?, preview_revision = preview_revision + 1, updated_at = ? WHERE id = ?;

-- name: SetSessionTerminateOnPRMerge :execrows
UPDATE sessions SET terminate_on_pr_merge = ?, updated_at = ? WHERE id = ?;

-- name: SessionIsSeed :one
-- SessionIsSeed reports whether the session id matches a row still in seed
-- state (see DeleteSeedSession for the conditions). Callers probe with this
-- before touching change_log so that DeleteSession is a true no-op for live
-- sessions instead of silently destroying their CDC events. Returns 0 when
-- the row does not exist OR has progressed past seed state.
SELECT EXISTS(
    SELECT 1 FROM sessions
    WHERE id = ?
      AND is_terminated = 0
      AND workspace_path = ''
      AND runtime_handle_id = ''
      AND agent_session_id = ''
      AND prompt = ''
) AS is_seed;

-- NOTE: the `DELETE FROM sessions WHERE id = ? AND <seed-state predicates>`
-- statement is intentionally NOT a sqlc query — same sqlc 1.31 SQLite-parser
-- bug as documented in queries/changelog.sql: trailing string literals (and
-- placeholders) on the RHS of `=` in a DELETE get silently stripped, so the
-- generated SQL ends up mid-clause and the row count is meaningless. The
-- store runs that DELETE directly via tx.ExecContext inside
-- Store.DeleteSession, inside the same transaction as the SessionIsSeed
-- probe and the raw change_log cleanup.

-- NOTE: the two conditional pause_json writes (set-if-absent, clear-if-incident)
-- are deliberately NOT sqlc queries — same sqlc 1.31 SQLite-parser bug as the
-- DELETE above. Literals on the RHS of `=` (`pause_json = ''`, `is_terminated =
-- 0`) get silently stripped, and the truncated tail leaks into the NEXT
-- generated const. The store runs both directly via ExecContext; see
-- SetSessionPauseIfAbsent / ClearSessionPauseIfIncident in session_store.go.
