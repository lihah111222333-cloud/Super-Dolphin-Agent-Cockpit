-- name: UpsertPromptIntentDraft :one
INSERT INTO prompt_intent_drafts (
    draft_key, cwd, kind, raw_input, source_type, source_url,
    origin_hash, license_hint, generated_card, confidence, status, scope, issues, created_at, updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000)
)
ON CONFLICT (draft_key) DO UPDATE SET
    cwd = EXCLUDED.cwd,
    kind = EXCLUDED.kind,
    raw_input = EXCLUDED.raw_input,
    source_type = EXCLUDED.source_type,
    source_url = EXCLUDED.source_url,
    origin_hash = EXCLUDED.origin_hash,
    license_hint = EXCLUDED.license_hint,
    generated_card = EXCLUDED.generated_card,
    confidence = EXCLUDED.confidence,
    status = EXCLUDED.status,
    scope = EXCLUDED.scope,
    issues = EXCLUDED.issues,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING id, draft_key, cwd, kind, raw_input, source_type, source_url,
          origin_hash, license_hint, CAST(generated_card AS BLOB) AS generated_card,
          confidence, status, scope, CAST(issues AS BLOB) AS issues, created_at, updated_at;

-- name: GetPromptIntentDraft :one
SELECT id, draft_key, cwd, kind, raw_input, source_type, source_url,
       origin_hash, license_hint, CAST(generated_card AS BLOB) AS generated_card,
       confidence, status, scope, CAST(issues AS BLOB) AS issues, created_at, updated_at
FROM prompt_intent_drafts
WHERE draft_key = sqlc.arg(draft_key)
  AND cwd = sqlc.arg(cwd);

-- name: ListPromptIntentDrafts :many
SELECT id, draft_key, cwd, kind, raw_input, source_type, source_url,
       origin_hash, license_hint, CAST(generated_card AS BLOB) AS generated_card,
       confidence, status, scope, CAST(issues AS BLOB) AS issues, created_at, updated_at
FROM prompt_intent_drafts
WHERE cwd = sqlc.arg(cwd)
  AND (sqlc.narg(status) IS NULL OR status = sqlc.narg(status))
ORDER BY updated_at DESC
LIMIT sqlc.arg(limit_count);

-- name: UpdatePromptIntentDraftStatus :one
UPDATE prompt_intent_drafts
SET status = sqlc.arg(status), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE draft_key = sqlc.arg(draft_key)
  AND cwd = sqlc.arg(cwd)
RETURNING id, draft_key, cwd, kind, raw_input, source_type, source_url,
          origin_hash, license_hint, CAST(generated_card AS BLOB) AS generated_card,
          confidence, status, scope, CAST(issues AS BLOB) AS issues, created_at, updated_at;
