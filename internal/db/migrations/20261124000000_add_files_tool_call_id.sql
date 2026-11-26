-- +goose Up
-- +goose StatementBegin
ALTER TABLE files ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
-- The web drawer lists every version of a session's files without their
-- content. Columns after content sit behind its overflow pages, so the
-- listing reads them from this index instead.
CREATE INDEX IF NOT EXISTS idx_files_session_versions
    ON files (session_id, version, created_at, updated_at, path, id, tool_call_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_files_session_versions;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE files DROP COLUMN tool_call_id;
-- +goose StatementEnd
