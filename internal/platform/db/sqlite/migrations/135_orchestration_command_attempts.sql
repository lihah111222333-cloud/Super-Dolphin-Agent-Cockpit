-- Durable record-before-effect attempts for orchestration commands.
-- This table is intentionally independent from direct-peer delivery, event
-- outboxes and command cards.  It records the one provider boundary handle
-- for an idempotent command; it never invokes a provider.
CREATE TABLE orchestration_command_attempts (
    command_id TEXT NOT NULL
        REFERENCES orchestration_command_intents(command_id) ON DELETE RESTRICT,
    attempt_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(attempt_id)) > 0 AND length(attempt_id) <= 128),
    wakeup_id TEXT NOT NULL
        CHECK(length(trim(wakeup_id)) > 0 AND length(wakeup_id) <= 256),
    wakeup_fence TEXT NOT NULL
        CHECK(length(trim(wakeup_fence)) > 0 AND length(wakeup_fence) <= 256),
    idempotency_namespace TEXT NOT NULL
        CHECK(length(trim(idempotency_namespace)) > 0 AND length(idempotency_namespace) <= 256),
    idempotency_key_digest TEXT NOT NULL CHECK(
        length(idempotency_key_digest) = 64
        AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    request_digest TEXT NOT NULL CHECK(
        length(request_digest) = 64
        AND request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    attempt_number INTEGER NOT NULL CHECK(attempt_number = 1),
    dispatch_receipt TEXT NOT NULL
        CHECK(length(trim(dispatch_receipt)) > 0 AND length(dispatch_receipt) <= 256),
    permit TEXT NOT NULL
        CHECK(length(trim(permit)) > 0 AND length(permit) <= 256),
    nonce TEXT NOT NULL
        CHECK(length(trim(nonce)) > 0 AND length(nonce) <= 256),
    phase TEXT NOT NULL CHECK(phase IN (
        'effect_pending', 'outcome_unknown', 'provider_accepted',
        'provider_definitive_failure'
    )),
    outcome TEXT NOT NULL CHECK(outcome IN (
        'pending', 'unknown', 'accepted', 'definitive_failure'
    )),
    provider_submission_id TEXT NOT NULL DEFAULT ''
        CHECK(length(provider_submission_id) <= 256
            AND trim(provider_submission_id) = provider_submission_id
            AND instr(provider_submission_id, char(0)) = 0),
    provider_receipt_id TEXT NOT NULL DEFAULT ''
        CHECK(length(provider_receipt_id) <= 256
            AND trim(provider_receipt_id) = provider_receipt_id
            AND instr(provider_receipt_id, char(0)) = 0),
    failure_code TEXT NOT NULL DEFAULT ''
        CHECK(length(failure_code) <= 256
            AND trim(failure_code) = failure_code
            AND instr(failure_code, char(0)) = 0),
    recorded_at TEXT NOT NULL
        CHECK(length(trim(recorded_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(command_id),
    UNIQUE(idempotency_namespace, idempotency_key_digest),
    CHECK(
        (phase = 'effect_pending' AND outcome = 'pending'
            AND provider_submission_id = '' AND provider_receipt_id = '' AND failure_code = '')
        OR (phase = 'outcome_unknown' AND outcome = 'unknown'
            AND provider_submission_id = '' AND provider_receipt_id = '' AND failure_code = '')
        OR (phase = 'provider_accepted' AND outcome = 'accepted'
            AND failure_code = '' AND (provider_submission_id <> '' OR provider_receipt_id <> ''))
        OR (phase = 'provider_definitive_failure' AND outcome = 'definitive_failure'
            AND failure_code <> '' AND provider_submission_id = '' AND provider_receipt_id = '')
    )
) STRICT;

CREATE INDEX idx_orchestration_command_attempts_command
    ON orchestration_command_attempts(command_id);

CREATE TRIGGER trg_orchestration_command_attempts_intent_binding
BEFORE INSERT ON orchestration_command_attempts
WHEN EXISTS (
    SELECT 1 FROM orchestration_command_intents AS i
    WHERE i.command_id = NEW.command_id
      AND (i.idempotency_namespace <> NEW.idempotency_namespace
           OR i.idempotency_key_digest <> NEW.idempotency_key_digest
           OR i.request_digest <> NEW.request_digest)
)
BEGIN
    SELECT RAISE(ABORT, 'orchestration command attempt does not match command intent');
END;

CREATE TRIGGER trg_orchestration_command_attempts_immutable_update
BEFORE UPDATE OF command_id, attempt_id, wakeup_id, wakeup_fence,
    idempotency_namespace, idempotency_key_digest, request_digest,
    attempt_number, dispatch_receipt, permit, nonce, recorded_at
ON orchestration_command_attempts
BEGIN
    SELECT RAISE(ABORT, 'orchestration command attempt identity is immutable');
END;

CREATE TRIGGER trg_orchestration_command_attempts_immutable_delete
BEFORE DELETE ON orchestration_command_attempts
BEGIN
    SELECT RAISE(ABORT, 'orchestration command attempts are immutable');
END;

CREATE TRIGGER trg_orchestration_command_attempts_outcome_transition
BEFORE UPDATE OF phase, outcome, provider_submission_id, provider_receipt_id, failure_code
ON orchestration_command_attempts
WHEN NOT (
    (OLD.phase = 'effect_pending' AND NEW.phase IN (
        'effect_pending', 'outcome_unknown', 'provider_accepted',
        'provider_definitive_failure'
    ))
    OR (OLD.phase = 'outcome_unknown' AND NEW.phase IN (
        'outcome_unknown', 'provider_accepted', 'provider_definitive_failure'
    ))
    OR (OLD.phase IN ('provider_accepted', 'provider_definitive_failure')
        AND NEW.phase = OLD.phase AND NEW.outcome = OLD.outcome
        AND NEW.provider_submission_id = OLD.provider_submission_id
        AND NEW.provider_receipt_id = OLD.provider_receipt_id
        AND NEW.failure_code = OLD.failure_code)
)
BEGIN
    SELECT RAISE(ABORT, 'orchestration command attempt outcome transition is invalid');
END;
