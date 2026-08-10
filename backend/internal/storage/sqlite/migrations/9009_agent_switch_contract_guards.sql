-- Migration 9009: enforce the durable worker-switch contract below the Go
-- store. 0085 is an immutable upstream migration, so fork-specific state and
-- recovery guards live in the fork range rather than rewriting it.

-- +goose Up
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
CREATE TRIGGER agent_switches_state_transition_guard
BEFORE UPDATE OF state ON agent_switches
WHEN NOT (
    (NEW.state = OLD.state AND OLD.state NOT IN ('completed', 'failed'))
    OR (NEW.state = 'failed' AND OLD.state NOT IN ('completed', 'failed'))
    OR (OLD.state = 'preparing_handoff' AND NEW.state = 'stopping_source')
    OR (OLD.state = 'stopping_source' AND NEW.state = 'source_stopped')
    OR (OLD.state = 'source_stopped' AND NEW.state = 'starting_target')
    OR (OLD.state = 'starting_target' AND NEW.state = 'target_ready')
    OR (OLD.state = 'target_ready' AND NEW.state = 'delivering_context')
    OR (OLD.state = 'delivering_context' AND NEW.state = 'completed')
)
BEGIN
    SELECT RAISE(ABORT, 'invalid agent switch state transition');
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

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_switches_recovery_tuple_guard;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_switches_state_transition_guard;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_switches_provenance_immutable;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_switches_initial_tuple_guard;
-- +goose StatementEnd
