-- +goose Up
-- +goose StatementBegin
-- idx_files_session_versions leads with session_id, so it already serves
-- every lookup by session_id alone. The single-column index adds nothing
-- but a b-tree write per insert.
DROP INDEX IF EXISTS idx_files_session_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_files_session_id ON files (session_id);
-- +goose StatementEnd
