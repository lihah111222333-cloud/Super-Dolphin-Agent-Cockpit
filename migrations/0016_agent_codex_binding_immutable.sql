-- 0016_agent_codex_binding_immutable.sql
--
-- 目的:
--   锁定 agent_codex_binding 的核心绑定关系，禁止 UPDATE 改写
--   agent_id/codex_thread_id，仅允许更新其他元数据字段（如 rollout_path）。
--
-- 说明:
--   - 允许 DELETE（手工删除）；
--   - 允许 INSERT（首次创建）；
--   - 禁止 UPDATE 修改 agent_id 或 codex_thread_id。

CREATE OR REPLACE FUNCTION prevent_agent_codex_binding_rebind()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.agent_id IS DISTINCT FROM OLD.agent_id THEN
        RAISE EXCEPTION 'agent_codex_binding.agent_id is immutable';
    END IF;
    IF NEW.codex_thread_id IS DISTINCT FROM OLD.codex_thread_id THEN
        RAISE EXCEPTION 'agent_codex_binding.codex_thread_id is immutable for agent_id=%', OLD.agent_id;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_prevent_agent_codex_binding_rebind ON agent_codex_binding;

CREATE TRIGGER trg_prevent_agent_codex_binding_rebind
BEFORE UPDATE ON agent_codex_binding
FOR EACH ROW
EXECUTE FUNCTION prevent_agent_codex_binding_rebind();

