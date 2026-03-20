-- 0021_agent_provider_binding.sql
--
-- Provider-aware binding table:
--   - PRIMARY KEY (agent_id)
--   - UNIQUE (provider, provider_thread_id)
--   - provider/provider_thread_id must be non-empty
--   - immutable trigger blocks UPDATE on agent_id/provider/provider_thread_id

CREATE TABLE IF NOT EXISTS agent_provider_binding (
    agent_id            TEXT    NOT NULL,
    provider            TEXT    NOT NULL,
    provider_thread_id  TEXT    NOT NULL,
    codex_thread_id     TEXT    NOT NULL DEFAULT '',
    rollout_path        TEXT    NOT NULL DEFAULT '',
    cwd                 TEXT    NOT NULL DEFAULT '',
    archived            BOOLEAN NOT NULL DEFAULT false,
    created_at          BIGINT  NOT NULL DEFAULT 0,
    updated_at          BIGINT  NOT NULL DEFAULT 0,

    CONSTRAINT pk_agent_provider_binding PRIMARY KEY (agent_id),
    CONSTRAINT uq_agent_provider_binding_provider_thread UNIQUE (provider, provider_thread_id),
    CONSTRAINT chk_agent_provider_binding_provider_not_empty CHECK (provider <> ''),
    CONSTRAINT chk_agent_provider_binding_thread_not_empty CHECK (provider_thread_id <> '')
);

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
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_prevent_agent_provider_binding_rebind ON agent_provider_binding;

CREATE TRIGGER trg_prevent_agent_provider_binding_rebind
BEFORE UPDATE ON agent_provider_binding
FOR EACH ROW
EXECUTE FUNCTION prevent_agent_provider_binding_rebind();
