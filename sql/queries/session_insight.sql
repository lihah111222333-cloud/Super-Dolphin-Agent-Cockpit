-- UpsertSessionInsight merges an incoming snapshot with any existing row
-- keyed by (thread_id, local_turn_id). The update clause encodes two
-- invariants the P3 plan requires at the SQL layer:
--
--  * terminal precedence: once status is 'interrupted' or 'aborted', a
--    later 'completed'/'failed' must not displace it. success is
--    likewise frozen once the locked terminal is set.
--  * token no-regression: counters only advance. A context-window-only
--    event with zero counts cannot regress token_input / token_output /
--    token_total. Scalar GREATEST is per-field so a legitimate zero
--    event for one dimension does not zero out a sibling.
--
-- The *_observed flags go sticky: once TRUE, no later event can flip
-- them back to FALSE. This keeps "we already saw real data" from being
-- clobbered by a projection that doesn't re-emit the signal.
--
-- name: UpsertSessionInsight :one
INSERT INTO session_insights (
    thread_id, agent_id, session_id, provider,
    local_turn_id, provider_turn_id,
    started_at, completed_at, duration_ms,
    success, status, stop_reason,
    tool_calls, tool_calls_observed,
    tool_failures, tool_failures_observed,
    approval_requests, approval_requests_observed,
    token_input, token_output, token_total, token_snapshot_observed,
    context_window_tokens, ui_projection, skills_selected,
    created_at, updated_at
) VALUES (
    sqlc.arg(thread_id), sqlc.arg(agent_id), sqlc.arg(session_id), sqlc.arg(provider),
    sqlc.arg(local_turn_id), sqlc.arg(provider_turn_id),
    sqlc.arg(started_at), sqlc.arg(completed_at), sqlc.arg(duration_ms),
    sqlc.narg(success), sqlc.arg(status), sqlc.arg(stop_reason),
    sqlc.arg(tool_calls), sqlc.arg(tool_calls_observed),
    sqlc.arg(tool_failures), sqlc.arg(tool_failures_observed),
    sqlc.arg(approval_requests), sqlc.arg(approval_requests_observed),
    sqlc.arg(token_input), sqlc.arg(token_output), sqlc.arg(token_total), sqlc.arg(token_snapshot_observed),
    sqlc.arg(context_window_tokens), sqlc.arg(ui_projection), sqlc.arg(skills_selected)::jsonb,
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT (thread_id, local_turn_id)
WHERE thread_id <> '' AND local_turn_id <> ''
DO UPDATE SET
    agent_id         = COALESCE(NULLIF(EXCLUDED.agent_id, ''), session_insights.agent_id),
    session_id       = COALESCE(NULLIF(EXCLUDED.session_id, ''), session_insights.session_id),
    provider         = COALESCE(NULLIF(EXCLUDED.provider, ''), session_insights.provider),
    provider_turn_id = COALESCE(NULLIF(EXCLUDED.provider_turn_id, ''), session_insights.provider_turn_id),
    started_at       = COALESCE(session_insights.started_at, EXCLUDED.started_at),
    completed_at     = COALESCE(EXCLUDED.completed_at, session_insights.completed_at),
    duration_ms      = GREATEST(session_insights.duration_ms, EXCLUDED.duration_ms),
    status = CASE
        WHEN session_insights.status IN ('interrupted', 'aborted') THEN session_insights.status
        ELSE EXCLUDED.status
    END,
    success = CASE
        WHEN session_insights.status IN ('interrupted', 'aborted') THEN session_insights.success
        ELSE EXCLUDED.success
    END,
    stop_reason = CASE
        WHEN session_insights.status IN ('interrupted', 'aborted') THEN session_insights.stop_reason
        WHEN EXCLUDED.stop_reason <> '' THEN EXCLUDED.stop_reason
        ELSE session_insights.stop_reason
    END,
    tool_calls               = GREATEST(session_insights.tool_calls, EXCLUDED.tool_calls),
    tool_calls_observed      = session_insights.tool_calls_observed OR EXCLUDED.tool_calls_observed,
    tool_failures            = GREATEST(session_insights.tool_failures, EXCLUDED.tool_failures),
    tool_failures_observed   = session_insights.tool_failures_observed OR EXCLUDED.tool_failures_observed,
    approval_requests        = GREATEST(session_insights.approval_requests, EXCLUDED.approval_requests),
    approval_requests_observed = session_insights.approval_requests_observed OR EXCLUDED.approval_requests_observed,
    token_input              = GREATEST(session_insights.token_input, EXCLUDED.token_input),
    token_output             = GREATEST(session_insights.token_output, EXCLUDED.token_output),
    token_total              = GREATEST(session_insights.token_total, EXCLUDED.token_total),
    token_snapshot_observed  = session_insights.token_snapshot_observed OR EXCLUDED.token_snapshot_observed,
    context_window_tokens    = GREATEST(session_insights.context_window_tokens, EXCLUDED.context_window_tokens),
    ui_projection            = COALESCE(NULLIF(EXCLUDED.ui_projection, ''), session_insights.ui_projection),
    skills_selected = CASE
        WHEN EXCLUDED.skills_selected IS NULL OR EXCLUDED.skills_selected = '[]'::jsonb
            THEN session_insights.skills_selected
        ELSE EXCLUDED.skills_selected
    END,
    updated_at               = EXCLUDED.updated_at
RETURNING id, thread_id, agent_id, session_id, provider, local_turn_id,
          provider_turn_id, started_at, completed_at, duration_ms,
          success, status, stop_reason,
          tool_calls, tool_calls_observed,
          tool_failures, tool_failures_observed,
          approval_requests, approval_requests_observed,
          token_input, token_output, token_total, token_snapshot_observed,
          context_window_tokens, ui_projection, skills_selected,
          created_at, updated_at;

-- name: GetSessionInsightByLocalTurn :one
SELECT id, thread_id, agent_id, session_id, provider, local_turn_id,
       provider_turn_id, started_at, completed_at, duration_ms,
       success, status, stop_reason,
       tool_calls, tool_calls_observed,
       tool_failures, tool_failures_observed,
       approval_requests, approval_requests_observed,
       token_input, token_output, token_total, token_snapshot_observed,
       context_window_tokens, ui_projection, skills_selected,
       created_at, updated_at
FROM session_insights
WHERE thread_id = $1 AND local_turn_id = $2;

-- name: ListSessionInsightsByThread :many
SELECT id, thread_id, agent_id, session_id, provider, local_turn_id,
       provider_turn_id, started_at, completed_at, duration_ms,
       success, status, stop_reason,
       tool_calls, tool_calls_observed,
       tool_failures, tool_failures_observed,
       approval_requests, approval_requests_observed,
       token_input, token_output, token_total, token_snapshot_observed,
       context_window_tokens, ui_projection, skills_selected,
       created_at, updated_at
FROM session_insights
WHERE thread_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- ListRecentSessionInsights is used by the dashboard API to return the
-- N most recent turns across all threads.
--
-- name: ListRecentSessionInsights :many
SELECT id, thread_id, agent_id, session_id, provider, local_turn_id,
       provider_turn_id, started_at, completed_at, duration_ms,
       success, status, stop_reason,
       tool_calls, tool_calls_observed,
       tool_failures, tool_failures_observed,
       approval_requests, approval_requests_observed,
       token_input, token_output, token_total, token_snapshot_observed,
       context_window_tokens, ui_projection, skills_selected,
       created_at, updated_at
FROM session_insights
ORDER BY created_at DESC, id DESC
LIMIT $1;

-- ListObservedApprovalRequests returns the per-thread approval_requests
-- window but only for turns where approval_requests_observed = TRUE.
-- Claude path rows (observed=FALSE, value=0) are excluded so callers
-- don't conflate "provider didn't emit the event" with "the turn had
-- zero approvals". See P3 plan *_observed discussion.
--
-- name: ListObservedApprovalRequests :many
SELECT id, thread_id, agent_id, local_turn_id, provider_turn_id,
       approval_requests, created_at
FROM session_insights
WHERE approval_requests_observed = TRUE
  AND ($1 = '' OR thread_id = $1)
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- ListObservedTokenTurns returns turns where token_snapshot_observed =
-- TRUE. Used by dashboards that want to average token costs without
-- pulling in thread-projection zero-fallback rows.
--
-- name: ListObservedTokenTurns :many
SELECT id, thread_id, agent_id, local_turn_id, provider_turn_id,
       token_input, token_output, token_total, context_window_tokens,
       created_at
FROM session_insights
WHERE token_snapshot_observed = TRUE
  AND ($1 = '' OR thread_id = $1)
ORDER BY created_at DESC, id DESC
LIMIT $2;
