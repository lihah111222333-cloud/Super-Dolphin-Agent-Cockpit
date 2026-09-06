-- Append-only command lifecycle observations. This table stores only verified
-- receipts and state transitions; it never grants execution or calls a provider.
CREATE TABLE orchestration_command_journal (
    command_id TEXT NOT NULL
        REFERENCES orchestration_command_intents(command_id) ON DELETE RESTRICT,
    command_version INTEGER NOT NULL
        CHECK(command_version = 1),
    version INTEGER NOT NULL
        CHECK(version > 0),
    operation_id TEXT NOT NULL
        CHECK(length(trim(operation_id)) > 0 AND length(operation_id) <= 128),
    operation_digest TEXT NOT NULL CHECK(
        length(operation_digest) = 64
        AND operation_digest NOT GLOB '*[^0-9a-f]*'
    ),
    phase TEXT NOT NULL CHECK(phase IN (
        'recovery_custody_pending', 'accepted', 'dispatch_custody_pending',
        'dispatched', 'effect_authorization_pending', 'effect_token_issued',
        'external_effect_started', 'provider_acknowledged', 'completed',
        'dispatch_aborted_before_call', 'effect_aborted_before_issue',
        'effect_aborted_before_start', 'abort_cleanup_pending',
        'outcome_unknown', 'rejected', 'failed'
    )),
    accept_resolution TEXT NOT NULL CHECK(accept_resolution IN ('pending', 'accepted', 'aborted')),
    attempt_id TEXT NOT NULL
        CHECK(length(attempt_id) <= 128),
    trigger TEXT NOT NULL
        CHECK(length(trim(trigger)) > 0 AND length(trigger) <= 64),
    evidence_ref TEXT NOT NULL
        CHECK(length(evidence_ref) <= 512),
    uncertain_phase TEXT NOT NULL,
    schema_version INTEGER NOT NULL
        CHECK(schema_version = 1),
    recorded_at TEXT NOT NULL
        CHECK(length(trim(recorded_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY(command_id, version),
    UNIQUE(command_id, operation_id),
    CHECK(
        version <> 1 OR (
            phase = 'recovery_custody_pending'
            AND accept_resolution = 'pending'
            AND attempt_id = ''
            AND trigger = 'prepare_accept'
            AND evidence_ref = ''
            AND uncertain_phase = ''
        )
    ),
    CHECK(version = 1 OR length(evidence_ref) > 0),
    CHECK(
        (accept_resolution = 'pending' AND phase = 'recovery_custody_pending')
        OR (accept_resolution = 'aborted' AND phase = 'rejected')
        OR (accept_resolution = 'accepted' AND phase <> 'recovery_custody_pending')
    ),
    CHECK(
        (phase = 'outcome_unknown' AND uncertain_phase IN (
            'effect_token_issued', 'external_effect_started', 'provider_acknowledged'
        ))
        OR (phase <> 'outcome_unknown' AND uncertain_phase = '')
    ),
    CHECK(
        version = 1 OR trigger <> 'prepare_accept'
    ),
    CHECK(
        attempt_id <> '' OR phase IN (
            'recovery_custody_pending', 'accepted', 'rejected'
        )
    ),
    CHECK(
        phase NOT IN ('recovery_custody_pending', 'accepted')
        OR attempt_id = ''
    ),
    CHECK(
        accept_resolution <> 'aborted' OR attempt_id = ''
    ),
    CHECK(
        phase NOT IN ('completed', 'dispatched', 'abort_cleanup_pending', 'outcome_unknown')
        OR attempt_id <> ''
    )
) STRICT;

CREATE INDEX idx_orchestration_command_journal_attempt
    ON orchestration_command_journal(command_id, attempt_id);

-- An attempt may have many observations, but it can be started only once.
CREATE UNIQUE INDEX idx_orchestration_command_journal_new_attempt
    ON orchestration_command_journal(command_id, attempt_id)
    WHERE trigger IN ('begin_dispatch', 'reclaim_unsent');

CREATE TRIGGER trg_orchestration_command_journal_immutable_update
BEFORE UPDATE ON orchestration_command_journal
BEGIN
    SELECT RAISE(ABORT, 'orchestration command journal is immutable');
END;

CREATE TRIGGER trg_orchestration_command_journal_immutable_delete
BEFORE DELETE ON orchestration_command_journal
BEGIN
    SELECT RAISE(ABORT, 'orchestration command journal is immutable');
END;

CREATE TRIGGER trg_orchestration_command_journal_contiguous_version
BEFORE INSERT ON orchestration_command_journal
WHEN NEW.version <> (
    SELECT COALESCE(MAX(version), 0) + 1
    FROM orchestration_command_journal
    WHERE command_id = NEW.command_id
)
BEGIN
    SELECT RAISE(ABORT, 'orchestration command journal version must be contiguous');
END;
