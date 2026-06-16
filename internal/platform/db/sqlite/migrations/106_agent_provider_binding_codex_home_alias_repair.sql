DROP TRIGGER IF EXISTS trg_prevent_agent_provider_binding_rebind;
-- SPLIT --
CREATE TRIGGER trg_prevent_agent_provider_binding_rebind
BEFORE UPDATE ON agent_provider_binding
FOR EACH ROW
WHEN NEW.agent_id <> OLD.agent_id
  OR NEW.provider <> OLD.provider
  OR (OLD.provider_thread_id <> '' AND NEW.provider_thread_id <> OLD.provider_thread_id)
  OR (OLD.codex_instance_key <> '' AND NEW.codex_instance_key <> OLD.codex_instance_key)
  OR (OLD.codex_model_provider <> '' AND NEW.codex_model_provider <> OLD.codex_model_provider)
  OR (
      OLD.codex_home <> ''
      AND NEW.codex_home <> OLD.codex_home
      AND (
          (OLD.codex_instance_key <> '' AND NEW.codex_instance_key <> OLD.codex_instance_key)
          OR (OLD.codex_model_provider <> '' AND NEW.codex_model_provider <> OLD.codex_model_provider)
      )
  )
BEGIN
    SELECT RAISE(ABORT, 'agent_provider_binding identity is immutable');
END;
