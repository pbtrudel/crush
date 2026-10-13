-- +goose Up
ALTER TABLE sessions ADD COLUMN total_cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN total_context_tokens INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sessions DROP COLUMN total_cache_read_tokens;
ALTER TABLE sessions DROP COLUMN total_context_tokens;
