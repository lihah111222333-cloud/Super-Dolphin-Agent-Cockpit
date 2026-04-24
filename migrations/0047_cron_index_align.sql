-- 0047_cron_index_align.sql — align cron indexes and provider CHECK with
-- P21/P22 hard rules without rewriting the original 0045 baseline.

DROP INDEX IF EXISTS public.idx_cron_jobs_claim;
DROP INDEX IF EXISTS public.idx_cron_job_runs_turn;
DROP INDEX IF EXISTS public.uq_cron_job_runs_idempotency;

CREATE UNIQUE INDEX IF NOT EXISTS uq_cron_job_runs_idempotency
    ON public.cron_job_runs (job_id, idempotency_key)
    WHERE job_id <> '' AND idempotency_key <> '';

CREATE UNIQUE INDEX IF NOT EXISTS uq_cron_job_runs_dedupe_key
    ON public.cron_job_runs (dedupe_key)
    WHERE dedupe_key <> '';

DO $$
BEGIN
    ALTER TABLE public.cron_jobs
        DROP CONSTRAINT IF EXISTS chk_cron_jobs_provider_not_empty;

    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conname = 'chk_cron_jobs_provider_supported'
           AND conrelid = 'public.cron_jobs'::regclass
    ) THEN
        ALTER TABLE public.cron_jobs
            ADD CONSTRAINT chk_cron_jobs_provider_supported
            CHECK (provider IN ('codex', 'claude'));
    END IF;
END $$;
