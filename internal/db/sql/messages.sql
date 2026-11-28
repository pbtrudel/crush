-- name: GetMessage :one
SELECT *
FROM messages
WHERE id = ? LIMIT 1;

-- name: ListMessagesBySession :many
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY created_at ASC, rowid ASC;

-- name: ListMessagesBySessionFromSummary :many
-- Messages from the summary onward, which is all a compacted session sends.
-- created_at has one-second resolution, so a few messages preceding the
-- summary can come back too; the caller slices from the summary by ID.
SELECT m.*
FROM messages m
WHERE m.session_id = ?
  AND m.created_at >= (SELECT s.created_at FROM messages s WHERE s.id = ?)
ORDER BY m.created_at ASC, m.rowid ASC;

-- name: ListMessagesBySessionFromCut :many
-- Messages from the compaction cut point onward: the verbatim tail kept by
-- a compaction, the summary message itself, and everything created after
-- it. rowid (insertion order) is exact, unlike created_at, whose
-- one-second resolution cannot separate same-second inserts.
SELECT m.*
FROM messages m
WHERE m.session_id = ?
  AND m.rowid >= (SELECT c.rowid FROM messages c WHERE c.id = ?)
ORDER BY m.rowid ASC;

-- name: CreateMessage :one
INSERT INTO messages (
    id,
    session_id,
    role,
    parts,
    model,
    model_name,
    provider,
    is_summary_message,
    created_at,
    updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now'), strftime('%s', 'now')
)
RETURNING *;

-- name: UpdateMessage :exec
UPDATE messages
SET
    parts = ?,
    model_name = ?,
    prism_model_id = ?,
    prism_model_name = ?,
    prism_hypercredit_savings = ?,
    prism_dollar_savings = ?,
    finished_at = ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;


-- name: DeleteMessage :exec
DELETE FROM messages
WHERE id = ?;

-- name: DeleteSessionMessages :exec
DELETE FROM messages
WHERE session_id = ?;

-- name: ListUserMessagesBySession :many
-- Backs prompt history, which steps back one entry at a time.
SELECT *
FROM messages
WHERE session_id = ? AND role = 'user'
ORDER BY created_at DESC
LIMIT 200;

-- name: ListAllUserMessages :many
-- Backs prompt history when no session is open. Needs
-- idx_messages_role_created_at to seek rather than scan the table.
-- Child sessions are excluded: a subagent's task prompt is a user
-- message too, but the user never typed it, so it has no place in the
-- prompt history.
SELECT m.*
FROM messages m
JOIN sessions s ON s.id = m.session_id
WHERE m.role = 'user' AND s.parent_session_id IS NULL
ORDER BY m.created_at DESC
LIMIT 200;

-- name: GetLastAssistantMessageBySession :one
SELECT *
FROM messages
WHERE session_id = ? AND role = 'assistant' AND is_summary_message = 0
ORDER BY created_at DESC
LIMIT 1;

-- name: ListLatestMessagesPage :many
-- The newest page of a session: the gateway's load-older button walks
-- backwards from it one page at a time. Newest first, so a LIMIT keeps the
-- newest rows; the caller reverses the page. created_at has one-second
-- resolution, so rowid breaks ties exactly as ListMessagesBySession does.
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY created_at DESC, rowid DESC
LIMIT ?;

-- name: ListMessagesPageBefore :many
-- One page of a session older than the given message, newest first; the
-- caller reverses it. The row-value comparison matches ListMessagesBySession's
-- created_at, rowid order exactly, so paging visits the same rows in the same
-- order as reading the whole transcript. The caller asks for one row more
-- than the page to learn whether older messages remain.
SELECT m.*
FROM messages m
WHERE m.session_id = ?
  AND (m.created_at, m.rowid) < (SELECT b.created_at, b.rowid FROM messages b WHERE b.id = ?)
ORDER BY m.created_at DESC, m.rowid DESC
LIMIT ?;

-- name: ListUnfinishedAssistants :many
-- Where a settle pass starts: the assistant rows that never finished. A
-- summary is not a turn, so it is never settled. finished_at is written from
-- the message's finish part, so a NULL means the turn never ended.
SELECT *
FROM messages
WHERE session_id = ?
  AND role = 'assistant'
  AND is_summary_message = 0
  AND finished_at IS NULL
ORDER BY created_at ASC, rowid ASC;

-- name: ListToolMessagesFrom :many
-- The tool results that could answer a call made by one of the session's
-- unfinished assistant rows. A result is created after the assistant row that
-- made the call, so from_created_at (the oldest unfinished assistant's
-- created_at) bounds the read and a healthy session reads no tool row at all.
-- The bound is inclusive because created_at has one-second resolution.
SELECT *
FROM messages
WHERE session_id = ?
  AND role = 'tool'
  AND created_at >= ?
ORDER BY created_at ASC, rowid ASC;
