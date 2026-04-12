-- 0028_binding_provider_thread_nullable.sql
--
-- Allow provider_thread_id to be empty on initial launch.
-- The real UUID is filled in after the session handshake completes.
-- On resume/recovery (e.g. Claude session rotation), the UUID can be updated.
--
-- Changes:
--   1. Drop CHECK constraint that blocks empty provider_thread_id
--   2. Remove provider_thread_id from immutable trigger
--   3. Replace UNIQUE(provider, provider_thread_id) with a partial unique
--      index that only applies when provider_thread_id is non-empty,
--      so multiple agents can have empty provider_thread_id initially.
--   4. Set DEFAULT '' so existing INSERT paths don't break.

-- 1. Drop the non-empty check constraint.
ALTER TABLE agent_provider_binding
    DROP CONSTRAINT IF EXISTS chk_agent_provider_binding_thread_not_empty;

-- 2. Replace the full unique constraint with a partial unique index.
ALTER TABLE agent_provider_binding
    DROP CONSTRAINT IF EXISTS uq_agent_provider_binding_provider_thread;

CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_provider_binding_provider_thread
    ON agent_provider_binding (provider, provider_thread_id)
    WHERE provider_thread_id <> '';

-- 3. Recreate the immutable trigger: provider_thread_id is immutable
--    once set (non-empty), but can be filled in from empty.
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
    -- provider_thread_id: immutable once set, but allow empty → non-empty (first fill).
    IF OLD.provider_thread_id <> '' AND NEW.provider_thread_id IS DISTINCT FROM OLD.provider_thread_id THEN
        RAISE EXCEPTION 'agent_provider_binding.provider_thread_id is immutable for agent_id=%', OLD.agent_id;
    END IF;
    RETURN NEW;
END;
$$;
