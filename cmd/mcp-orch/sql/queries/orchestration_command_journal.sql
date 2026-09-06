-- name: PrepareOrchestrationCommandJournal :one
INSERT INTO orchestration_command_journal (
    command_id,
    command_version,
    version,
    operation_id,
    operation_digest,
    phase,
    accept_resolution,
    attempt_id,
    trigger,
    evidence_ref,
    uncertain_phase,
    schema_version
)
SELECT
    i.command_id,
    i.command_version,
    1,
    sqlc.arg(operation_id),
    sqlc.arg(operation_digest),
    'recovery_custody_pending',
    'pending',
    '',
    'prepare_accept',
    '',
    '',
    1
FROM orchestration_command_intents AS i
WHERE i.command_id = sqlc.arg(command_id)
  AND NOT EXISTS (
      SELECT 1
      FROM orchestration_command_journal AS j
      WHERE j.command_id = i.command_id
  )
RETURNING
    command_id,
    command_version,
    version,
    operation_id,
    operation_digest,
    phase,
    accept_resolution,
    attempt_id,
    trigger,
    evidence_ref,
    uncertain_phase,
    schema_version,
    recorded_at;

-- name: AppendOrchestrationCommandJournal :one
INSERT INTO orchestration_command_journal (
    command_id, command_version, version, operation_id, operation_digest,
    phase, accept_resolution, attempt_id, trigger, evidence_ref, uncertain_phase, schema_version
)
SELECT j.command_id, j.command_version, j.version + 1,
    sqlc.arg(operation_id), sqlc.arg(operation_digest),
    sqlc.arg(next_phase), sqlc.arg(next_accept_resolution), sqlc.arg(next_attempt_id),
    sqlc.arg(trigger), sqlc.arg(evidence_ref), sqlc.arg(uncertain_phase), 1
FROM orchestration_command_journal AS j
WHERE j.command_id = sqlc.arg(command_id)
  AND j.version = sqlc.arg(expected_version)
  AND j.phase = sqlc.arg(expected_phase)
  AND j.attempt_id = sqlc.arg(expected_attempt_id)
  AND j.accept_resolution = sqlc.arg(expected_accept_resolution)
  AND NOT EXISTS (
      SELECT 1 FROM orchestration_command_journal AS newer
      WHERE newer.command_id = j.command_id AND newer.version > j.version
  )
RETURNING
    command_id,
    command_version,
    version,
    operation_id,
    operation_digest,
    phase,
    accept_resolution,
    attempt_id,
    trigger,
    evidence_ref,
    uncertain_phase,
    schema_version,
    recorded_at;

-- name: GetOrchestrationCommandJournal :one
SELECT
    command_id,
    command_version,
    version,
    operation_id,
    operation_digest,
    phase,
    accept_resolution,
    attempt_id,
    trigger,
    evidence_ref,
    uncertain_phase,
    schema_version,
    recorded_at
FROM orchestration_command_journal
WHERE command_id = sqlc.arg(command_id)
ORDER BY version DESC
LIMIT 1;

-- name: GetOrchestrationCommandOperation :one
SELECT
    command_id,
    command_version,
    version,
    operation_id,
    operation_digest,
    phase,
    accept_resolution,
    attempt_id,
    trigger,
    evidence_ref,
    uncertain_phase,
    schema_version,
    recorded_at
FROM orchestration_command_journal
WHERE command_id = sqlc.arg(command_id)
  AND operation_id = sqlc.arg(operation_id);

-- name: GetOrchestrationCommandJournalVersion :one
SELECT
    command_id,
    command_version,
    version,
    operation_id,
    operation_digest,
    phase,
    accept_resolution,
    attempt_id,
    trigger,
    evidence_ref,
    uncertain_phase,
    schema_version,
    recorded_at
FROM orchestration_command_journal
WHERE command_id = sqlc.arg(command_id)
  AND version = sqlc.arg(version);

-- name: ListOrchestrationCommandJournal :many
SELECT
    command_id,
    command_version,
    version,
    operation_id,
    operation_digest,
    phase,
    accept_resolution,
    attempt_id,
    trigger,
    evidence_ref,
    uncertain_phase,
    schema_version,
    recorded_at
FROM orchestration_command_journal
WHERE command_id = sqlc.arg(command_id)
  AND version > sqlc.arg(after_version)
ORDER BY version ASC
LIMIT sqlc.arg(limit);
