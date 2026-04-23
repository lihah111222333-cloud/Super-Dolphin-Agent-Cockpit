-- 0060_turn_dedupe_registry.sql — P21 P1b: cross-process durable lookup
-- from dedupe_key -> local_turn_id so crash recovery survives a
-- mcp-orch process restart. The in-memory tracker in
-- internal/module/turn/tracker.go already covers the intra-process
-- race; this table bridges the gap so a crash between "StartTurn
-- returned" and "cron_job_runs.status advanced to submitted" no
-- longer forces a double-submit after the process restarts.
--
-- Lifetime:
--   * INSERT/UPSERT on turn start (dedupe_key non-empty). The same
--     row is overwritten if a second StartTurn with the same key
--     fires (last-wins, matching the tracker's RegisterDedupeKey).
--   * terminal_at stamped when the turn reaches completed / failed /
--     stalled so lookups can skip dead rows.
--   * A periodic sweep deletes rows whose updated_at is older than a
--     tracker-TTL-aligned cutoff. The sweep is the scheduler's job;
--     the schema here only exposes the column that makes it cheap.
--
-- Why a fresh table rather than a column on an existing turn row:
-- there is no turn row. Turns live in the in-memory tracker; this
-- table is the smallest possible durable shim purpose-built for the
-- P1b dedupe lookup.

CREATE TABLE IF NOT EXISTS public.turn_dedupe_registry (
    dedupe_key       TEXT        PRIMARY KEY,
    local_turn_id    TEXT        NOT NULL,
    provider_turn_id TEXT        NOT NULL DEFAULT '',
    thread_id        TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    terminal_at      TIMESTAMPTZ
);

-- Sweep target: delete rows whose updated_at is older than the cutoff
-- the sweeper picks. Kept as a plain btree so DESC scans are cheap
-- too if we ever want to list "recent dedupes".
CREATE INDEX IF NOT EXISTS idx_turn_dedupe_registry_updated_at
    ON public.turn_dedupe_registry (updated_at);

-- Live lookups (terminal_at IS NULL) are the hot path from the cron
-- scheduler's crash-recovery branch. Partial index keeps the usual
-- "already-terminal" majority out of the B-tree.
CREATE INDEX IF NOT EXISTS idx_turn_dedupe_registry_live
    ON public.turn_dedupe_registry (dedupe_key)
    WHERE terminal_at IS NULL;
