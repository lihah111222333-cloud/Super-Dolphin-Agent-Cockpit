-- 0030_baseline_schema_repair.sql
--
-- Repair PRIMARY KEY / UNIQUE constraints that were omitted from
-- 001_baseline.sql but are required by runtime ON CONFLICT upserts.
--
-- Each block is idempotent: verify the expected constraint shape,
-- deduplicate existing rows by keeping the latest / most complete row,
-- then add the missing constraint.

DO $$
BEGIN
    IF to_regclass('public.agent_threads') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.agent_threads'::regclass
             AND contype = 'p'
             AND pg_get_constraintdef(oid) = 'PRIMARY KEY (thread_id)'
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY thread_id
                       ORDER BY
                           (workspace_run_key <> '') DESC,
                           (owner_thread_id <> '') DESC,
                           updated_at DESC,
                           created_at DESC,
                           ctid DESC
                   ) AS rn
            FROM public.agent_threads
        )
        DELETE FROM public.agent_threads t
        USING ranked
        WHERE t.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE public.agent_threads
            ADD CONSTRAINT agent_threads_pkey PRIMARY KEY (thread_id);
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.agent_status') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.agent_status'::regclass
             AND contype = 'p'
             AND pg_get_constraintdef(oid) = 'PRIMARY KEY (agent_id)'
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY agent_id
                       ORDER BY
                           (session_id <> '') DESC,
                           (error <> '') DESC,
                           updated_at DESC,
                           created_at DESC,
                           ctid DESC
                   ) AS rn
            FROM public.agent_status
        )
        DELETE FROM public.agent_status s
        USING ranked
        WHERE s.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE public.agent_status
            ADD CONSTRAINT agent_status_pkey PRIMARY KEY (agent_id);
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.command_cards') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.command_cards'::regclass
             AND contype IN ('u', 'p')
             AND pg_get_constraintdef(oid) IN ('UNIQUE (card_key)', 'PRIMARY KEY (card_key)')
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY card_key
                       ORDER BY
                           (updated_by <> '') DESC,
                           (description <> '') DESC,
                           updated_at DESC,
                           created_at DESC,
                           id DESC,
                           ctid DESC
                   ) AS rn
            FROM public.command_cards
        )
        DELETE FROM public.command_cards c
        USING ranked
        WHERE c.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE public.command_cards
            ADD CONSTRAINT command_cards_card_key_key UNIQUE (card_key);
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.cwd_instance_locks') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.cwd_instance_locks'::regclass
             AND contype = 'p'
             AND pg_get_constraintdef(oid) = 'PRIMARY KEY (cwd)'
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY cwd
                       ORDER BY
                           (instance_id <> '') DESC,
                           heartbeat_at DESC,
                           acquired_at DESC,
                           pid DESC,
                           ctid DESC
                   ) AS rn
            FROM public.cwd_instance_locks
        )
        DELETE FROM public.cwd_instance_locks l
        USING ranked
        WHERE l.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE public.cwd_instance_locks
            ADD CONSTRAINT cwd_instance_locks_pkey PRIMARY KEY (cwd);
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.prompt_templates') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.prompt_templates'::regclass
             AND contype IN ('u', 'p')
             AND pg_get_constraintdef(oid) IN ('UNIQUE (prompt_key)', 'PRIMARY KEY (prompt_key)')
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY prompt_key
                       ORDER BY
                           (updated_by <> '') DESC,
                           (description <> '') DESC,
                           updated_at DESC,
                           created_at DESC,
                           id DESC,
                           ctid DESC
                   ) AS rn
            FROM public.prompt_templates
        )
        DELETE FROM public.prompt_templates p
        USING ranked
        WHERE p.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE public.prompt_templates
            ADD CONSTRAINT prompt_templates_prompt_key_key UNIQUE (prompt_key);
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.shared_files') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.shared_files'::regclass
             AND contype = 'p'
             AND pg_get_constraintdef(oid) = 'PRIMARY KEY (path)'
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY path
                       ORDER BY
                           (updated_by <> '') DESC,
                           updated_at DESC,
                           created_at DESC,
                           ctid DESC
                   ) AS rn
            FROM public.shared_files
        )
        DELETE FROM public.shared_files sf
        USING ranked
        WHERE sf.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE public.shared_files
            ADD CONSTRAINT shared_files_pkey PRIMARY KEY (path);
    END IF;
END
$$;

DO $$
DECLARE
    existing_pkey text;
BEGIN
    IF to_regclass('public.ui_preferences') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.ui_preferences'::regclass
             AND contype = 'p'
             AND pg_get_constraintdef(oid) = 'PRIMARY KEY (cwd, key)'
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY cwd, key
                       ORDER BY
                           updated_at DESC,
                           ctid DESC
                   ) AS rn
            FROM public.ui_preferences
        )
        DELETE FROM public.ui_preferences up
        USING ranked
        WHERE up.ctid = ranked.ctid
          AND ranked.rn > 1;

        SELECT conname
        INTO existing_pkey
        FROM pg_constraint
        WHERE conrelid = 'public.ui_preferences'::regclass
          AND contype = 'p'
        LIMIT 1;

        IF existing_pkey IS NOT NULL THEN
            EXECUTE format('ALTER TABLE public.ui_preferences DROP CONSTRAINT %I', existing_pkey);
        END IF;

        ALTER TABLE public.ui_preferences
            ADD CONSTRAINT ui_preferences_pkey PRIMARY KEY (cwd, key);
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.workspace_runs') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.workspace_runs'::regclass
             AND contype IN ('u', 'p')
             AND pg_get_constraintdef(oid) IN ('UNIQUE (run_key)', 'PRIMARY KEY (run_key)')
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY run_key
                       ORDER BY
                           (updated_by <> '') DESC,
                           (metadata <> '{}'::jsonb) DESC,
                           updated_at DESC,
                           created_at DESC,
                           id DESC,
                           ctid DESC
                   ) AS rn
            FROM public.workspace_runs
        )
        DELETE FROM public.workspace_runs wr
        USING ranked
        WHERE wr.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE public.workspace_runs
            ADD CONSTRAINT workspace_runs_run_key_key UNIQUE (run_key);
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regclass('public.workspace_run_files') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'public.workspace_run_files'::regclass
             AND contype IN ('u', 'p')
             AND pg_get_constraintdef(oid) IN (
                 'UNIQUE (run_key, relative_path)',
                 'PRIMARY KEY (run_key, relative_path)'
             )
       ) THEN
        WITH ranked AS (
            SELECT ctid,
                   row_number() OVER (
                       PARTITION BY run_key, relative_path
                       ORDER BY
                           (source_sha256_after <> '') DESC,
                           (workspace_sha256 <> '') DESC,
                           updated_at DESC,
                           created_at DESC,
                           id DESC,
                           ctid DESC
                   ) AS rn
            FROM public.workspace_run_files
        )
        DELETE FROM public.workspace_run_files wf
        USING ranked
        WHERE wf.ctid = ranked.ctid
          AND ranked.rn > 1;

        ALTER TABLE public.workspace_run_files
            ADD CONSTRAINT workspace_run_files_run_key_relative_path_key
            UNIQUE (run_key, relative_path);
    END IF;
END
$$;
