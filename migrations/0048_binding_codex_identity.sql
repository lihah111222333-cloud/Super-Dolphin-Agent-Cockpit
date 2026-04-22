-- 0048_binding_codex_identity.sql
--
-- P21 P1a: persist the codex instance identity triple
-- (codexHome / codexInstanceKey / codexModelProvider) on
-- agent_provider_binding so auto-resume can address the right local
-- app-server process after a restart.
--
-- These three columns join the existing immutable set (agent_id, provider,
-- provider_thread_id): once written to a non-empty value, UPDATE that would
-- mutate them is rejected by the rebind trigger. Clients that need a
-- different instance must create a new agent + binding instead of rewriting
-- existing state. Empty '' is the migration-window default, so the trigger
-- allows the first transition from '' -> non-empty without rejection.

ALTER TABLE agent_provider_binding
    ADD COLUMN IF NOT EXISTS codex_home           TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS codex_instance_key   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS codex_model_provider TEXT NOT NULL DEFAULT '';

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
    IF NEW.provider_thread_id IS DISTINCT FROM OLD.provider_thread_id THEN
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

DROP TRIGGER IF EXISTS trg_prevent_agent_provider_binding_rebind ON agent_provider_binding;

CREATE TRIGGER trg_prevent_agent_provider_binding_rebind
BEFORE UPDATE ON agent_provider_binding
FOR EACH ROW
EXECUTE FUNCTION prevent_agent_provider_binding_rebind();
