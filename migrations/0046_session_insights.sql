-- 0046_session_insights.sql — P21 P3: per-turn aggregated metrics.
-- Core-only (see sqlc.yaml).
--
-- Design (see docs/plans/迁移/p21/P3_SessionInsights.md):
--   * One row per (thread_id, local_turn_id). Both partial unique indexes
--     act as secondary guards; the write path UPSERTs on
--     (thread_id, local_turn_id) so a late provider_turn_id merges into
--     the same row instead of opening a new one.
--   * success is nullable so the collector can distinguish "unknown"
--     from "false" (TurnCompleted.Success is a non-pointer bool; the
--     collector must not silently encode "not yet observed" as false).
--   * *_observed flags let queries separate "provider didn't emit this
--     event family" (FALSE) from "observed and the metric is legitimately
--     zero" (TRUE with value 0). Claude path's approval_requests starts
--     as observed=FALSE; codex path as observed=TRUE.
--   * Write path guarantees (enforced at the query layer, see
--     sql/queries/session_insight.sql):
--       status / success precedence: interrupted/aborted cannot be
--         displaced by a later 'completed' write.
--       token counters: GREATEST(existing, incoming) so a
--         context-window-only zero event cannot regress recorded totals.

CREATE TABLE IF NOT EXISTS public.session_insights (
    id                          BIGSERIAL   PRIMARY KEY,
    thread_id                   TEXT        NOT NULL DEFAULT '',
    agent_id                    TEXT        NOT NULL DEFAULT '',
    session_id                  TEXT        NOT NULL DEFAULT '',
    provider                    TEXT        NOT NULL DEFAULT '',
    local_turn_id               TEXT        NOT NULL DEFAULT '',
    provider_turn_id            TEXT        NOT NULL DEFAULT '',
    started_at                  TIMESTAMPTZ,
    completed_at                TIMESTAMPTZ,
    duration_ms                 INTEGER     NOT NULL DEFAULT 0,
    success                     BOOLEAN,
    status                      TEXT        NOT NULL DEFAULT 'unknown',
    stop_reason                 TEXT        NOT NULL DEFAULT '',
    tool_calls                  INTEGER     NOT NULL DEFAULT 0,
    tool_calls_observed         BOOLEAN     NOT NULL DEFAULT TRUE,
    tool_failures               INTEGER     NOT NULL DEFAULT 0,
    tool_failures_observed      BOOLEAN     NOT NULL DEFAULT TRUE,
    approval_requests           INTEGER     NOT NULL DEFAULT 0,
    approval_requests_observed  BOOLEAN     NOT NULL DEFAULT FALSE,
    token_input                 INTEGER     NOT NULL DEFAULT 0,
    token_output                INTEGER     NOT NULL DEFAULT 0,
    token_total                 INTEGER     NOT NULL DEFAULT 0,
    token_snapshot_observed     BOOLEAN     NOT NULL DEFAULT FALSE,
    context_window_tokens       INTEGER     NOT NULL DEFAULT 0,
    ui_projection               TEXT        NOT NULL DEFAULT '',
    skills_selected             JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_session_insights_duration_nn CHECK (duration_ms >= 0),
    CONSTRAINT chk_session_insights_tool_calls_nn CHECK (tool_calls >= 0 AND tool_failures >= 0),
    CONSTRAINT chk_session_insights_approvals_nn CHECK (approval_requests >= 0),
    CONSTRAINT chk_session_insights_tokens_nn CHECK (
        token_input >= 0 AND token_output >= 0 AND token_total >= 0
        AND context_window_tokens >= 0
    )
);

CREATE INDEX IF NOT EXISTS idx_session_insights_thread_created
    ON public.session_insights (thread_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_session_insights_created
    ON public.session_insights (created_at DESC);

-- Primary conflict target for UPSERT: (thread_id, local_turn_id). Partial
-- because a collector can observe events whose thread_id / local_turn_id
-- are genuinely absent (header-less claude path), and those rows must not
-- collide with each other.
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_insights_local_turn
    ON public.session_insights (thread_id, local_turn_id)
    WHERE thread_id <> '' AND local_turn_id <> '';

-- Secondary guard: the same provider + agent + provider_turn_id combo
-- must not accidentally produce two rows. Callers should still UPSERT
-- on (thread_id, local_turn_id); this index just catches bugs.
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_insights_provider_turn
    ON public.session_insights (provider, agent_id, provider_turn_id)
    WHERE provider <> '' AND agent_id <> '' AND provider_turn_id <> '';
