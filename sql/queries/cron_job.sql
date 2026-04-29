-- CRUD --------------------------------------------------------------

-- name: CreateCronJob :one
INSERT INTO cron_jobs (
    id, name, prompt, schedule_type, schedule_expr, timezone, provider,
    model, cwd, config, skills, notify_channel, enabled, next_run_at,
    max_attempts, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(name), sqlc.arg(prompt), sqlc.arg(schedule_type),
    sqlc.arg(schedule_expr), sqlc.arg(timezone), sqlc.arg(provider),
    sqlc.arg(model), sqlc.arg(cwd),
    sqlc.arg(config)::jsonb,
    sqlc.arg(skills)::jsonb,
    sqlc.arg(notify_channel), sqlc.arg(enabled), sqlc.arg(next_run_at),
    sqlc.arg(max_attempts), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING id, name, prompt, schedule_type, schedule_expr, timezone, provider,
          model, cwd, config, skills, notify_channel, enabled, next_run_at,
          last_scheduled_at, last_run_at, claimed_at, claimed_by,
          lease_expires_at, claim_token, thread_id, agent_id, active_turn_id,
          last_turn_id, failure_count, max_attempts, next_retry_at,
          last_status, last_error_at, last_error, created_at, updated_at;

-- name: GetCronJobByID :one
SELECT id, name, prompt, schedule_type, schedule_expr, timezone, provider,
       model, cwd, config, skills, notify_channel, enabled, next_run_at,
       last_scheduled_at, last_run_at, claimed_at, claimed_by,
       lease_expires_at, claim_token, thread_id, agent_id, active_turn_id,
       last_turn_id, failure_count, max_attempts, next_retry_at,
       last_status, last_error_at, last_error, created_at, updated_at
FROM cron_jobs
WHERE id = $1;

-- name: ListCronJobs :many
SELECT id, name, prompt, schedule_type, schedule_expr, timezone, provider,
       model, cwd, config, skills, notify_channel, enabled, next_run_at,
       last_scheduled_at, last_run_at, claimed_at, claimed_by,
       lease_expires_at, claim_token, thread_id, agent_id, active_turn_id,
       last_turn_id, failure_count, max_attempts, next_retry_at,
       last_status, last_error_at, last_error, created_at, updated_at
FROM cron_jobs
ORDER BY created_at DESC, id DESC;

-- name: DeleteCronJob :exec
DELETE FROM cron_jobs WHERE id = $1;

-- name: UpdateCronJobSchedule :exec
UPDATE cron_jobs
SET name            = sqlc.arg(name),
    prompt          = sqlc.arg(prompt),
    schedule_type   = sqlc.arg(schedule_type),
    schedule_expr   = sqlc.arg(schedule_expr),
    timezone        = sqlc.arg(timezone),
    provider        = sqlc.arg(provider),
    model           = sqlc.arg(model),
    cwd             = sqlc.arg(cwd),
    config          = sqlc.arg(config)::jsonb,
    skills          = sqlc.arg(skills)::jsonb,
    notify_channel  = sqlc.arg(notify_channel),
    enabled         = sqlc.arg(enabled),
    next_run_at     = sqlc.arg(next_run_at),
    max_attempts    = sqlc.arg(max_attempts),
    updated_at      = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: SetCronJobEnabled :exec
UPDATE cron_jobs
SET enabled    = sqlc.arg(enabled),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- Claim / lease -----------------------------------------------------

-- ClaimDueJobs marks up to `limit` due rows as claimed by `claimed_by`.
-- The embedded SELECT ... FOR UPDATE SKIP LOCKED makes sure two
-- schedulers hitting the same tick never grab the same row. claim_token
-- is generated in the application layer (Go UUID) and passed in so no
-- Postgres extension (pgcrypto / uuid-ossp) is required.
--
-- name: ClaimDueJobs :many
WITH due AS (
    SELECT id
    FROM cron_jobs
    WHERE enabled = TRUE
      AND (claim_token = '' OR COALESCE(lease_expires_at, 'epoch'::timestamptz) <= sqlc.arg(now))
      AND COALESCE(next_retry_at, next_run_at) <= sqlc.arg(now)
    ORDER BY COALESCE(next_retry_at, next_run_at) ASC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(max_claim)
)
UPDATE cron_jobs AS j
SET claimed_by       = sqlc.arg(claimed_by),
    claimed_at       = sqlc.arg(now),
    lease_expires_at = sqlc.arg(lease_expires_at),
    claim_token      = sqlc.arg(claim_token),
    updated_at       = sqlc.arg(now)
FROM due
WHERE j.id = due.id
RETURNING j.id, j.name, j.prompt, j.schedule_type, j.schedule_expr,
          j.timezone, j.provider, j.model, j.cwd, j.config, j.skills,
          j.notify_channel, j.enabled, j.next_run_at, j.last_scheduled_at,
          j.last_run_at, j.claimed_at, j.claimed_by, j.lease_expires_at,
          j.claim_token, j.thread_id, j.agent_id, j.active_turn_id,
          j.last_turn_id, j.failure_count, j.max_attempts, j.next_retry_at,
          j.last_status, j.last_error_at, j.last_error, j.created_at,
          j.updated_at;

-- name: RenewLease :execrows
UPDATE cron_jobs
SET lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at       = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token);

-- name: ExtendClaim :execrows
UPDATE cron_jobs
SET lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at       = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token)
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at < sqlc.arg(lease_expires_at);

-- name: ReleaseClaim :execrows
UPDATE cron_jobs
SET claimed_by       = '',
    claimed_at       = NULL,
    lease_expires_at = NULL,
    claim_token      = '',
    updated_at       = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token);

-- MarkCronJobFinished releases the claim, records the successful run's
-- turn id and advances scheduling fields. Conditional on claim_token so
-- a late worker cannot overwrite terminal state after being preempted.
--
-- name: MarkCronJobFinished :execrows
UPDATE cron_jobs
SET claimed_by        = '',
    claimed_at        = NULL,
    lease_expires_at  = NULL,
    claim_token       = '',
    last_run_at       = sqlc.arg(last_run_at),
    last_turn_id      = sqlc.arg(last_turn_id),
    active_turn_id    = '',
    last_status       = 'finished',
    last_error_at     = NULL,
    last_error        = '',
    failure_count     = 0,
    next_run_at       = sqlc.arg(next_run_at),
    next_retry_at     = NULL,
    updated_at        = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token);

-- name: MarkCronJobFailed :execrows
UPDATE cron_jobs
SET claimed_by        = '',
    claimed_at        = NULL,
    lease_expires_at  = NULL,
    claim_token       = '',
    last_run_at       = sqlc.arg(last_run_at),
    last_turn_id      = sqlc.arg(last_turn_id),
    active_turn_id    = '',
    last_status       = sqlc.arg(last_status),
    last_error_at     = sqlc.arg(last_error_at),
    last_error        = sqlc.arg(last_error),
    failure_count     = failure_count + 1,
    next_retry_at     = sqlc.arg(next_retry_at),
    updated_at        = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token);

-- name: SetCronJobActiveTurn :execrows
UPDATE cron_jobs
SET active_turn_id = sqlc.arg(active_turn_id),
    thread_id      = COALESCE(NULLIF(sqlc.arg(thread_id), ''), thread_id),
    agent_id       = COALESCE(NULLIF(sqlc.arg(agent_id), ''), agent_id),
    updated_at     = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token);

-- cron_job_runs -----------------------------------------------------

-- name: InsertCronJobRun :one
INSERT INTO cron_job_runs (
    id, job_id, scheduled_at, idempotency_key, dedupe_key,
    status, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(job_id), sqlc.arg(scheduled_at),
    sqlc.arg(idempotency_key), sqlc.arg(dedupe_key),
    sqlc.arg(status), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
          agent_id, turn_id, submitted_at, status, error, created_at,
          updated_at;

-- CASCronJobRunStatus only advances the status when the current status
-- equals expected_status; protects three-phase transitions (pending ->
-- submitting -> submitted -> running -> finished/failed/observe_lost)
-- from stale worker overwrites.
--
-- name: CASCronJobRunStatus :execrows
UPDATE cron_job_runs
SET status     = sqlc.arg(next_status),
    error      = sqlc.arg(error),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = sqlc.arg(expected_status);

-- name: SetCronJobRunTurn :execrows
UPDATE cron_job_runs
SET turn_id       = sqlc.arg(turn_id),
    thread_id     = COALESCE(NULLIF(sqlc.arg(thread_id), ''), thread_id),
    agent_id      = COALESCE(NULLIF(sqlc.arg(agent_id), ''), agent_id),
    submitted_at  = sqlc.arg(submitted_at),
    updated_at    = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: GetCronJobRunByDedupeKey :one
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE dedupe_key = $1 AND dedupe_key <> '';

-- name: GetCronJobRunByID :one
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE id = $1;

-- name: ListCronJobRunsByJob :many
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE job_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: ListUnresolvedCronJobRuns :many
-- Used on scheduler boot for crash recovery: any run stuck in submitting /
-- submitted / running must be re-entered through Observe / LookupByDedupeKey
-- instead of StartTurn, per the three-phase protocol.
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE status IN ('submitting', 'submitted', 'running')
ORDER BY created_at ASC, id ASC;

-- name: GetRunningCronJobRunByTurnID :one
-- Used by CompleteTurn to locate the active run for a completed turn without
-- scanning all unresolved rows. turn_id is indexed by the dedupe_key B-tree
-- and the status guard ensures only one row can match (at most one run per
-- turn can be in 'running').
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE turn_id = $1 AND status = 'running'
LIMIT 1;

-- name: ListCronJobsClaimedBy :many
-- Used by RenewLeases / ExtendClaimForTurnProgress to fetch only the jobs
-- owned by this scheduler instance, avoiding a full-table scan of cron_jobs.
SELECT id, name, prompt, schedule_type, schedule_expr, timezone, provider,
       model, cwd, config, skills, notify_channel, enabled, next_run_at,
       last_scheduled_at, last_run_at, claimed_at, claimed_by,
       lease_expires_at, claim_token, thread_id, agent_id, active_turn_id,
       last_turn_id, failure_count, max_attempts, next_retry_at,
       last_status, last_error_at, last_error, created_at, updated_at
FROM cron_jobs
WHERE claimed_by = $1 AND claim_token <> ''
ORDER BY id ASC;
