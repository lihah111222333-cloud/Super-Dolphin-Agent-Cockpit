-- name: GetUIPreferenceValue :one
SELECT value
FROM ui_preferences
WHERE cwd = $1 AND key = $2;

-- name: UpsertUIPreference :exec
INSERT INTO ui_preferences (cwd, key, value, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (cwd, key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = NOW();

-- name: ListUIPreferences :many
SELECT key, value, cwd, updated_at
FROM ui_preferences
WHERE cwd = ''
   OR ($1::text <> '' AND cwd = $1)
ORDER BY cwd ASC, key ASC;
