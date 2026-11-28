-- +goose Up
-- +goose StatementBegin
-- The gateway pages a session one page at a time, newest first for the
-- snapshot and backwards for the load-older button, and the settled path
-- still reads the whole transcript oldest first. session_id first so each
-- read can seek; created_at second so the rows arrive already ordered and
-- neither ORDER BY needs a sort. rowid, the tie-break both orders use, is
-- this index's row locator.
CREATE INDEX IF NOT EXISTS idx_messages_session_created_at ON messages (session_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_messages_session_created_at;
-- +goose StatementEnd
