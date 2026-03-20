-- 0014_drop_agent_messages.sql
-- 消息存储重构: 删除 agent_messages 表, 改用 Codex 作为消息 SSOT。
DROP TABLE IF EXISTS agent_messages;
