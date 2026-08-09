-- Migration 9011: retain the exact authorized role snapshot across the small
-- durable window between failover-attempt admission and switch-saga creation.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE session_failover_attempts ADD COLUMN role_snapshot_json TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- Upgraded legacy attempts may retain an empty snapshot for conservative
-- legacy recovery. Every new attempt must carry valid immutable JSON.
-- +goose StatementBegin
CREATE TRIGGER session_failover_attempts_role_snapshot_insert_guard
BEFORE INSERT ON session_failover_attempts
WHEN NEW.role_snapshot_json = '' OR json_valid(NEW.role_snapshot_json) <> 1
BEGIN
    SELECT RAISE(ABORT, 'failover attempt role snapshot is required');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_failover_attempts_role_snapshot_immutable
BEFORE UPDATE ON session_failover_attempts
WHEN NEW.role_snapshot_json IS NOT OLD.role_snapshot_json
BEGIN
    SELECT RAISE(ABORT, 'failover attempt role snapshot is immutable');
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_failover_attempts_role_snapshot_immutable;
DROP TRIGGER IF EXISTS session_failover_attempts_role_snapshot_insert_guard;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE session_failover_attempts DROP COLUMN role_snapshot_json;
-- +goose StatementEnd
