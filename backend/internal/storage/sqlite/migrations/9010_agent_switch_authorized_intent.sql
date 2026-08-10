-- Migration 9010: persist the exact role-authorized worker-switch intent.
-- 0085 and 9009 are immutable; this migration advances their guards so the
-- caller-owned target generation exists from the saga's first durable write.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE agent_switches ADD COLUMN target_model TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE agent_switches ADD COLUMN role_snapshot_json TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE agent_switches ADD COLUMN failover_attempt_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE session_failover_attempts ADD COLUMN source_generation_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- Existing attempt rows remain readable for legacy recovery, while every new
-- attempt must carry the source fence and may never rewrite it.
-- +goose StatementBegin
CREATE TRIGGER session_failover_attempts_source_generation_insert_guard
BEFORE INSERT ON session_failover_attempts
WHEN NEW.source_generation_id = ''
BEGIN
    SELECT RAISE(ABORT, 'failover attempt source generation is required');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_failover_attempts_source_generation_immutable
BEFORE UPDATE ON session_failover_attempts
WHEN NEW.source_generation_id IS NOT OLD.source_generation_id
BEGIN
    SELECT RAISE(ABORT, 'failover attempt source generation is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_switches_initial_tuple_guard;
DROP TRIGGER IF EXISTS agent_switches_provenance_immutable;
DROP TRIGGER IF EXISTS agent_switches_recovery_tuple_guard;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER agent_switches_initial_tuple_guard
BEFORE INSERT ON agent_switches
WHEN NEW.state <> 'preparing_handoff'
    OR NEW.target_generation_id = ''
    OR NEW.role_snapshot_json = ''
    OR json_valid(NEW.role_snapshot_json) <> 1
    OR NEW.target_native_session_ref IS NOT NULL
    OR NEW.target_start_mode <> ''
    OR NEW.target_runtime_handle_id <> ''
    OR NEW.target_acknowledged_at IS NOT NULL
    OR NEW.agent_handoff_status <> 'not_attempted'
    OR NEW.source_transcript_status <> 'not_attempted'
    OR NEW.semantic_handoff_included <> 0
    OR NEW.agent_handoff_path <> ''
    OR NEW.agent_handoff_hash <> ''
    OR NEW.final_handoff_path <> ''
    OR NEW.final_handoff_hash <> ''
    OR NEW.error_code <> ''
BEGIN
    SELECT RAISE(ABORT, 'agent switch initial authorized intent mismatch');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER agent_switches_provenance_immutable
BEFORE UPDATE ON agent_switches
WHEN NEW.session_id IS NOT OLD.session_id
    OR NEW.idempotency_key IS NOT OLD.idempotency_key
    OR NEW.request_fingerprint IS NOT OLD.request_fingerprint
    OR NEW.from_harness IS NOT OLD.from_harness
    OR NEW.target_harness IS NOT OLD.target_harness
    OR NEW.target_model IS NOT OLD.target_model
    OR NEW.role_snapshot_json IS NOT OLD.role_snapshot_json
    OR NEW.failover_attempt_id IS NOT OLD.failover_attempt_id
    OR NEW.source_generation_id IS NOT OLD.source_generation_id
    OR NEW.target_generation_id IS NOT OLD.target_generation_id
    OR NEW.requested_at IS NOT OLD.requested_at
BEGIN
    SELECT RAISE(ABORT, 'agent switch authorized provenance is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER agent_switches_recovery_tuple_guard
BEFORE UPDATE ON agent_switches
WHEN NEW.updated_at < OLD.updated_at
    OR (NEW.target_start_mode <> '' AND NEW.target_generation_id = '')
    OR (
        NEW.state IN (
            'stopping_source', 'source_stopped', 'starting_target',
            'target_ready', 'delivering_context', 'completed'
        )
        AND (NEW.target_generation_id = '' OR NEW.target_start_mode = '')
    )
    OR (
        NEW.state IN ('target_ready', 'delivering_context', 'completed')
        AND (NEW.target_native_session_ref IS NULL OR NEW.target_runtime_handle_id = '')
    )
    OR (
        NEW.target_acknowledged_at IS NOT NULL
        AND NEW.state NOT IN ('delivering_context', 'completed')
    )
    OR (NEW.state = 'completed' AND NEW.target_acknowledged_at IS NULL)
    OR ((NEW.final_handoff_path = '') <> (NEW.final_handoff_hash = ''))
    OR (
        NEW.final_handoff_hash <> ''
        AND (
            length(NEW.final_handoff_hash) <> 64
            OR NEW.final_handoff_hash GLOB '*[^0-9a-f]*'
        )
    )
    OR (
        NEW.semantic_handoff_included = 1
        AND (
            NEW.agent_handoff_status <> 'received'
            OR NEW.final_handoff_path = ''
            OR NEW.agent_handoff_path <> NEW.final_handoff_path
            OR NEW.agent_handoff_hash <> NEW.final_handoff_hash
        )
    )
BEGIN
    SELECT RAISE(ABORT, 'agent switch recovery tuple mismatch');
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_switches_recovery_tuple_guard;
DROP TRIGGER IF EXISTS agent_switches_provenance_immutable;
DROP TRIGGER IF EXISTS agent_switches_initial_tuple_guard;
DROP TRIGGER IF EXISTS session_failover_attempts_source_generation_immutable;
DROP TRIGGER IF EXISTS session_failover_attempts_source_generation_insert_guard;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE agent_switches DROP COLUMN failover_attempt_id;
ALTER TABLE agent_switches DROP COLUMN role_snapshot_json;
ALTER TABLE agent_switches DROP COLUMN target_model;
ALTER TABLE session_failover_attempts DROP COLUMN source_generation_id;
-- +goose StatementEnd

-- Restore the immutable 9009 guards exactly for a deliberate rollback.
-- +goose StatementBegin
CREATE TRIGGER agent_switches_initial_tuple_guard
BEFORE INSERT ON agent_switches
WHEN NEW.state <> 'preparing_handoff'
    OR NEW.target_native_session_ref IS NOT NULL
    OR NEW.target_start_mode <> ''
    OR NEW.target_generation_id <> ''
    OR NEW.target_runtime_handle_id <> ''
    OR NEW.target_acknowledged_at IS NOT NULL
    OR NEW.agent_handoff_status <> 'not_attempted'
    OR NEW.source_transcript_status <> 'not_attempted'
    OR NEW.semantic_handoff_included <> 0
    OR NEW.agent_handoff_path <> ''
    OR NEW.agent_handoff_hash <> ''
    OR NEW.final_handoff_path <> ''
    OR NEW.final_handoff_hash <> ''
    OR NEW.error_code <> ''
BEGIN
    SELECT RAISE(ABORT, 'agent switch initial tuple mismatch');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER agent_switches_provenance_immutable
BEFORE UPDATE ON agent_switches
WHEN NEW.session_id IS NOT OLD.session_id
    OR NEW.idempotency_key IS NOT OLD.idempotency_key
    OR NEW.request_fingerprint IS NOT OLD.request_fingerprint
    OR NEW.from_harness IS NOT OLD.from_harness
    OR NEW.target_harness IS NOT OLD.target_harness
    OR NEW.source_generation_id IS NOT OLD.source_generation_id
    OR NEW.requested_at IS NOT OLD.requested_at
BEGIN
    SELECT RAISE(ABORT, 'agent switch provenance is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER agent_switches_recovery_tuple_guard
BEFORE UPDATE ON agent_switches
WHEN NEW.updated_at < OLD.updated_at
    OR (OLD.target_generation_id <> '' AND NEW.target_generation_id <> OLD.target_generation_id)
    OR ((NEW.target_generation_id = '') <> (NEW.target_start_mode = ''))
    OR (
        NEW.state IN (
            'stopping_source', 'source_stopped', 'starting_target',
            'target_ready', 'delivering_context', 'completed'
        )
        AND (NEW.target_generation_id = '' OR NEW.target_start_mode = '')
    )
    OR (
        NEW.state IN ('target_ready', 'delivering_context', 'completed')
        AND (NEW.target_native_session_ref IS NULL OR NEW.target_runtime_handle_id = '')
    )
    OR (
        NEW.target_acknowledged_at IS NOT NULL
        AND NEW.state NOT IN ('delivering_context', 'completed')
    )
    OR (NEW.state = 'completed' AND NEW.target_acknowledged_at IS NULL)
    OR ((NEW.final_handoff_path = '') <> (NEW.final_handoff_hash = ''))
    OR (
        NEW.final_handoff_hash <> ''
        AND (
            length(NEW.final_handoff_hash) <> 64
            OR NEW.final_handoff_hash GLOB '*[^0-9a-f]*'
        )
    )
    OR (
        NEW.semantic_handoff_included = 1
        AND (
            NEW.agent_handoff_status <> 'received'
            OR NEW.final_handoff_path = ''
            OR NEW.agent_handoff_path <> NEW.final_handoff_path
            OR NEW.agent_handoff_hash <> NEW.final_handoff_hash
        )
    )
BEGIN
    SELECT RAISE(ABORT, 'agent switch recovery tuple mismatch');
END;
-- +goose StatementEnd
