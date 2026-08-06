-- +goose Up
-- +goose StatementBegin
-- Durable pause pin (Phase 3A, MASTER_PLAN §7). Non-empty JSON means AO must
-- not write to the session's pane on its own initiative; only an explicit
-- resume clears it. Stored as JSON alongside switch_pending_json rather than as
-- typed columns because, like that pin, it is read as a whole or not at all and
-- its shape grows with 3B (failover cursor, incident bounds).
ALTER TABLE sessions ADD COLUMN pause_json TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN pause_json;
-- +goose StatementEnd
