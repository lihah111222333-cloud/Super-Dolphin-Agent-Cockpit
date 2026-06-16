-- DAG v2 T1.2-mid 根治: task_dags.trigger 加 CHECK 枚举。
--
-- 背景：0072 仅声明 trigger TEXT NOT NULL DEFAULT 'manual'，无枚举约束。
-- 0072 的注释和 internal/sidecar/orch/store/taskdag/contract.go 规定全集为：
--   manual | auto | scheduled | external
-- 0075 兼容映射也以这 4 值为对齐目标。但 DB 没有 CHECK，任何 UPDATE
-- 写入未知字符串都会通过；F5 cron daemon / dispatcher 读到非枚举值
-- 会落到 default 分支或抛 unmarshal 错。
--
-- 修法：CHECK (trigger IN ('manual','auto','scheduled','external'))，
-- 与 0080 status CHECK 同款 NOT VALID + VALIDATE 两步。新 INSERT/UPDATE
-- 写未知值立即被拒；既有行用 NOT VALID 渐进、VALIDATE 升级到强约束。
--
-- 跑前预检（强制）:
--   SELECT DISTINCT trigger FROM task_dags;
--   -> 期望: 仅 manual/auto/scheduled/external 子集；非空白名单值需先
--   UPDATE 收敛或人工 review。
-- 实际跑前预检结果（HEAD=096c0957, schema_migrations=80，task_dags
-- 70 行）：DISTINCT trigger = {manual, auto}，全在 4 值白名单内，
-- VALIDATE 安全通过。
--
-- ROLLBACK (manual): 见 migrations/ROLLBACK.md §0081。

-- 包 BEGIN/COMMIT 让两步 ALTER 原子化（与 0076/0078/0079/0080 同款）。
-- 当前 runner pool.Exec 单次执行整文件、不自动包 tx，因此显式 BEGIN/COMMIT 必要。
BEGIN;

-- 步骤 1：NOT VALID 仅约束新写入，不阻塞既有行。
ALTER TABLE task_dags
  ADD CONSTRAINT chk_task_dags_trigger_enum
  CHECK (trigger IN ('manual', 'auto', 'scheduled', 'external'))
  NOT VALID;

-- 步骤 2：VALIDATE 升级到强约束（要求所有既有行通过；预检已确认 70 行全白名单）。
ALTER TABLE task_dags
  VALIDATE CONSTRAINT chk_task_dags_trigger_enum;

COMMIT;
