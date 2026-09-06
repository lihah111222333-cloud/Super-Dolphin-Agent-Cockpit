-- One-to-one, non-authorizing profile for an identity- and grant-bound send_message intent.
-- This table does not record acceptance, recovery custody, dispatch, provider receipt,
-- CONTENT verification or RESOURCE admission.
CREATE TABLE orchestration_command_intent_profiles (
    command_id TEXT PRIMARY KEY NOT NULL
        REFERENCES orchestration_command_intents(command_id) ON DELETE RESTRICT,
    profile_kind TEXT NOT NULL CHECK(profile_kind = 'send_message_v1'),
    profile_version INTEGER NOT NULL CHECK(profile_version = 1),

    caller_profile TEXT NOT NULL CHECK(
        length(caller_profile) > 0 AND length(caller_profile) <= 512
        AND trim(caller_profile) = caller_profile
        AND instr(caller_profile, char(0)) = 0
    ),
    caller_auth_context TEXT NOT NULL CHECK(
        length(caller_auth_context) > 0 AND length(caller_auth_context) <= 512
        AND trim(caller_auth_context) = caller_auth_context
        AND instr(caller_auth_context, char(0)) = 0
    ),
    caller_auth_expires_at TEXT NOT NULL CHECK(
        length(caller_auth_expires_at) > 0 AND length(caller_auth_expires_at) <= 64
        AND trim(caller_auth_expires_at) = caller_auth_expires_at
        AND instr(caller_auth_expires_at, char(0)) = 0
        AND julianday(caller_auth_expires_at) IS NOT NULL
    ),

    authorization_kind TEXT NOT NULL CHECK(authorization_kind = 'access_grant_allow_v1'),
    authorization_decision TEXT NOT NULL CHECK(authorization_decision = 'allowed'),
    authorization_request_version INTEGER NOT NULL CHECK(authorization_request_version = 1),
    authorization_request_id TEXT NOT NULL CHECK(
        length(authorization_request_id) > 0 AND length(authorization_request_id) <= 128
        AND trim(authorization_request_id) = authorization_request_id
        AND instr(authorization_request_id, char(0)) = 0
    ),
    authorization_request_digest TEXT NOT NULL CHECK(
        length(authorization_request_digest) = 64
        AND authorization_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    authorization_grant_id TEXT NOT NULL
        REFERENCES orchestration_access_grants(grant_id) ON DELETE RESTRICT,
    authorization_grant_revision INTEGER NOT NULL CHECK(authorization_grant_revision > 0),
    authorization_capability TEXT NOT NULL CHECK(authorization_capability = 'send'),
    authorization_requested_at TEXT NOT NULL CHECK(
        length(authorization_requested_at) > 0 AND length(authorization_requested_at) <= 64
        AND trim(authorization_requested_at) = authorization_requested_at
        AND instr(authorization_requested_at, char(0)) = 0
        AND julianday(authorization_requested_at) IS NOT NULL
    ),
    authorization_decided_at TEXT NOT NULL CHECK(
        length(authorization_decided_at) > 0 AND length(authorization_decided_at) <= 64
        AND trim(authorization_decided_at) = authorization_decided_at
        AND instr(authorization_decided_at, char(0)) = 0
        AND julianday(authorization_decided_at) IS NOT NULL
    ),
    authorization_expires_at TEXT NOT NULL CHECK(
        length(authorization_expires_at) > 0 AND length(authorization_expires_at) <= 64
        AND trim(authorization_expires_at) = authorization_expires_at
        AND instr(authorization_expires_at, char(0)) = 0
        AND julianday(authorization_expires_at) IS NOT NULL
    ),
    grant_request_digest TEXT NOT NULL CHECK(
        length(grant_request_digest) = 64
        AND grant_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    grant_issued_at TEXT NOT NULL CHECK(
        length(grant_issued_at) > 0 AND length(grant_issued_at) <= 64
        AND trim(grant_issued_at) = grant_issued_at
        AND instr(grant_issued_at, char(0)) = 0
        AND julianday(grant_issued_at) IS NOT NULL
    ),
    grant_updated_at TEXT NOT NULL CHECK(
        length(grant_updated_at) > 0 AND length(grant_updated_at) <= 64
        AND trim(grant_updated_at) = grant_updated_at
        AND instr(grant_updated_at, char(0)) = 0
        AND julianday(grant_updated_at) IS NOT NULL
    ),

    source_selector_version INTEGER NOT NULL CHECK(source_selector_version = 1),
    source_selector_kind TEXT NOT NULL CHECK(source_selector_kind = 'active_binding'),
    source_agent_id TEXT NOT NULL CHECK(
        length(source_agent_id) > 0 AND length(source_agent_id) <= 128
        AND trim(source_agent_id) = source_agent_id
        AND instr(source_agent_id, char(0)) = 0
    ),
    source_identity_kind TEXT NOT NULL CHECK(source_identity_kind IN (
        'provider_session', 'native_subagent', 'managed_agent', 'a2a_remote_agent'
    )),
    source_reservation_id TEXT NOT NULL CHECK(
        length(source_reservation_id) > 0 AND length(source_reservation_id) <= 128
        AND trim(source_reservation_id) = source_reservation_id
        AND instr(source_reservation_id, char(0)) = 0
    ),
    source_reservation_generation INTEGER NOT NULL CHECK(source_reservation_generation > 0),
    source_incarnation_id TEXT NOT NULL CHECK(
        length(source_incarnation_id) > 0 AND length(source_incarnation_id) <= 128
        AND trim(source_incarnation_id) = source_incarnation_id
        AND instr(source_incarnation_id, char(0)) = 0
    ),
    source_incarnation_generation INTEGER NOT NULL CHECK(source_incarnation_generation > 0),
    source_external_identity_ref TEXT NOT NULL CHECK(
        length(source_external_identity_ref) > 0 AND length(source_external_identity_ref) <= 512
        AND trim(source_external_identity_ref) = source_external_identity_ref
        AND instr(source_external_identity_ref, char(0)) = 0
    ),
    source_binding_version INTEGER NOT NULL CHECK(source_binding_version > 0),
    source_binding_digest TEXT NOT NULL CHECK(
        length(source_binding_digest) = 64
        AND source_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    source_fence_ref TEXT NOT NULL CHECK(
        length(source_fence_ref) > 0 AND length(source_fence_ref) <= 512
        AND trim(source_fence_ref) = source_fence_ref
        AND instr(source_fence_ref, char(0)) = 0
    ),
    source_active_process_receipt_ref TEXT NOT NULL CHECK(
        length(source_active_process_receipt_ref) > 0 AND length(source_active_process_receipt_ref) <= 512
        AND trim(source_active_process_receipt_ref) = source_active_process_receipt_ref
        AND instr(source_active_process_receipt_ref, char(0)) = 0
    ),
    source_binding_state TEXT NOT NULL CHECK(source_binding_state = 'active'),
    source_binding_schema_version INTEGER NOT NULL CHECK(source_binding_schema_version = 1),
    source_binding_updated_at TEXT NOT NULL CHECK(
        length(source_binding_updated_at) > 0 AND length(source_binding_updated_at) <= 64
        AND trim(source_binding_updated_at) = source_binding_updated_at
        AND instr(source_binding_updated_at, char(0)) = 0
        AND julianday(source_binding_updated_at) IS NOT NULL
    ),

    target_selector_version INTEGER NOT NULL CHECK(target_selector_version = 1),
    target_selector_kind TEXT NOT NULL CHECK(target_selector_kind = 'active_binding'),
    target_agent_id TEXT NOT NULL CHECK(
        length(target_agent_id) > 0 AND length(target_agent_id) <= 128
        AND trim(target_agent_id) = target_agent_id
        AND instr(target_agent_id, char(0)) = 0
    ),
    target_identity_kind TEXT NOT NULL CHECK(target_identity_kind IN (
        'provider_session', 'native_subagent', 'managed_agent', 'a2a_remote_agent'
    )),
    target_reservation_id TEXT NOT NULL CHECK(
        length(target_reservation_id) > 0 AND length(target_reservation_id) <= 128
        AND trim(target_reservation_id) = target_reservation_id
        AND instr(target_reservation_id, char(0)) = 0
    ),
    target_reservation_generation INTEGER NOT NULL CHECK(target_reservation_generation > 0),
    target_incarnation_id TEXT NOT NULL CHECK(
        length(target_incarnation_id) > 0 AND length(target_incarnation_id) <= 128
        AND trim(target_incarnation_id) = target_incarnation_id
        AND instr(target_incarnation_id, char(0)) = 0
    ),
    target_incarnation_generation INTEGER NOT NULL CHECK(target_incarnation_generation > 0),
    target_external_identity_ref TEXT NOT NULL CHECK(
        length(target_external_identity_ref) > 0 AND length(target_external_identity_ref) <= 512
        AND trim(target_external_identity_ref) = target_external_identity_ref
        AND instr(target_external_identity_ref, char(0)) = 0
    ),
    target_binding_version INTEGER NOT NULL CHECK(target_binding_version > 0),
    target_binding_digest TEXT NOT NULL CHECK(
        length(target_binding_digest) = 64
        AND target_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    target_fence_ref TEXT NOT NULL CHECK(
        length(target_fence_ref) > 0 AND length(target_fence_ref) <= 512
        AND trim(target_fence_ref) = target_fence_ref
        AND instr(target_fence_ref, char(0)) = 0
    ),
    target_active_process_receipt_ref TEXT NOT NULL CHECK(
        length(target_active_process_receipt_ref) > 0 AND length(target_active_process_receipt_ref) <= 512
        AND trim(target_active_process_receipt_ref) = target_active_process_receipt_ref
        AND instr(target_active_process_receipt_ref, char(0)) = 0
    ),
    target_binding_state TEXT NOT NULL CHECK(target_binding_state = 'active'),
    target_binding_schema_version INTEGER NOT NULL CHECK(target_binding_schema_version = 1),
    target_binding_updated_at TEXT NOT NULL CHECK(
        length(target_binding_updated_at) > 0 AND length(target_binding_updated_at) <= 64
        AND trim(target_binding_updated_at) = target_binding_updated_at
        AND instr(target_binding_updated_at, char(0)) = 0
        AND julianday(target_binding_updated_at) IS NOT NULL
    ),

    complete_intent_digest TEXT NOT NULL CHECK(
        length(complete_intent_digest) = 64
        AND complete_intent_digest NOT GLOB '*[^0-9a-f]*'
    ),
    digest_domain TEXT NOT NULL CHECK(digest_domain = 'orch.command.send-message-profile/v1'),
    digest_algorithm TEXT NOT NULL CHECK(digest_algorithm = 'hmac-sha256'),
    digest_key_id TEXT NOT NULL CHECK(
        length(digest_key_id) > 0 AND length(digest_key_id) <= 128
        AND trim(digest_key_id) = digest_key_id
        AND instr(digest_key_id, char(0)) = 0
    ),
    digest_key_version INTEGER NOT NULL CHECK(digest_key_version > 0),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    recorded_at TEXT NOT NULL CHECK(
        length(recorded_at) > 0 AND length(recorded_at) <= 64
        AND trim(recorded_at) = recorded_at
        AND instr(recorded_at, char(0)) = 0
        AND julianday(recorded_at) IS NOT NULL
    ) DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    CHECK(source_agent_id <> target_agent_id),
    CHECK(julianday(grant_issued_at) <= julianday(authorization_requested_at)),
    CHECK(julianday(authorization_requested_at) <= julianday(authorization_decided_at)),
    CHECK(julianday(caller_auth_expires_at) > julianday(authorization_decided_at)),
    CHECK(julianday(authorization_expires_at) > julianday(authorization_decided_at)),
    FOREIGN KEY(source_agent_id, source_binding_version)
        REFERENCES orchestration_current_bindings(agent_id, binding_version) ON DELETE RESTRICT,
    FOREIGN KEY(target_agent_id, target_binding_version)
        REFERENCES orchestration_current_bindings(agent_id, binding_version) ON DELETE RESTRICT
) STRICT;

CREATE INDEX idx_orchestration_command_intent_profiles_grant
    ON orchestration_command_intent_profiles(authorization_grant_id, authorization_grant_revision);

CREATE TRIGGER trg_orchestration_command_intent_profiles_exact_relation
BEFORE INSERT ON orchestration_command_intent_profiles
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
        FROM orchestration_command_intents AS command
        JOIN orchestration_access_grants AS grant
          ON grant.grant_id = NEW.authorization_grant_id
        JOIN orchestration_current_bindings AS source_binding
          ON source_binding.agent_id = NEW.source_agent_id
         AND source_binding.binding_version = NEW.source_binding_version
        JOIN orchestration_current_bindings AS target_binding
          ON target_binding.agent_id = NEW.target_agent_id
         AND target_binding.binding_version = NEW.target_binding_version
        WHERE command.command_id = NEW.command_id
          AND command.command_kind = 'send_message'
          AND command.caller_principal_ref = grant.caller_principal_ref
          AND command.scope_ref = grant.scope_ref
          AND command.source_fence_ref = NEW.source_fence_ref
          AND command.target_fence_ref = NEW.target_fence_ref
          AND command.request_digest = NEW.complete_intent_digest
          AND command.digest_key_id = NEW.digest_key_id
          AND grant.revision = NEW.authorization_grant_revision
          AND grant.status = 'active'
          AND grant.can_send = 1
          AND grant.request_digest = NEW.grant_request_digest
          AND julianday(grant.issued_at) = julianday(NEW.grant_issued_at)
          AND julianday(grant.updated_at) = julianday(NEW.grant_updated_at)
          AND grant.source_agent_id = NEW.source_agent_id
          AND grant.source_reservation_id = NEW.source_reservation_id
          AND grant.source_reservation_generation = NEW.source_reservation_generation
          AND grant.source_incarnation_id = NEW.source_incarnation_id
          AND grant.source_incarnation_generation = NEW.source_incarnation_generation
          AND grant.source_binding_version = NEW.source_binding_version
          AND grant.source_binding_digest = NEW.source_binding_digest
          AND grant.source_fence_ref = NEW.source_fence_ref
          AND grant.target_agent_id = NEW.target_agent_id
          AND grant.target_reservation_id = NEW.target_reservation_id
          AND grant.target_reservation_generation = NEW.target_reservation_generation
          AND grant.target_incarnation_id = NEW.target_incarnation_id
          AND grant.target_incarnation_generation = NEW.target_incarnation_generation
          AND grant.target_binding_version = NEW.target_binding_version
          AND grant.target_binding_digest = NEW.target_binding_digest
          AND grant.target_fence_ref = NEW.target_fence_ref
          AND julianday(grant.expires_at) = julianday(NEW.authorization_expires_at)
          AND source_binding.identity_kind = NEW.source_identity_kind
          AND source_binding.reservation_id = NEW.source_reservation_id
          AND source_binding.reservation_generation = NEW.source_reservation_generation
          AND source_binding.incarnation_id = NEW.source_incarnation_id
          AND source_binding.incarnation_generation = NEW.source_incarnation_generation
          AND source_binding.external_identity_ref = NEW.source_external_identity_ref
          AND source_binding.binding_digest = NEW.source_binding_digest
          AND source_binding.source_fence_ref = NEW.source_fence_ref
          AND source_binding.active_process_receipt_ref = NEW.source_active_process_receipt_ref
          AND source_binding.state = NEW.source_binding_state
          AND source_binding.schema_version = NEW.source_binding_schema_version
          AND julianday(source_binding.updated_at) = julianday(NEW.source_binding_updated_at)
          AND NEW.source_binding_state = 'active'
          AND NOT EXISTS (
              SELECT 1
              FROM orchestration_current_bindings AS newer_source
              WHERE newer_source.agent_id = source_binding.agent_id
                AND newer_source.binding_version > source_binding.binding_version
          )
          AND target_binding.identity_kind = NEW.target_identity_kind
          AND target_binding.reservation_id = NEW.target_reservation_id
          AND target_binding.reservation_generation = NEW.target_reservation_generation
          AND target_binding.incarnation_id = NEW.target_incarnation_id
          AND target_binding.incarnation_generation = NEW.target_incarnation_generation
          AND target_binding.external_identity_ref = NEW.target_external_identity_ref
          AND target_binding.binding_digest = NEW.target_binding_digest
          AND target_binding.source_fence_ref = NEW.target_fence_ref
          AND target_binding.active_process_receipt_ref = NEW.target_active_process_receipt_ref
          AND target_binding.state = NEW.target_binding_state
          AND target_binding.schema_version = NEW.target_binding_schema_version
          AND julianday(target_binding.updated_at) = julianday(NEW.target_binding_updated_at)
          AND NEW.target_binding_state = 'active'
          AND NOT EXISTS (
              SELECT 1
              FROM orchestration_current_bindings AS newer_target
              WHERE newer_target.agent_id = target_binding.agent_id
                AND newer_target.binding_version > target_binding.binding_version
          )
    ) THEN RAISE(ABORT, 'orchestration command intent profile relation mismatch') END;
END;

CREATE TRIGGER trg_orchestration_command_intent_profiles_immutable_update
BEFORE UPDATE ON orchestration_command_intent_profiles
BEGIN
    SELECT RAISE(ABORT, 'orchestration command intent profiles are immutable');
END;

CREATE TRIGGER trg_orchestration_command_intent_profiles_immutable_delete
BEFORE DELETE ON orchestration_command_intent_profiles
BEGIN
    SELECT RAISE(ABORT, 'orchestration command intent profiles are immutable');
END;
