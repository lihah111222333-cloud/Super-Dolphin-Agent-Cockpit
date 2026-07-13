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
    sqlc.arg(config),
    sqlc.arg(skills),
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
WHERE id = ?;

-- name: ListCronJobsPage :many
SELECT id, name, prompt, schedule_type, schedule_expr, timezone, provider,
       model, cwd, config, skills, notify_channel, enabled, next_run_at,
       last_scheduled_at, last_run_at, claimed_at, claimed_by,
       lease_expires_at, claim_token, thread_id, agent_id, active_turn_id,
       last_turn_id, failure_count, max_attempts, next_retry_at,
       last_status, last_error_at, last_error, created_at, updated_at
FROM cron_jobs
WHERE (created_at, id) < (CAST(sqlc.arg(cursor_created_at) AS INTEGER), CAST(sqlc.arg(cursor_id) AS TEXT))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_plus_one);

-- name: DeleteCronJob :exec
DELETE FROM cron_jobs WHERE id = ?;

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
    config          = sqlc.arg(config),
    skills          = sqlc.arg(skills),
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

-- name: PatchCronJobNextRunAt :exec
-- Narrow update used by RunOnce: only touches next_run_at + updated_at
-- without overwriting any other field, avoiding a read-modify-write race.
UPDATE cron_jobs
SET next_run_at = sqlc.arg(next_run_at),
    updated_at  = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- Claim / lease -----------------------------------------------------

-- ClaimDueJobsForUpdate marks up to `limit` due rows as claimed by `claimed_by`.
-- FOR UPDATE SKIP LOCKED removed: SQLite does not support it.
-- Task07 implements atomic claim with one UPDATE ... RETURNING statement.
-- The outer UPDATE repeats the availability predicate so claim ownership is
-- rechecked at write time, preserving fencing if another writer wins first.
--
-- name: ClaimDueJobsForUpdate :many
UPDATE cron_jobs
SET claimed_by       = sqlc.arg(claimed_by),
    claimed_at       = sqlc.arg(now),
    lease_expires_at = sqlc.arg(lease_expires_at),
    claim_token      = sqlc.arg(claim_token),
    updated_at       = sqlc.arg(now)
WHERE id IN (
    SELECT id
    FROM cron_jobs
    WHERE enabled = 1
      AND (claim_token = '' OR COALESCE(lease_expires_at, 0) <= sqlc.arg(now))
      AND COALESCE(next_retry_at, next_run_at) <= sqlc.arg(now)
    ORDER BY COALESCE(next_retry_at, next_run_at) ASC, id ASC
    LIMIT sqlc.arg(max_claim)
)
  AND enabled = 1
  AND (claim_token = '' OR COALESCE(lease_expires_at, 0) <= sqlc.arg(now))
  AND COALESCE(next_retry_at, next_run_at) <= sqlc.arg(now)
RETURNING id, name, prompt, schedule_type, schedule_expr,
          timezone, provider, model, cwd, config, skills,
          notify_channel, enabled, next_run_at, last_scheduled_at,
          last_run_at, claimed_at, claimed_by, lease_expires_at,
          claim_token, thread_id, agent_id, active_turn_id,
          last_turn_id, failure_count, max_attempts, next_retry_at,
          last_status, last_error_at, last_error, created_at,
          updated_at;

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

-- MarkCronJobFinished releases the claim only when the current job claim,
-- active turn, and already-finished run row all describe the same terminal
-- event. This prevents a late terminal event from an old run from releasing a
-- newer claim.
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
    updated_at        = sqlc.arg(updated_at)
WHERE cron_jobs.id = sqlc.arg(id)
  AND cron_jobs.claim_token = sqlc.arg(claim_token)
  AND cron_jobs.active_turn_id = sqlc.arg(expected_active_turn_id)
  AND EXISTS (
      SELECT 1
      FROM cron_job_runs
      WHERE cron_job_runs.id = sqlc.arg(run_id)
        AND cron_job_runs.job_id = cron_jobs.id
        AND cron_job_runs.turn_id = sqlc.arg(expected_active_turn_id)
        AND cron_job_runs.status = 'finished'
  );

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
    next_run_at       = sqlc.arg(next_run_at),
    next_retry_at     = sqlc.arg(next_retry_at),
    updated_at        = sqlc.arg(updated_at)
WHERE cron_jobs.id = sqlc.arg(id)
  AND cron_jobs.claim_token = sqlc.arg(claim_token)
  AND cron_jobs.active_turn_id = sqlc.arg(expected_active_turn_id)
  AND EXISTS (
      SELECT 1
      FROM cron_job_runs
      WHERE cron_job_runs.id = sqlc.arg(run_id)
        AND cron_job_runs.job_id = cron_jobs.id
        AND cron_job_runs.turn_id = sqlc.arg(expected_active_turn_id)
        AND cron_job_runs.status = sqlc.arg(last_status)
  );

-- name: SetCronJobActiveTurn :execrows
UPDATE cron_jobs
SET active_turn_id = sqlc.arg(active_turn_id),
    thread_id      = COALESCE(NULLIF(CAST(sqlc.arg(thread_id) AS TEXT), ''), thread_id),
    agent_id       = COALESCE(NULLIF(CAST(sqlc.arg(agent_id) AS TEXT), ''), agent_id),
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
    thread_id     = COALESCE(NULLIF(CAST(sqlc.arg(thread_id) AS TEXT), ''), thread_id),
    agent_id      = COALESCE(NULLIF(CAST(sqlc.arg(agent_id) AS TEXT), ''), agent_id),
    submitted_at  = sqlc.arg(submitted_at),
    updated_at    = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: GetCronJobRunByDedupeKey :one
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE dedupe_key = ? AND dedupe_key <> '';

-- name: GetCronJobRunByID :one
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE id = ?;

-- name: ListCronJobRunsByJob :many
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE job_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ?;

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

-- name: ListUnresolvedCronJobRunsPage :many
-- Bounded scheduler boot recovery page. The caller advances cursor with the
-- last returned id so startup recovery never materializes every unresolved row.
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE status IN ('submitting', 'submitted', 'running')
  AND (CAST(sqlc.arg(cursor) AS TEXT) = '' OR id > CAST(sqlc.arg(cursor) AS TEXT))
ORDER BY id ASC
LIMIT sqlc.arg(limit);

-- name: GetRunningCronJobRunByTurnID :one
-- Used by CompleteTurn to locate the active run for a completed turn without
-- scanning all unresolved rows. turn_id is indexed by the dedupe_key B-tree
-- and the status guard ensures only one row can match (at most one run per
-- turn can be in 'running').
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE turn_id = ? AND status = 'running'
LIMIT 1;

-- name: GetSubmittedOrRunningCronJobRunByTurnID :one
-- Used by CompleteTurn to locate early terminal events that arrive while a run
-- is still submitted, without falling back to the unresolved recovery scan.
SELECT id, job_id, scheduled_at, idempotency_key, dedupe_key, thread_id,
       agent_id, turn_id, submitted_at, status, error, created_at,
       updated_at
FROM cron_job_runs
WHERE turn_id = sqlc.arg(turn_id)
  AND turn_id <> ''
  AND status IN ('submitted', 'running')
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
WHERE claimed_by = ? AND claim_token <> ''
ORDER BY id ASC;
