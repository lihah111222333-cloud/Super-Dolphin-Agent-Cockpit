-- 0045_cron_jobs.sql — P21 P1b: persistent cron-style scheduling for long
-- running / background agent tasks. Core-only (see sqlc.yaml).
--
-- Design (see docs/plans/迁移/p21/P1b_CronScheduledTasks.md):
--   * cron_jobs rows hold schedule + runtime snapshot (claim lease / last
--     status / retry budget). claim_token is generated in Go (UUID v4);
--     we intentionally do NOT pull in pgcrypto / gen_random_uuid so core
--     deployments stay extension-free.
--   * cron_job_runs rows are the per-trigger idempotency unit. dedupe_key =
--     sha256(job_id||scheduled_at||idempotency_key) is unique when set so
--     provider-side StartTurn retries after crash collapse to one submit.
--   * ClaimDueJobs must use SELECT ... FOR UPDATE SKIP LOCKED + same-tx
--     UPDATE so two schedulers never grab the same job.

CREATE TABLE IF NOT EXISTS public.cron_jobs (
    id                  TEXT        PRIMARY KEY,
    name                TEXT        NOT NULL,
    prompt              TEXT        NOT NULL,
    schedule_type       TEXT        NOT NULL DEFAULT 'cron',
    schedule_expr       TEXT        NOT NULL,
    timezone            TEXT        NOT NULL DEFAULT '',
    provider            TEXT        NOT NULL DEFAULT 'codex',
    model               TEXT        NOT NULL DEFAULT '',
    cwd                 TEXT        NOT NULL,
    config              JSONB       NOT NULL DEFAULT '{}'::jsonb,
    skills              JSONB       NOT NULL DEFAULT '[]'::jsonb,
    notify_channel      TEXT        NOT NULL DEFAULT '',
    enabled             BOOLEAN     NOT NULL DEFAULT TRUE,
    next_run_at         TIMESTAMPTZ NOT NULL,
    last_scheduled_at   TIMESTAMPTZ,
    last_run_at         TIMESTAMPTZ,
    claimed_at          TIMESTAMPTZ,
    claimed_by          TEXT        NOT NULL DEFAULT '',
    lease_expires_at    TIMESTAMPTZ,
    claim_token         TEXT        NOT NULL DEFAULT '',
    thread_id           TEXT        NOT NULL DEFAULT '',
    agent_id            TEXT        NOT NULL DEFAULT '',
    active_turn_id      TEXT        NOT NULL DEFAULT '',
    last_turn_id        TEXT        NOT NULL DEFAULT '',
    failure_count       INTEGER     NOT NULL DEFAULT 0,
    max_attempts        INTEGER     NOT NULL DEFAULT 0,
    next_retry_at       TIMESTAMPTZ,
    last_status         TEXT        NOT NULL DEFAULT '',
    last_error_at       TIMESTAMPTZ,
    last_error          TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_cron_jobs_cwd_not_empty     CHECK (cwd <> ''),
    CONSTRAINT chk_cron_jobs_provider_not_empty CHECK (provider <> ''),
    CONSTRAINT chk_cron_jobs_schedule_expr     CHECK (schedule_expr <> ''),
    CONSTRAINT chk_cron_jobs_max_attempts_nn   CHECK (max_attempts >= 0),
    CONSTRAINT chk_cron_jobs_failure_count_nn  CHECK (failure_count >= 0)
);

CREATE TABLE IF NOT EXISTS public.cron_job_runs (
    id                  TEXT        PRIMARY KEY,
    job_id              TEXT        NOT NULL REFERENCES public.cron_jobs(id) ON DELETE CASCADE,
    scheduled_at        TIMESTAMPTZ NOT NULL,
    idempotency_key     TEXT        NOT NULL,
    dedupe_key          TEXT        NOT NULL DEFAULT '',
    thread_id           TEXT        NOT NULL DEFAULT '',
    agent_id            TEXT        NOT NULL DEFAULT '',
    turn_id             TEXT        NOT NULL DEFAULT '',
    submitted_at        TIMESTAMPTZ,
    status              TEXT        NOT NULL DEFAULT 'pending',
    error               TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_cron_job_runs_status CHECK (
        status IN ('pending', 'submitting', 'submitted', 'running',
                   'finished', 'failed', 'observe_lost')
    )
);

CREATE INDEX IF NOT EXISTS idx_cron_jobs_due
    ON public.cron_jobs (COALESCE(next_retry_at, next_run_at))
    WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_cron_jobs_claim
    ON public.cron_jobs (claimed_by, lease_expires_at)
    WHERE claim_token <> '';

CREATE UNIQUE INDEX IF NOT EXISTS uq_cron_job_runs_idempotency
    ON public.cron_job_runs (job_id, idempotency_key);

CREATE UNIQUE INDEX IF NOT EXISTS uq_cron_job_runs_dedupe_key
    ON public.cron_job_runs (dedupe_key)
    WHERE dedupe_key <> '';

CREATE INDEX IF NOT EXISTS idx_cron_job_runs_job_created
    ON public.cron_job_runs (job_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_cron_job_runs_turn
    ON public.cron_job_runs (turn_id)
    WHERE turn_id <> '';
