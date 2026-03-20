-- name: GetCommandCard :one
SELECT id, card_key, title, description, command_template, args_schema, risk_level, enabled, created_by, updated_by, created_at, updated_at
FROM command_cards
WHERE card_key = $1;

-- name: InsertCommandCardVersion :exec
INSERT INTO command_card_versions (
    card_key, title, description, command_template, args_schema,
    risk_level, enabled, created_by, updated_by, source_updated_at
) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, $10);

-- name: UpsertCommandCard :one
INSERT INTO command_cards (
    card_key, title, description, command_template, args_schema,
    risk_level, enabled, created_by, updated_by, updated_at
) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, NOW())
ON CONFLICT (card_key) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    command_template = EXCLUDED.command_template,
    args_schema = EXCLUDED.args_schema,
    risk_level = EXCLUDED.risk_level,
    enabled = EXCLUDED.enabled,
    updated_by = EXCLUDED.updated_by,
    updated_at = NOW()
RETURNING id, card_key, title, description, command_template, args_schema, risk_level, enabled, created_by, updated_by, created_at, updated_at;

-- name: ListCommandCards :many
SELECT c.id, c.card_key, c.title, c.description, c.command_template, c.args_schema, c.risk_level, c.enabled, c.created_by, c.updated_by, c.created_at, c.updated_at,
       stats.last_run_at, COALESCE(stats.run_count, 0)::bigint AS run_count
FROM command_cards AS c
LEFT JOIN (
    SELECT card_key, MAX(created_at) AS last_run_at, COUNT(*)::bigint AS run_count
    FROM command_card_runs
    GROUP BY card_key
) AS stats ON stats.card_key = c.card_key
WHERE ($1::text = ''
    OR c.card_key ILIKE '%' || $1 || '%'
    OR c.title ILIKE '%' || $1 || '%'
    OR c.description ILIKE '%' || $1 || '%'
    OR c.command_template ILIKE '%' || $1 || '%')
ORDER BY c.updated_at DESC, c.id DESC
LIMIT $2;
