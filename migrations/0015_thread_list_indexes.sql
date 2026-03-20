-- 0015_thread_list_indexes.sql
-- 启动后会话列表链路优化:
--   1) agent_status.List: ORDER BY updated_at DESC LIMIT 500
--   2) agent_codex_binding.ListAll: ORDER BY created_at DESC
--
-- 说明:
-- - 启动 thread/start 本身不读取 codex rollout 文件，本迁移主要优化“列表聚合”场景。
-- - 现有 idx_agent_status_status_updated 仅在带 status 过滤时更有效；
--   无过滤场景补充 updated_at 单列索引可减少排序成本。

CREATE INDEX IF NOT EXISTS idx_agent_status_updated_at_desc
    ON agent_status (updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_acb_created_at_desc
    ON agent_codex_binding (created_at DESC);
