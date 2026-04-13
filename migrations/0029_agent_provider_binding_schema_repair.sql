-- 0029_agent_provider_binding_schema_repair.sql
--
-- Repair agent_provider_binding on databases bootstrapped from 001_baseline.sql.
-- The baseline snapshot created the table without the PRIMARY KEY / provider
-- CHECK, so 0021's CREATE TABLE IF NOT EXISTS skipped those constraints on
-- fresh installs. This migration is idempotent and also heals upgraded
-- databases that already have rows but are missing the expected schema.

ALTER TABLE agent_provider_binding
    ADD COLUMN IF NOT EXISTS session_uuid TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_provider_binding
    DROP CONSTRAINT IF EXISTS chk_agent_provider_binding_thread_not_empty;

ALTER TABLE agent_provider_binding
    DROP CONSTRAINT IF EXISTS uq_agent_provider_binding_provider_thread;

DO $$
BEGIN
    IF to_regclass('public.uq_agent_provider_binding_provider_thread') IS NULL THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY provider, provider_thread_id
                       ORDER BY
                           (codex_thread_id <> '') DESC,
                           (session_uuid <> '') DESC,
                           (cwd <> '') DESC,
                           updated_at DESC,
                           created_at DESC,
                           ctid DESC
                   ) AS rn
            FROM agent_provider_binding
            WHERE provider_thread_id <> ''
        )
        DELETE FROM agent_provider_binding apb
        USING ranked
        WHERE apb.ctid = ranked.ctid
          AND ranked.rn > 1;
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_provider_binding_provider_thread
    ON agent_provider_binding (provider, provider_thread_id)
    WHERE provider_thread_id <> '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'agent_provider_binding'::regclass
          AND conname = 'pk_agent_provider_binding'
    ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY agent_id
                       ORDER BY
                           (provider_thread_id <> '') DESC,
                           (codex_thread_id <> '') DESC,
                           (session_uuid <> '') DESC,
                           (cwd <> '') DESC,
                           updated_at DESC,
                           created_at DESC,
                           ctid DESC
                   ) AS rn
            FROM agent_provider_binding
        )
        DELETE FROM agent_provider_binding apb
        USING ranked
        WHERE apb.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE agent_provider_binding
            ADD CONSTRAINT pk_agent_provider_binding PRIMARY KEY (agent_id);
    END IF;
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'agent_provider_binding'::regclass
          AND conname = 'chk_agent_provider_binding_provider_not_empty'
    ) THEN
        ALTER TABLE agent_provider_binding
            ADD CONSTRAINT chk_agent_provider_binding_provider_not_empty
            CHECK (provider <> '') NOT VALID;
    END IF;
END
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'agent_provider_binding'::regclass
          AND conname = 'chk_agent_provider_binding_provider_not_empty'
          AND NOT convalidated
    ) THEN
        BEGIN
            ALTER TABLE agent_provider_binding
                VALIDATE CONSTRAINT chk_agent_provider_binding_provider_not_empty;
        EXCEPTION
            WHEN check_violation THEN
                RAISE NOTICE 'agent_provider_binding contains rows with empty provider; leaving check constraint as NOT VALID';
        END;
    END IF;
END
$$;

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
    IF OLD.provider_thread_id <> '' AND NEW.provider_thread_id IS DISTINCT FROM OLD.provider_thread_id THEN
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
