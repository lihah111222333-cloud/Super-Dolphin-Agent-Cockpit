-- Canonical command intent owner. Creating an intent is not acceptance or dispatch authorization.
-- Keep this record append-only; attempts and receipts belong to a later migration.
CREATE TABLE orchestration_command_intents (
    command_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(command_id)) > 0 AND length(command_id) <= 128),
    command_kind TEXT NOT NULL CHECK(command_kind IN (
        'launch_agent', 'fork_agent', 'send_message', 'interrupt_agent',
        'stop_agent', 'recover_agent', 'room_broadcast', 'room_round'
    )),
    caller_principal_ref TEXT NOT NULL
        CHECK(
            length(caller_principal_ref) > 0
            AND length(caller_principal_ref) <= 512
            AND trim(caller_principal_ref) = caller_principal_ref
            AND instr(caller_principal_ref, char(0)) = 0
        ),
    scope_ref TEXT NOT NULL
        CHECK(
            length(scope_ref) > 0
            AND length(scope_ref) <= 512
            AND trim(scope_ref) = scope_ref
            AND instr(scope_ref, char(0)) = 0
        ),
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
    digest_key_id TEXT NOT NULL
        CHECK(length(trim(digest_key_id)) > 0 AND length(digest_key_id) <= 128),
    request_content_ref TEXT NOT NULL
        CHECK(length(trim(request_content_ref)) > 0 AND length(request_content_ref) <= 512),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512),
    target_fence_ref TEXT NOT NULL
        CHECK(length(trim(target_fence_ref)) > 0 AND length(target_fence_ref) <= 512),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    command_version INTEGER NOT NULL CHECK(command_version = 1),
    recorded_at TEXT NOT NULL
        CHECK(length(trim(recorded_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(idempotency_namespace, idempotency_key_digest)
) STRICT;

CREATE TRIGGER trg_orchestration_command_intents_immutable_update
BEFORE UPDATE ON orchestration_command_intents
BEGIN
    SELECT RAISE(ABORT, 'orchestration command intents are immutable');
END;

CREATE TRIGGER trg_orchestration_command_intents_immutable_delete
BEFORE DELETE ON orchestration_command_intents
BEGIN
    SELECT RAISE(ABORT, 'orchestration command intents are immutable');
END;
