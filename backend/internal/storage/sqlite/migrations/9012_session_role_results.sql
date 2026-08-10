-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN role_result_state TEXT NOT NULL DEFAULT ''
    CHECK (role_result_state IN ('', 'completed', 'blocked', 'failed'));
ALTER TABLE sessions ADD COLUMN role_result_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN role_result_reported_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN role_result_generation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN role_result_current INTEGER NOT NULL DEFAULT 0
    CHECK (role_result_current IN (0, 1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN role_result_current;
ALTER TABLE sessions DROP COLUMN role_result_generation_id;
ALTER TABLE sessions DROP COLUMN role_result_reported_at;
ALTER TABLE sessions DROP COLUMN role_result_summary;
ALTER TABLE sessions DROP COLUMN role_result_state;
-- +goose StatementEnd
