-- +goose Up
-- +goose StatementBegin
-- Per-session spawn capability: only SHA-256 of the random token is stored.
-- Plaintext is process-local (AO_SPAWN_CAPABILITY env), never a global mint key.
ALTER TABLE sessions ADD COLUMN spawn_capability_hash TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN spawn_capability_hash;
-- +goose StatementEnd
