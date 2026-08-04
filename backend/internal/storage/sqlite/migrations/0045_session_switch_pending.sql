-- +goose Up
-- +goose StatementBegin
-- Durable in-flight worker switch/fresh pin. Current harness/model remain the
-- source until target_ack; non-empty JSON means input is gated (sessionguard).
ALTER TABLE sessions ADD COLUMN switch_pending_json TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN switch_pending_json;
-- +goose StatementEnd
