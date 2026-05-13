-- DAG v2 T1.2-mid 根治 (X1 #3): 把 task_dag_nodes.depends_on 列约束为
-- jsonb 数组类型。原 0073 仅声明列类型 jsonb 默认 '[]'::jsonb，但运行时
-- jsonb 列允许任意 jsonb 值（null/object/string/number/array），如果
-- 有人 UPDATE depends_on='null'::jsonb 或 '{}'::jsonb，service.PromoteRoot
-- NodesToReady 的谓词 jsonb_array_length(depends_on)=0 会**运行时 abort**
-- 非数组值上报 SQL error，这是潜在崩溃点。
--
-- 修法：先把既有非数组 depends_on 收敛为 []，再加
-- CHECK (jsonb_typeof(depends_on) = 'array')。新 INSERT/UPDATE 写入非数组
-- 立即被拒；既有脏数据来自旧版本/手工写入时自动归一化为“无依赖”，避免
-- 本地旧库在自动迁移时卡死。
--
-- 跑前预检（强制）:
--   SELECT jsonb_typeof(depends_on), COUNT(*) FROM task_dag_nodes
--     GROUP BY jsonb_typeof(depends_on);
--   非 array 行数应为 0；若 > 0 需先 UPDATE 收敛或人工 review 数据。
-- 实际跑前预检结果：460 行全部 array (jtype=array, cnt=460)，VALIDATE 直接通过。
--
-- ROLLBACK (manual):
--   ALTER TABLE task_dag_nodes DROP CONSTRAINT IF EXISTS chk_depends_on_is_array;

-- 包 BEGIN/COMMIT 让修复与两步 ALTER 原子化：若后续失败则整体 rollback，
-- 避免遗留半修复数据或 NOT VALID 状态的半成品约束。当前 runner
-- (internal/platform/db/module.go executeMigration) 用 pool.Exec 单次执行整文件、
-- 不自动包 tx，因此显式 BEGIN/COMMIT 是必须的。
BEGIN;

-- 自动收敛历史脏数据。depends_on 只有数组语义；非数组值无法可靠解释为依赖
-- 边集合，统一降级为空依赖，恢复 PromoteRootNodesToReady 的可执行性。
UPDATE task_dag_nodes
SET depends_on = '[]'::jsonb
WHERE depends_on IS NULL
   OR jsonb_typeof(depends_on) IS DISTINCT FROM 'array';

-- 步骤 1：NOT VALID 仅约束新写入，不阻塞既有行（即使预检显示全 array,
-- NOT VALID 模式仍是更稳健的渐进路径，未来追加 migration 不会受历史数据卡死）。
ALTER TABLE task_dag_nodes
  ADD CONSTRAINT chk_depends_on_is_array
  CHECK (jsonb_typeof(depends_on) = 'array')
  NOT VALID;

-- 步骤 2：VALIDATE 升级到强约束（要求所有既有行通过；预检已确认全部 array）。
ALTER TABLE task_dag_nodes
  VALIDATE CONSTRAINT chk_depends_on_is_array;

COMMIT;
