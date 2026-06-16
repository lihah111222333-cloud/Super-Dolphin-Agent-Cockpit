-- DAG v2 A+ 修复 (B-0074 剩余): task_dag_runs.trigger_source 加 CHECK 枚举。
--
-- 背景：0074 仅声明 trigger_source TEXT NOT NULL DEFAULT ''，无枚举约束。
-- ADR 0001 §2.10 baseline、internal/sidecar/orch/store/taskdag/contract.go 与 MCP
-- 工具 schema (internal/sidecar/orch/tools/task_tools.go startDAGTriggerEnum) 规定：
--   manual | auto | scheduled | external
-- 但 DB 没有 CHECK，任何 UPDATE 写入未知字符串都会通过；F5 cron daemon /
-- dispatcher 读到非枚举值会落 default 分支或抛 unmarshal 错。
--
-- 修法：CHECK (trigger_source IN ('manual','auto','scheduled','external',''))，
-- 与 0080/0081 同款 NOT VALID + VALIDATE 两步。新 INSERT/UPDATE 写未知值
-- 立即被拒；既有行用 NOT VALID 渐进、VALIDATE 升级到强约束。
--
-- 设计取舍 — 为什么允许 ''（空串）：
--   0074 把 trigger_source 的 DEFAULT 定为空串 ''，既有 row（含手工/早期 run）
--   带空串值。若仅 CHECK 4 值 VALIDATE 会失败。空串语义等价于 "未提供"，
--   handler 层 startDAGRequestFromInput 把空串映射为 "" 透传，service 层不会
--   误判。后续若决定把 default 收敛为 'manual'，再发独立 migration 移除空串
--   合法值（见 docs/plans/dag改造实施计划.md §10 follow-up）。
--
-- The CHECK includes '' (empty string) because 0074 set DEFAULT '' and the
-- existing rows in development DB carry empty values. Stripping '' here
-- would break VALIDATE on existing rows; collapsing default to 'manual' is
-- a follow-up migration tracked in plans §10.
--
-- 跑前预检（强制）:
--   SELECT DISTINCT trigger_source FROM task_dag_runs;
--   -> 期望: 仅 manual/auto/scheduled/external/'' 子集；非白名单值需先
--   UPDATE 收敛或人工 review。
-- 实际跑前预检结果（HEAD=4f3bb5be, schema_migrations=81，task_dag_runs
-- 3 行）：DISTINCT trigger_source = {manual, ''}，全在白名单内，VALIDATE
-- 安全通过。
--
-- ROLLBACK (manual): 见 migrations/ROLLBACK.md §0082。

-- 包 BEGIN/COMMIT 让两步 ALTER 原子化（与 0076/0078/0079/0080/0081 同款）。
-- 当前 runner pool.Exec 单次执行整文件、不自动包 tx，因此显式 BEGIN/COMMIT 必要。
BEGIN;

-- 幂等保护：若约束已存在（例如上次执行 BEGIN/COMMIT 成功但 schema_migrations
-- INSERT 漂移），跳过 ADD；否则按 NOT VALID + VALIDATE 两步建立。
-- 背景：executeMigration（internal/platform/db/module.go）SQL 与 schema_migrations
-- 写入未原子化，进程中断会留下"约束已建、bookkeeping 漏写"的半成品状态。
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'chk_task_dag_runs_trigger_source_enum'
      AND conrelid = 'public.task_dag_runs'::regclass
  ) THEN
    -- 步骤 1：NOT VALID 仅约束新写入，不阻塞既有行。
    ALTER TABLE task_dag_runs
      ADD CONSTRAINT chk_task_dag_runs_trigger_source_enum
      CHECK (trigger_source IN ('manual', 'auto', 'scheduled', 'external', ''))
      NOT VALID;
  END IF;

  -- 步骤 2：VALIDATE 升级到强约束（VALIDATE 对已 validated 约束是 no-op，
  -- 但显式 IF 检查避免无谓 lock 与日志噪音）。
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'chk_task_dag_runs_trigger_source_enum'
      AND conrelid = 'public.task_dag_runs'::regclass
      AND convalidated = false
  ) THEN
    ALTER TABLE task_dag_runs
      VALIDATE CONSTRAINT chk_task_dag_runs_trigger_source_enum;
  END IF;
END
$$;

COMMIT;
