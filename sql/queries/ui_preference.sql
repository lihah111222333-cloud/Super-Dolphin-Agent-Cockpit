-- name: GetUIPreferenceValue :one
SELECT value
FROM ui_preferences
WHERE cwd = ? AND key = ?;

-- name: UpsertUIPreference :exec
INSERT INTO ui_preferences (cwd, key, value, updated_at)
VALUES (?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (cwd, key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000);

-- name: ListUIPreferences :many
SELECT key, value, cwd, updated_at
FROM ui_preferences
WHERE cwd = ''
   OR (? <> '' AND cwd = ?)
ORDER BY cwd ASC, key ASC;
