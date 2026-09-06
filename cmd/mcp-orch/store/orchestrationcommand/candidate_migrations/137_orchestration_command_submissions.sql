-- Candidate-only accept-resolution submission fence; never dispatches providers.
CREATE TABLE orchestration_command_submissions (
 command_id TEXT NOT NULL REFERENCES orchestration_command_intents(command_id) ON DELETE RESTRICT
     CHECK(length(trim(command_id)) > 0 AND length(command_id) <= 128 AND trim(command_id) = command_id AND instr(command_id, char(0)) = 0),
 command_version INTEGER NOT NULL CHECK(command_version = 1),
 submission_id TEXT PRIMARY KEY NOT NULL CHECK(length(trim(submission_id)) > 0 AND length(submission_id) <= 128 AND trim(submission_id) = submission_id AND instr(submission_id, char(0)) = 0),
 subject_selector TEXT NOT NULL CHECK(length(trim(subject_selector)) > 0 AND length(subject_selector) <= 512 AND trim(subject_selector) = subject_selector AND instr(subject_selector, char(0)) = 0),
 caller_principal_ref TEXT NOT NULL CHECK(length(trim(caller_principal_ref)) > 0 AND length(caller_principal_ref) <= 512 AND trim(caller_principal_ref) = caller_principal_ref AND instr(caller_principal_ref, char(0)) = 0),
 scope_ref TEXT NOT NULL CHECK(length(trim(scope_ref)) > 0 AND length(scope_ref) <= 512 AND trim(scope_ref) = scope_ref AND instr(scope_ref, char(0)) = 0),
 source_fence_ref TEXT NOT NULL CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512 AND trim(source_fence_ref) = source_fence_ref AND instr(source_fence_ref, char(0)) = 0),
 target_fence_ref TEXT NOT NULL CHECK(length(trim(target_fence_ref)) > 0 AND length(target_fence_ref) <= 512 AND trim(target_fence_ref) = target_fence_ref AND instr(target_fence_ref, char(0)) = 0),
 idempotency_namespace TEXT NOT NULL CHECK(length(trim(idempotency_namespace)) > 0 AND length(idempotency_namespace) <= 256 AND trim(idempotency_namespace) = idempotency_namespace AND instr(idempotency_namespace, char(0)) = 0),
 idempotency_key_digest TEXT NOT NULL CHECK(length(idempotency_key_digest) = 64 AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'),
 request_digest TEXT NOT NULL CHECK(length(request_digest) = 64 AND request_digest NOT GLOB '*[^0-9a-f]*'),
 pending_cursor TEXT NOT NULL CHECK(length(trim(pending_cursor)) > 0 AND length(pending_cursor) <= 256 AND trim(pending_cursor) = pending_cursor AND instr(pending_cursor, char(0)) = 0),
 pending_cursor_digest TEXT NOT NULL CHECK(length(pending_cursor_digest) = 64 AND pending_cursor_digest NOT GLOB '*[^0-9a-f]*'),
 accept_resolution TEXT NOT NULL CHECK(accept_resolution IN ('pending', 'accepted', 'aborted')),
 resolution_kind TEXT NOT NULL CHECK(resolution_kind IN ('pending', 'accepted', 'rejected')),
 accept_receipt_ref TEXT NOT NULL DEFAULT '' CHECK(length(accept_receipt_ref) <= 512 AND trim(accept_receipt_ref) = accept_receipt_ref AND instr(accept_receipt_ref, char(0)) = 0),
 accept_receipt_digest TEXT NOT NULL DEFAULT '' CHECK(accept_receipt_digest = '' OR (length(accept_receipt_digest) = 64 AND accept_receipt_digest NOT GLOB '*[^0-9a-f]*')),
 reject_receipt_ref TEXT NOT NULL DEFAULT '' CHECK(length(reject_receipt_ref) <= 512 AND trim(reject_receipt_ref) = reject_receipt_ref AND instr(reject_receipt_ref, char(0)) = 0),
 reject_receipt_digest TEXT NOT NULL DEFAULT '' CHECK(reject_receipt_digest = '' OR (length(reject_receipt_digest) = 64 AND reject_receipt_digest NOT GLOB '*[^0-9a-f]*')),
 recovery_receipt_ref TEXT NOT NULL DEFAULT '' CHECK(length(recovery_receipt_ref) <= 512 AND trim(recovery_receipt_ref) = recovery_receipt_ref AND instr(recovery_receipt_ref, char(0)) = 0),
 recovery_receipt_digest TEXT NOT NULL DEFAULT '' CHECK(recovery_receipt_digest = '' OR (length(recovery_receipt_digest) = 64 AND recovery_receipt_digest NOT GLOB '*[^0-9a-f]*')),
 accepted_command_fence_ref TEXT NOT NULL DEFAULT '' CHECK(length(accepted_command_fence_ref) <= 512 AND trim(accepted_command_fence_ref) = accepted_command_fence_ref AND instr(accepted_command_fence_ref, char(0)) = 0),
 accepted_command_fence_digest TEXT NOT NULL DEFAULT '' CHECK(accepted_command_fence_digest = '' OR (length(accepted_command_fence_digest) = 64 AND accepted_command_fence_digest NOT GLOB '*[^0-9a-f]*')),
 accepted_command_fence_cursor TEXT NOT NULL DEFAULT '' CHECK(length(accepted_command_fence_cursor) <= 256 AND trim(accepted_command_fence_cursor) = accepted_command_fence_cursor AND instr(accepted_command_fence_cursor, char(0)) = 0),
 rejected_command_fence_ref TEXT NOT NULL DEFAULT '' CHECK(length(rejected_command_fence_ref) <= 512 AND trim(rejected_command_fence_ref) = rejected_command_fence_ref AND instr(rejected_command_fence_ref, char(0)) = 0),
 rejected_command_fence_digest TEXT NOT NULL DEFAULT '' CHECK(rejected_command_fence_digest = '' OR (length(rejected_command_fence_digest) = 64 AND rejected_command_fence_digest NOT GLOB '*[^0-9a-f]*')),
 rejected_command_fence_cursor TEXT NOT NULL DEFAULT '' CHECK(length(rejected_command_fence_cursor) <= 256 AND trim(rejected_command_fence_cursor) = rejected_command_fence_cursor AND instr(rejected_command_fence_cursor, char(0)) = 0),
 revision INTEGER NOT NULL CHECK(revision > 0),
 recorded_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')) CHECK(length(trim(recorded_at)) > 0 AND trim(recorded_at) = recorded_at AND instr(recorded_at, char(0)) = 0 AND julianday(recorded_at) IS NOT NULL),
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')) CHECK(length(trim(updated_at)) > 0 AND trim(updated_at) = updated_at AND instr(updated_at, char(0)) = 0 AND julianday(updated_at) IS NOT NULL),
 UNIQUE(command_id), UNIQUE(idempotency_namespace, idempotency_key_digest),
 CHECK(
    (
        (accept_resolution = 'pending' AND resolution_kind = 'pending' AND accept_receipt_ref = '' AND accept_receipt_digest = '' AND reject_receipt_ref = '' AND reject_receipt_digest = '' AND recovery_receipt_ref = '' AND recovery_receipt_digest = '' AND accepted_command_fence_ref = '' AND accepted_command_fence_digest = '' AND accepted_command_fence_cursor = '' AND rejected_command_fence_ref = '' AND rejected_command_fence_digest = '' AND rejected_command_fence_cursor = '')
        OR (accept_resolution = 'accepted' AND resolution_kind = 'accepted' AND accept_receipt_ref <> '' AND accept_receipt_digest <> '' AND recovery_receipt_ref <> '' AND recovery_receipt_digest <> '' AND reject_receipt_ref = '' AND reject_receipt_digest = '' AND accepted_command_fence_ref <> '' AND accepted_command_fence_digest <> '' AND accepted_command_fence_cursor <> '' AND rejected_command_fence_ref = '' AND rejected_command_fence_digest = '' AND rejected_command_fence_cursor = '')
        OR (accept_resolution = 'aborted' AND resolution_kind = 'rejected' AND reject_receipt_ref <> '' AND reject_receipt_digest <> '' AND recovery_receipt_ref <> '' AND recovery_receipt_digest <> '' AND accept_receipt_ref = '' AND accept_receipt_digest = '' AND accepted_command_fence_ref = '' AND accepted_command_fence_digest = '' AND accepted_command_fence_cursor = '' AND rejected_command_fence_ref <> '' AND rejected_command_fence_digest <> '' AND rejected_command_fence_cursor <> '')
    )
    AND julianday(updated_at) >= julianday(recorded_at)
 )
) STRICT;
CREATE INDEX idx_orchestration_command_submissions_command ON orchestration_command_submissions(command_id);
CREATE TRIGGER trg_orchestration_command_submissions_intent_binding
BEFORE INSERT ON orchestration_command_submissions
WHEN EXISTS (SELECT 1 FROM orchestration_command_intents i WHERE i.command_id = NEW.command_id AND (i.command_version <> NEW.command_version OR i.idempotency_namespace <> NEW.idempotency_namespace OR i.idempotency_key_digest <> NEW.idempotency_key_digest OR i.request_digest <> NEW.request_digest))
BEGIN SELECT RAISE(ABORT, 'orchestration command submission does not match command intent'); END;
CREATE TRIGGER trg_orchestration_command_submissions_immutable_update
BEFORE UPDATE OF command_id, command_version, submission_id, subject_selector, caller_principal_ref, scope_ref, source_fence_ref, target_fence_ref, idempotency_namespace, idempotency_key_digest, request_digest, pending_cursor, pending_cursor_digest, recorded_at
ON orchestration_command_submissions
BEGIN SELECT RAISE(ABORT, 'orchestration command submission identity is immutable'); END;
CREATE TRIGGER trg_orchestration_command_submissions_immutable_delete
BEFORE DELETE ON orchestration_command_submissions
BEGIN SELECT RAISE(ABORT, 'orchestration command submissions are immutable'); END;
CREATE TRIGGER trg_orchestration_command_submissions_resolution
BEFORE UPDATE OF accept_resolution, resolution_kind, accept_receipt_ref, accept_receipt_digest, reject_receipt_ref, reject_receipt_digest, recovery_receipt_ref, recovery_receipt_digest, accepted_command_fence_ref, accepted_command_fence_digest, accepted_command_fence_cursor, rejected_command_fence_ref, rejected_command_fence_digest, rejected_command_fence_cursor, revision, updated_at
ON orchestration_command_submissions
WHEN NOT ((OLD.accept_resolution = 'pending' AND NEW.accept_resolution IN ('accepted', 'aborted') AND NEW.revision = OLD.revision + 1)
       OR (OLD.accept_resolution IN ('accepted', 'aborted') AND NEW.accept_resolution = OLD.accept_resolution AND NEW.revision = OLD.revision
           AND NEW.accept_receipt_ref = OLD.accept_receipt_ref AND NEW.accept_receipt_digest = OLD.accept_receipt_digest
           AND NEW.reject_receipt_ref = OLD.reject_receipt_ref AND NEW.reject_receipt_digest = OLD.reject_receipt_digest
           AND NEW.recovery_receipt_ref = OLD.recovery_receipt_ref AND NEW.recovery_receipt_digest = OLD.recovery_receipt_digest
           AND NEW.resolution_kind = OLD.resolution_kind
           AND NEW.accepted_command_fence_ref = OLD.accepted_command_fence_ref AND NEW.accepted_command_fence_digest = OLD.accepted_command_fence_digest AND NEW.accepted_command_fence_cursor = OLD.accepted_command_fence_cursor
           AND NEW.rejected_command_fence_ref = OLD.rejected_command_fence_ref AND NEW.rejected_command_fence_digest = OLD.rejected_command_fence_digest AND NEW.rejected_command_fence_cursor = OLD.rejected_command_fence_cursor
           AND NEW.updated_at = OLD.updated_at))
BEGIN SELECT RAISE(ABORT, 'orchestration command submission resolution is sticky'); END;
