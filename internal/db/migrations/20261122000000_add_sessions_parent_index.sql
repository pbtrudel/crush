-- +goose Up
-- +goose StatementBegin
-- The web snapshot walks a session tree one child list at a time. Without
-- this, every step scans the whole sessions table.
CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions (parent_session_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sessions_parent;
-- +goose StatementEnd
