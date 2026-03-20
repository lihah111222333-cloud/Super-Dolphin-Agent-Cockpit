-- 0018_binding_cwd_archived.sql — 为 agent_codex_binding 添加 cwd + archived 字段。
--
-- cwd:      agent 归属的项目目录, 用于多项目隔离过滤。
-- archived: 是否已归档, 替代 agent_threads 表的归档逻辑。
--
-- 这两个字段不受 immutable trigger 限制 (trigger 只锁 agent_id / codex_thread_id)。

ALTER TABLE agent_codex_binding
  ADD COLUMN IF NOT EXISTS cwd      TEXT    NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT false;

-- 按 cwd 快速过滤
CREATE INDEX IF NOT EXISTS idx_acb_cwd ON agent_codex_binding(cwd);
