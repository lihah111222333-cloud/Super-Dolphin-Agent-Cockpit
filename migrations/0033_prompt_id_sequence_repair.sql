-- 0033_prompt_id_sequence_repair.sql
--
-- 001_baseline.sql declares prompt_templates.id / prompt_versions.id as
-- `bigint NOT NULL` without any sequence or default, but the runtime
-- INSERT statements (UpsertPromptTemplate, InsertPromptVersion) omit the
-- id column and rely on a server-side default. On fresh databases
-- bootstrapped from 001_baseline.sql every insert therefore fails with
-- `null value in column "id" ... violates not-null constraint`.
--
-- Repair: attach an owned sequence and wire it up as the column default.
-- Idempotent on databases that were upgraded from 0001_initial_schema.sql
-- (where id is already BIGSERIAL) because pg_get_serial_sequence returns
-- a non-null value there and this block is skipped.

DO $$
DECLARE
    current_max bigint;
BEGIN
    IF to_regclass('public.prompt_templates') IS NOT NULL
       AND pg_get_serial_sequence('public.prompt_templates', 'id') IS NULL THEN
        CREATE SEQUENCE IF NOT EXISTS public.prompt_templates_id_seq AS bigint;
        SELECT MAX(id) INTO current_max FROM public.prompt_templates;
        PERFORM setval(
            'public.prompt_templates_id_seq',
            COALESCE(current_max, 1),
            current_max IS NOT NULL
        );
        ALTER TABLE public.prompt_templates
            ALTER COLUMN id SET DEFAULT nextval('public.prompt_templates_id_seq');
        ALTER SEQUENCE public.prompt_templates_id_seq
            OWNED BY public.prompt_templates.id;
    END IF;
END
$$;

DO $$
DECLARE
    current_max bigint;
BEGIN
    IF to_regclass('public.prompt_versions') IS NOT NULL
       AND pg_get_serial_sequence('public.prompt_versions', 'id') IS NULL THEN
        CREATE SEQUENCE IF NOT EXISTS public.prompt_versions_id_seq AS bigint;
        SELECT MAX(id) INTO current_max FROM public.prompt_versions;
        PERFORM setval(
            'public.prompt_versions_id_seq',
            COALESCE(current_max, 1),
            current_max IS NOT NULL
        );
        ALTER TABLE public.prompt_versions
            ALTER COLUMN id SET DEFAULT nextval('public.prompt_versions_id_seq');
        ALTER SEQUENCE public.prompt_versions_id_seq
            OWNED BY public.prompt_versions.id;
    END IF;
END
$$;
