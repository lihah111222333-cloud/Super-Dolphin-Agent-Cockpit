-- 0109_refresh_dag_designer_prompt_agent_cwd.sql
--
-- The DAG designer cwd guidance is now owned by the registry-backed builtin
-- prompt assets under internal/platform/shared/builtinprompts/assets. After the
-- 0104 registry cutover, new builtin prompt bodies must not be patched in SQL
-- migrations; existing system-seed rows stay untouched unless a future data
-- repair migrates metadata only.

SELECT 1;
