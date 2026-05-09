-- Restore first-time write of agent_provider_binding.provider_thread_id.
--
-- 001_baseline.sql wrote the prevent_agent_provider_binding_rebind trigger
-- with `OLD.provider_thread_id <> '' AND NEW IS DISTINCT FROM OLD` semantics
-- (first-time write allowed). 0048_binding_codex_identity.sql added codex
-- identity columns and recreated the function but accidentally dropped the
-- `OLD.provider_thread_id <> ''` precondition, making provider_thread_id
-- unconditionally immutable. After 0048 every UPDATE issued by
-- UpdateAgentProviderBindingSessionUUID was rejected by the trigger, so
-- claude rows accumulate with provider_thread_id = '' or = agent_id while
-- session_uuid does carry the real claude session UUID. On restart the
-- driver cannot resume the right rollout, so prior conversation history
-- "disappears" in the UI.
--
-- This migration:
--   1. CREATE OR REPLACE the trigger function so provider_thread_id may be
--      written when its prior value is '' or equal to agent_id (matching the
--      CASE in UpdateAgentProviderBindingSessionUUID). Real non-placeholder
--      values remain immutable.
--   2. Repair existing claude rows whose provider_thread_id is the agent_id
--      placeholder by promoting their session_uuid into provider_thread_id,
--      gated on the session_uuid being a canonical v4 UUID so we don't
--      promote another stray placeholder.

BEGIN;

CREATE OR REPLACE FUNCTION prevent_agent_provider_binding_rebind()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.agent_id IS DISTINCT FROM OLD.agent_id THEN
        RAISE EXCEPTION 'agent_provider_binding.agent_id is immutable';
    END IF;
    IF NEW.provider IS DISTINCT FROM OLD.provider THEN
        RAISE EXCEPTION 'agent_provider_binding.provider is immutable for agent_id=%', OLD.agent_id;
    END IF;
    IF OLD.provider_thread_id <> ''
       AND OLD.provider_thread_id <> OLD.agent_id
       AND NEW.provider_thread_id IS DISTINCT FROM OLD.provider_thread_id THEN
        RAISE EXCEPTION 'agent_provider_binding.provider_thread_id is immutable for agent_id=%', OLD.agent_id;
    END IF;
    IF OLD.codex_home <> '' AND NEW.codex_home IS DISTINCT FROM OLD.codex_home THEN
        RAISE EXCEPTION 'agent_provider_binding.codex_home is immutable for agent_id=%', OLD.agent_id;
    END IF;
    IF OLD.codex_instance_key <> '' AND NEW.codex_instance_key IS DISTINCT FROM OLD.codex_instance_key THEN
        RAISE EXCEPTION 'agent_provider_binding.codex_instance_key is immutable for agent_id=%', OLD.agent_id;
    END IF;
    IF OLD.codex_model_provider <> '' AND NEW.codex_model_provider IS DISTINCT FROM OLD.codex_model_provider THEN
        RAISE EXCEPTION 'agent_provider_binding.codex_model_provider is immutable for agent_id=%', OLD.agent_id;
    END IF;
    RETURN NEW;
END;
$$;

UPDATE agent_provider_binding
SET provider_thread_id = session_uuid
WHERE provider = 'claude'
  AND provider_thread_id = agent_id
  AND session_uuid ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

COMMIT;
