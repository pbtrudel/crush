-- +goose Up
ALTER TABLE sessions ADD COLUMN summary_cut_message_id TEXT;

-- +goose Down
ALTER TABLE sessions DROP COLUMN summary_cut_message_id;
