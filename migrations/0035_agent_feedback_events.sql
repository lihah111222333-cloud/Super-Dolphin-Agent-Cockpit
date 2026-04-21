-- 0035_agent_feedback_events.sql — append-only feedback telemetry for
-- per-agent/prompt optimization.
--
-- 用途: 记录每一条 thread/turn 上的用户反馈事件（thumbs_up / thumbs_down /
--   retry / edit / handoff_out / user_override_route 等），按 agent_key +
--   prompt_version_id 聚合即可得到"每个 agent 的满意度 / 每版 prompt 的 A/B
--   信号"。表是 append-only；更正历史请再写一条反向事件。
-- Go 代码: internal/store/feedback/{contract.go,store.go};
--   internal/module/feedback/{service.go,rpc.go}.

CREATE TABLE IF NOT EXISTS public.agent_feedback_events (
    id                 BIGSERIAL PRIMARY KEY,
    thread_id          TEXT     NOT NULL,
    turn_id            TEXT     NOT NULL DEFAULT '',
    agent_key          TEXT     NOT NULL DEFAULT '',
    prompt_version_id  BIGINT,
    event_type         TEXT     NOT NULL,
    actor              TEXT     NOT NULL DEFAULT '',
    payload            JSONB    NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_feedback_events_thread
    ON public.agent_feedback_events (thread_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_feedback_events_agent_key
    ON public.agent_feedback_events (agent_key, created_at DESC)
    WHERE agent_key <> '';

CREATE INDEX IF NOT EXISTS idx_agent_feedback_events_prompt_version
    ON public.agent_feedback_events (prompt_version_id, created_at DESC)
    WHERE prompt_version_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_feedback_events_event_type
    ON public.agent_feedback_events (event_type, created_at DESC);
