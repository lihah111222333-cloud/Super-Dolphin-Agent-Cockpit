-- name: GetCommandCard :one
SELECT id, card_key, title, description, command_template, CAST(args_schema AS BLOB) AS args_schema, risk_level, enabled, created_by, updated_by, created_at, updated_at
FROM command_cards
WHERE card_key = ?;

-- name: DeleteCommandCard :execrows
DELETE FROM command_cards
WHERE card_key = ?;

-- name: InsertCommandCardVersion :exec
INSERT INTO command_card_versions (
    card_key, title, description, command_template, args_schema,
    risk_level, enabled, created_by, updated_by, source_updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListCommandCardVersions :many
SELECT id, card_key, title, description, command_template, CAST(args_schema AS BLOB) AS args_schema, risk_level, enabled,
       created_by, updated_by, source_updated_at, created_at, archived_at
FROM command_card_versions
WHERE card_key = ?
ORDER BY id DESC;

-- name: UpsertCommandCard :one
INSERT INTO command_cards (
    card_key, title, description, command_template, args_schema,
    risk_level, enabled, created_by, updated_by, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (card_key) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    command_template = EXCLUDED.command_template,
    args_schema = EXCLUDED.args_schema,
    risk_level = EXCLUDED.risk_level,
    enabled = EXCLUDED.enabled,
    updated_by = EXCLUDED.updated_by,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING id, card_key, title, description, command_template, CAST(args_schema AS BLOB) AS args_schema, risk_level, enabled, created_by, updated_by, created_at, updated_at;

-- name: ListCommandCards :many
SELECT c.id, c.card_key, c.title, c.description, c.command_template, CAST(c.args_schema AS BLOB) AS args_schema, c.risk_level, c.enabled, c.created_by, c.updated_by, c.created_at, c.updated_at,
       stats.last_run_at, COALESCE(stats.run_count, 0) AS run_count
FROM command_cards AS c
LEFT JOIN (
    SELECT card_key, MAX(created_at) AS last_run_at, COUNT(*) AS run_count
    FROM command_card_runs
    GROUP BY card_key
) AS stats ON stats.card_key = c.card_key
WHERE (sqlc.arg(keyword) = ''
    OR c.card_key LIKE '%' || sqlc.arg(keyword) || '%'
    OR c.title LIKE '%' || sqlc.arg(keyword) || '%'
    OR c.description LIKE '%' || sqlc.arg(keyword) || '%'
    OR c.command_template LIKE '%' || sqlc.arg(keyword) || '%')
ORDER BY c.updated_at DESC, c.id DESC
LIMIT sqlc.arg(limit_count);
