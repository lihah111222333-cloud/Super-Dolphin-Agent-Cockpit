-- name: GetUIPreferenceValue :one
SELECT value
FROM ui_preferences
WHERE cwd = ? AND key = ?;

-- name: UpsertUIPreference :exec
INSERT INTO ui_preferences (cwd, key, value, updated_at)
VALUES (sqlc.arg(cwd), sqlc.arg(key), sqlc.arg(value), sqlc.arg(updated_at))
ON CONFLICT (cwd, key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = EXCLUDED.updated_at;

-- name: ListUIPreferences :many
SELECT key, value, cwd, updated_at
FROM ui_preferences
WHERE cwd = ''
   OR (sqlc.arg(cwd_filter) <> '' AND cwd = sqlc.arg(cwd_filter))
ORDER BY cwd ASC, key ASC;
