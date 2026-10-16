-- name: CreateSession :one
INSERT INTO sessions (
    id,
    parent_session_id,
    title,
    message_count,
    prompt_tokens,
    completion_tokens,
    cost,
    summary_message_id,
    updated_at,
    created_at
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    null,
    strftime('%s', 'now'),
    strftime('%s', 'now')
) RETURNING *;

-- name: GetSessionByID :one
SELECT *
FROM sessions
WHERE id = ? LIMIT 1;

-- name: GetLastSession :one
SELECT *
FROM sessions
ORDER BY updated_at DESC
LIMIT 1;

-- name: ListSessions :many
SELECT *
FROM sessions
WHERE parent_session_id is NULL
ORDER BY updated_at DESC;

-- name: UpdateSession :one
UPDATE sessions
SET
    title = ?,
    prompt_tokens = ?,
    completion_tokens = ?,
    total_cache_read_tokens = ?,
    total_context_tokens = ?,
    summary_message_id = ?,
    summary_cut_message_id = ?,
    cost = ?,
    todos = ?,
    channel = ?
WHERE id = ?
RETURNING *;

-- name: SetSessionChannel :one
UPDATE sessions
SET channel = ?
WHERE id = ?
RETURNING *;

-- name: AddSessionUsage :exec
UPDATE sessions
SET
    total_cache_read_tokens = total_cache_read_tokens + ?,
    total_context_tokens = total_context_tokens + ?,
    cost = cost + ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;

-- name: AddStepUsage :one
UPDATE sessions
SET
    prompt_tokens = ?,
    completion_tokens = ?,
    total_cache_read_tokens = total_cache_read_tokens + ?,
    total_context_tokens = total_context_tokens + ?,
    cost = cost + ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?
RETURNING *;

-- name: UpdateSessionTodos :exec
UPDATE sessions
SET
    todos = ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;

-- name: UpdateSessionTitleIfCurrent :exec
UPDATE sessions
SET
    title = ?,
    updated_at = strftime('%s', 'now')
WHERE id = ? AND title = ?;


-- name: RenameSession :exec
UPDATE sessions
SET
    title = ?
WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;
