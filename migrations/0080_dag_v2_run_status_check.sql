-- DAG v2 T1.2-mid 根治 (B-0074 剩余): task_dag_runs.status 加 CHECK 枚举。
--
-- 背景：0074 仅声明 status TEXT NOT NULL DEFAULT 'running'，无枚举约束。
-- 应用层 contract (internal/sidecar/orch/store/taskdag/contract.go:381) 规定：
--   running | succeeded | failed | cancelled
-- 0076 partial unique index `WHERE status='running'` 也依赖该 status 字面量。
-- 但 DB 没有约束，任何 UPDATE 写入未知字符串都会通过；后续 service 状态机
-- 读到非枚举值会落到 default 分支或抛 unmarshal 错。
--
-- 修法：CHECK (status IN ('running','succeeded','failed','cancelled'))，与
-- 0078 depends_on CHECK 同款 NOT VALID + VALIDATE 两步。新 INSERT/UPDATE
-- 写未知值立即被拒；既有行用 NOT VALID 渐进、VALIDATE 升级到强约束。
--
-- 跑前预检（强制）:
--   SELECT DISTINCT status FROM task_dag_runs;
--   -> 期望: 仅 running/succeeded/failed/cancelled 子集；非空白名单值需先
--   UPDATE 收敛或人工 review。
-- 实际跑前预检结果（HEAD=99ca4d25, schema_migrations=78）：task_dag_runs
-- 0 行（T1.2 未在生产使用过），DISTINCT status 为空集合，VALIDATE 直接通过。
--
-- ROLLBACK (manual): 见 migrations/ROLLBACK.md §0080。

-- 包 BEGIN/COMMIT 让两步 ALTER 原子化（与 0076/0078/0079 同款）。当前 runner
-- pool.Exec 单次执行整文件、不自动包 tx，因此显式 BEGIN/COMMIT 必要。
BEGIN;

-- 步骤 1：NOT VALID 仅约束新写入，不阻塞既有行。
ALTER TABLE task_dag_runs
  ADD CONSTRAINT chk_task_dag_runs_status_enum
  CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled'))
  NOT VALID;

-- 步骤 2：VALIDATE 升级到强约束（要求所有既有行通过；预检已确认 0 行）。
ALTER TABLE task_dag_runs
  VALIDATE CONSTRAINT chk_task_dag_runs_status_enum;

COMMIT;
