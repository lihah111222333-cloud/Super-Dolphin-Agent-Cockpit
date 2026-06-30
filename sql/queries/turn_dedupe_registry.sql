-- Queries for turn_dedupe_registry. See migration 0060 for the table
-- layout + lifetime contract.

-- name: UpsertTurnDedupeRegistry :exec
INSERT INTO turn_dedupe_registry (
    dedupe_key, local_turn_id, thread_id, created_at, updated_at, terminal_at
) VALUES (
    sqlc.arg(dedupe_key),
    sqlc.arg(local_turn_id),
    sqlc.arg(thread_id),
    sqlc.arg(now),
    sqlc.arg(now),
    NULL
)
ON CONFLICT (dedupe_key) DO UPDATE SET
    local_turn_id    = EXCLUDED.local_turn_id,
    thread_id        = CASE
                          WHEN EXCLUDED.thread_id = '' THEN turn_dedupe_registry.thread_id
                          ELSE EXCLUDED.thread_id
                       END,
    provider_turn_id = turn_dedupe_registry.provider_turn_id,
    updated_at       = EXCLUDED.updated_at
WHERE turn_dedupe_registry.terminal_at IS NULL;

-- name: BindTurnDedupeProviderID :exec
UPDATE turn_dedupe_registry
   SET provider_turn_id = sqlc.arg(provider_turn_id),
       updated_at       = sqlc.arg(now)
 WHERE dedupe_key = sqlc.arg(dedupe_key);

-- name: MarkTurnDedupeTerminal :exec
UPDATE turn_dedupe_registry
   SET terminal_at = sqlc.arg(now),
       updated_at  = sqlc.arg(now)
 WHERE dedupe_key = sqlc.arg(dedupe_key)
   AND terminal_at IS NULL;

-- name: GetLiveTurnDedupe :one
-- Returns the still-live registry row for dedupe_key, or an empty row
-- when none exists / all matching rows are already terminal. The
-- scheduler's caller checks local_turn_id == "" to distinguish miss
-- from hit.
SELECT dedupe_key, local_turn_id, provider_turn_id, thread_id, created_at,
       updated_at, terminal_at
  FROM turn_dedupe_registry
 WHERE dedupe_key = sqlc.arg(dedupe_key)
   AND terminal_at IS NULL
 LIMIT 1;

-- name: SweepTurnDedupeRegistry :exec
-- Deletes every row whose updated_at is older than cutoff. Run on a
-- coarse interval by the scheduler so the table can never outgrow
-- the tracker TTL window.
DELETE FROM turn_dedupe_registry
 WHERE updated_at < sqlc.arg(cutoff);
