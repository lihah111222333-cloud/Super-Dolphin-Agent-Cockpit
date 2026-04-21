-- 0034_thread_agent_routing.sql — smart-routing columns on agent_threads.
--
-- 用途: 记录 router 为当前 thread 选中的 agent_key, 以及被注入到
--   baseInstructions 的 prompt_versions 行 id。支持按 agent/prompt_version
--   聚合指标与 A/B 比较。
-- Go 代码: internal/store/thread/{contract.go,store.go}, internal/router/.

ALTER TABLE public.agent_threads
    ADD COLUMN IF NOT EXISTS agent_key         TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS prompt_version_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_agent_threads_agent_key
    ON public.agent_threads (agent_key)
    WHERE agent_key <> '';
