-- DAG v2 骨架阶段 S3.4: 旧 metadata.auto_handoff_phase1 → trigger='auto' 一次性映射
--
-- 蓝图 v2 §10 补丁 7 / 实施计划 S15.1：删除 task_tools.go:23,116,233-234 写入点。
-- 本 migration 把已存在数据库里的旧字段一次性迁移到一等字段 trigger 上，
-- 然后清理 metadata 里的 auto_handoff_phase1 key。
--
-- 这是单向迁移；rollback 不易（trigger='auto' 的来源可能不止 auto_handoff_phase1）。
-- 如必须回滚：
--   1) 暂停 service：保证没新写
--   2) 把 trigger='auto' 的行加回 metadata.auto_handoff_phase1=true（手动 SQL）
--   3) 把 trigger 列重置为 'manual'
-- 这套 rollback 不能恢复"原本就 trigger=auto 但不是 auto_handoff_phase1 来的" row，
-- 所以建议本 migration 落地前先 pg_dump 备份。
--
-- ROLLBACK (best-effort, manual)：见上。

UPDATE task_dags
SET trigger = 'auto'
WHERE trigger = 'manual'
  AND metadata IS NOT NULL
  AND metadata ? 'auto_handoff_phase1'
  AND (metadata->>'auto_handoff_phase1')::boolean = TRUE;

-- 清理 metadata 里的过期字段（仅在它存在时移除）。
UPDATE task_dags
SET metadata = metadata - 'auto_handoff_phase1'
WHERE metadata IS NOT NULL
  AND metadata ? 'auto_handoff_phase1';
