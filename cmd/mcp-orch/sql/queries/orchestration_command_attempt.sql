-- name: CreateOrchestrationCommandAttempt :one
INSERT INTO orchestration_command_attempts (
    command_id, attempt_id, wakeup_id, wakeup_fence,
    idempotency_namespace, idempotency_key_digest, request_digest,
    attempt_number, dispatch_receipt, permit, nonce, phase, outcome
) VALUES (
    sqlc.arg(command_id), sqlc.arg(attempt_id), sqlc.arg(wakeup_id), sqlc.arg(wakeup_fence),
    sqlc.arg(idempotency_namespace), sqlc.arg(idempotency_key_digest), sqlc.arg(request_digest),
    1, sqlc.arg(dispatch_receipt), sqlc.arg(permit), sqlc.arg(nonce),
    'effect_pending', 'pending'
)
ON CONFLICT(idempotency_namespace, idempotency_key_digest) DO NOTHING
RETURNING command_id, attempt_id, wakeup_id, wakeup_fence,
    idempotency_namespace, idempotency_key_digest, request_digest,
    attempt_number, dispatch_receipt, permit, nonce, phase, outcome,
    provider_submission_id, provider_receipt_id, failure_code, recorded_at;

-- name: ConsumeOrchestrationCommandAttemptPermit :one
-- Consume is the provider-effect precondition. The existing attempt phase is
-- reused as the durable one-shot marker: effect_pending/pending may move once
-- to outcome_unknown/unknown, while every later consume observes zero rows.
UPDATE orchestration_command_attempts
SET phase = 'outcome_unknown',
    outcome = 'unknown'
WHERE command_id = sqlc.arg(command_id)
  AND attempt_id = sqlc.arg(attempt_id)
  AND wakeup_id = sqlc.arg(wakeup_id)
  AND wakeup_fence = sqlc.arg(wakeup_fence)
  AND request_digest = sqlc.arg(request_digest)
  AND dispatch_receipt = sqlc.arg(dispatch_receipt)
  AND permit = sqlc.arg(permit)
  AND nonce = sqlc.arg(nonce)
  AND attempt_number = 1
  AND phase = 'effect_pending'
  AND outcome = 'pending'
RETURNING command_id, attempt_id, wakeup_id, wakeup_fence,
    idempotency_namespace, idempotency_key_digest, request_digest,
    attempt_number, dispatch_receipt, permit, nonce, phase, outcome,
    provider_submission_id, provider_receipt_id, failure_code, recorded_at;

-- name: GetOrchestrationCommandAttemptByKey :one
SELECT command_id, attempt_id, wakeup_id, wakeup_fence,
    idempotency_namespace, idempotency_key_digest, request_digest,
    attempt_number, dispatch_receipt, permit, nonce, phase, outcome,
    provider_submission_id, provider_receipt_id, failure_code, recorded_at
FROM orchestration_command_attempts
WHERE idempotency_namespace = sqlc.arg(idempotency_namespace)
  AND idempotency_key_digest = sqlc.arg(idempotency_key_digest);

-- name: GetOrchestrationCommandAttemptByCommand :one
SELECT command_id, attempt_id, wakeup_id, wakeup_fence,
    idempotency_namespace, idempotency_key_digest, request_digest,
    attempt_number, dispatch_receipt, permit, nonce, phase, outcome,
    provider_submission_id, provider_receipt_id, failure_code, recorded_at
FROM orchestration_command_attempts
WHERE command_id = sqlc.arg(command_id);

-- name: UpdateOrchestrationCommandAttemptOutcome :one
UPDATE orchestration_command_attempts
SET phase = sqlc.arg(next_phase),
    outcome = sqlc.arg(next_outcome),
    provider_submission_id = sqlc.arg(provider_submission_id),
    provider_receipt_id = sqlc.arg(provider_receipt_id),
    failure_code = sqlc.arg(failure_code)
WHERE command_id = sqlc.arg(command_id)
  AND attempt_id = sqlc.arg(attempt_id)
  AND wakeup_fence = sqlc.arg(wakeup_fence)
  AND request_digest = sqlc.arg(request_digest)
  AND dispatch_receipt = sqlc.arg(dispatch_receipt)
  AND permit = sqlc.arg(permit)
  AND nonce = sqlc.arg(nonce)
  AND phase = sqlc.arg(expected_phase)
  AND outcome = sqlc.arg(expected_outcome)
RETURNING command_id, attempt_id, wakeup_id, wakeup_fence,
    idempotency_namespace, idempotency_key_digest, request_digest,
    attempt_number, dispatch_receipt, permit, nonce, phase, outcome,
    provider_submission_id, provider_receipt_id, failure_code, recorded_at;
