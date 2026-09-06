package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

const authoritySchemaSQL = `
CREATE TABLE authority_schema (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    schema_version INTEGER NOT NULL CHECK (schema_version = 3),
    schema_digest TEXT NOT NULL CHECK (length(schema_digest) = 64 AND instr(schema_digest, char(0)) = 0 AND schema_digest NOT GLOB '*[^0-9a-f]*'),
    created_at TEXT NOT NULL CHECK (length(created_at) BETWEEN 1 AND 64 AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0 AND substr(created_at, -1) = 'Z' AND julianday(created_at) IS NOT NULL)
) STRICT;

CREATE TABLE authority_meta (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    external_service INTEGER NOT NULL CHECK (external_service IN (0, 1)),
    store_id TEXT NOT NULL CHECK (length(store_id) BETWEEN 1 AND 128 AND trim(store_id) = store_id AND instr(store_id, char(0)) = 0),
    state_path_digest TEXT NOT NULL CHECK (length(state_path_digest) = 64 AND instr(state_path_digest, char(0)) = 0 AND state_path_digest NOT GLOB '*[^0-9a-f]*'),
    authority_id TEXT NOT NULL CHECK (length(authority_id) BETWEEN 1 AND 256 AND trim(authority_id) = authority_id AND instr(authority_id, char(0)) = 0),
    authority_cluster_id TEXT NOT NULL CHECK (length(authority_cluster_id) BETWEEN 1 AND 256 AND trim(authority_cluster_id) = authority_cluster_id AND instr(authority_cluster_id, char(0)) = 0),
    authority_generation INTEGER NOT NULL CHECK (authority_generation > 0),
    owner_id TEXT NOT NULL CHECK (length(owner_id) BETWEEN 1 AND 256 AND trim(owner_id) = owner_id AND instr(owner_id, char(0)) = 0),
    owner_generation INTEGER NOT NULL CHECK (owner_generation > 0),
    owner_fence_digest TEXT NOT NULL CHECK (length(owner_fence_digest) = 64 AND instr(owner_fence_digest, char(0)) = 0 AND owner_fence_digest NOT GLOB '*[^0-9a-f]*'),
    authority_profile_generation INTEGER NOT NULL CHECK (authority_profile_generation > 0),
    authority_profile_digest TEXT NOT NULL CHECK (length(authority_profile_digest) = 64 AND instr(authority_profile_digest, char(0)) = 0 AND authority_profile_digest NOT GLOB '*[^0-9a-f]*'),
    retention_policy_digest TEXT NOT NULL CHECK (length(retention_policy_digest) = 64 AND instr(retention_policy_digest, char(0)) = 0 AND retention_policy_digest NOT GLOB '*[^0-9a-f]*'),
    FOREIGN KEY (singleton) REFERENCES authority_schema(singleton)
) STRICT;

CREATE TABLE authority_writer_fence (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    writer_epoch INTEGER NOT NULL CHECK (writer_epoch > 0),
    deployment_fence TEXT NOT NULL CHECK (length(deployment_fence) BETWEEN 1 AND 512
        AND trim(deployment_fence) = deployment_fence
        AND instr(deployment_fence, char(0)) = 0),
    FOREIGN KEY (singleton) REFERENCES authority_schema(singleton)
) STRICT;

CREATE TABLE authority_heads (
    security_domain_id TEXT NOT NULL CHECK (length(security_domain_id) BETWEEN 1 AND 256 AND trim(security_domain_id) = security_domain_id AND instr(security_domain_id, char(0)) = 0),
    authority_profile TEXT NOT NULL CHECK (length(authority_profile) BETWEEN 1 AND 128 AND trim(authority_profile) = authority_profile AND instr(authority_profile, char(0)) = 0),
    head_id TEXT NOT NULL CHECK (length(head_id) BETWEEN 1 AND 256 AND trim(head_id) = head_id AND instr(head_id, char(0)) = 0),
    head_revision INTEGER NOT NULL CHECK (head_revision >= 0),
    head_root_digest TEXT NOT NULL CHECK (length(head_root_digest) = 64 AND instr(head_root_digest, char(0)) = 0 AND head_root_digest NOT GLOB '*[^0-9a-f]*'),
    security_time_lineage_digest TEXT NOT NULL CHECK (length(security_time_lineage_digest) = 64 AND instr(security_time_lineage_digest, char(0)) = 0 AND security_time_lineage_digest NOT GLOB '*[^0-9a-f]*'),
    security_time_generation INTEGER NOT NULL CHECK (security_time_generation > 0),
    security_time_revision INTEGER NOT NULL CHECK (security_time_revision > 0),
    anti_rollback_generation INTEGER NOT NULL CHECK (anti_rollback_generation > 0),
    anti_rollback_anchor_digest TEXT NOT NULL CHECK (length(anti_rollback_anchor_digest) = 64 AND instr(anti_rollback_anchor_digest, char(0)) = 0 AND anti_rollback_anchor_digest NOT GLOB '*[^0-9a-f]*'),
    anti_rollback_attestation_ref TEXT NOT NULL CHECK (length(anti_rollback_attestation_ref) BETWEEN 1 AND 512 AND trim(anti_rollback_attestation_ref) = anti_rollback_attestation_ref AND instr(anti_rollback_attestation_ref, char(0)) = 0),
    active_resolution_id TEXT,
    PRIMARY KEY (security_domain_id, authority_profile),
    UNIQUE (head_id),
    FOREIGN KEY (active_resolution_id) REFERENCES resolutions(resolution_id)
) STRICT;

CREATE TABLE submissions (
    operation_id TEXT PRIMARY KEY NOT NULL CHECK (length(operation_id) BETWEEN 1 AND 128 AND trim(operation_id) = operation_id AND instr(operation_id, char(0)) = 0),
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    security_domain_id TEXT NOT NULL,
    authority_profile TEXT NOT NULL,
    command_id TEXT NOT NULL CHECK (length(command_id) BETWEEN 1 AND 128 AND trim(command_id) = command_id AND instr(command_id, char(0)) = 0),
    command_version INTEGER NOT NULL CHECK (command_version > 0),
    command_profile_digest TEXT NOT NULL CHECK (length(command_profile_digest) = 64 AND instr(command_profile_digest, char(0)) = 0 AND command_profile_digest NOT GLOB '*[^0-9a-f]*'),
    authorization_digest TEXT NOT NULL CHECK (length(authorization_digest) = 64 AND instr(authorization_digest, char(0)) = 0 AND authorization_digest NOT GLOB '*[^0-9a-f]*'),
    identity_fence_digest TEXT NOT NULL CHECK (length(identity_fence_digest) = 64 AND instr(identity_fence_digest, char(0)) = 0 AND identity_fence_digest NOT GLOB '*[^0-9a-f]*'),
    content_snapshot_digest TEXT NOT NULL CHECK (length(content_snapshot_digest) = 64 AND instr(content_snapshot_digest, char(0)) = 0 AND content_snapshot_digest NOT GLOB '*[^0-9a-f]*'),
    resource_snapshot_digest TEXT NOT NULL CHECK (length(resource_snapshot_digest) = 64 AND instr(resource_snapshot_digest, char(0)) = 0 AND resource_snapshot_digest NOT GLOB '*[^0-9a-f]*'),
    batch_digest TEXT NOT NULL CHECK (length(batch_digest) = 64 AND instr(batch_digest, char(0)) = 0 AND batch_digest NOT GLOB '*[^0-9a-f]*'),
    idempotency_key_digest TEXT NOT NULL CHECK (length(idempotency_key_digest) = 64 AND instr(idempotency_key_digest, char(0)) = 0 AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'),
    request_digest TEXT NOT NULL CHECK (length(request_digest) = 64 AND instr(request_digest, char(0)) = 0 AND request_digest NOT GLOB '*[^0-9a-f]*'),
    handle_digest TEXT NOT NULL CHECK (length(handle_digest) = 64 AND instr(handle_digest, char(0)) = 0 AND handle_digest NOT GLOB '*[^0-9a-f]*'),
    request_json TEXT NOT NULL CHECK (json_valid(request_json) AND json_type(request_json) = 'object'),
    handle_json TEXT NOT NULL CHECK (json_valid(handle_json) AND json_type(handle_json) = 'object'),
    status TEXT NOT NULL CHECK (status IN ('pending', 'available', 'aborted')),
    receipt_digest TEXT CHECK (receipt_digest IS NULL OR (length(receipt_digest) = 64 AND instr(receipt_digest, char(0)) = 0 AND receipt_digest NOT GLOB '*[^0-9a-f]*')),
    created_at TEXT NOT NULL CHECK (length(created_at) BETWEEN 1 AND 64 AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0 AND substr(created_at, -1) = 'Z' AND julianday(created_at) IS NOT NULL),
    updated_at TEXT NOT NULL CHECK (length(updated_at) BETWEEN 1 AND 64 AND trim(updated_at) = updated_at AND instr(updated_at, char(0)) = 0 AND substr(updated_at, -1) = 'Z' AND julianday(updated_at) IS NOT NULL),
    UNIQUE (security_domain_id, authority_profile, idempotency_key_digest),
    FOREIGN KEY (security_domain_id, authority_profile) REFERENCES authority_heads(security_domain_id, authority_profile)
) STRICT;

CREATE TABLE resolutions (
    resolution_id TEXT PRIMARY KEY NOT NULL CHECK (length(resolution_id) BETWEEN 1 AND 128 AND trim(resolution_id) = resolution_id AND instr(resolution_id, char(0)) = 0),
    operation_id TEXT NOT NULL UNIQUE,
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    kind TEXT NOT NULL CHECK (kind IN ('availability', 'abort_tombstone')),
    resolution_request_digest TEXT NOT NULL CHECK (length(resolution_request_digest) = 64 AND instr(resolution_request_digest, char(0)) = 0 AND resolution_request_digest NOT GLOB '*[^0-9a-f]*'),
    finalization_operation_id TEXT NOT NULL UNIQUE CHECK (length(finalization_operation_id) BETWEEN 1 AND 128 AND trim(finalization_operation_id) = finalization_operation_id AND instr(finalization_operation_id, char(0)) = 0),
    expected_resolution_digest TEXT NOT NULL CHECK (length(expected_resolution_digest) = 64 AND instr(expected_resolution_digest, char(0)) = 0 AND expected_resolution_digest NOT GLOB '*[^0-9a-f]*'),
    finalization_request_digest TEXT NOT NULL CHECK (length(finalization_request_digest) = 64 AND instr(finalization_request_digest, char(0)) = 0 AND finalization_request_digest NOT GLOB '*[^0-9a-f]*'),
    request_json TEXT NOT NULL CHECK (json_valid(request_json) AND json_type(request_json) = 'object'),
    head_id TEXT NOT NULL,
    expected_head_revision INTEGER NOT NULL CHECK (expected_head_revision >= 0),
    expected_head_root_digest TEXT NOT NULL CHECK (length(expected_head_root_digest) = 64 AND instr(expected_head_root_digest, char(0)) = 0 AND expected_head_root_digest NOT GLOB '*[^0-9a-f]*'),
    expected_anti_rollback_generation INTEGER NOT NULL CHECK (expected_anti_rollback_generation > 0),
    expected_anchor_digest TEXT NOT NULL CHECK (length(expected_anchor_digest) = 64 AND instr(expected_anchor_digest, char(0)) = 0 AND expected_anchor_digest NOT GLOB '*[^0-9a-f]*'),
    security_time_lineage_digest TEXT NOT NULL CHECK (length(security_time_lineage_digest) = 64 AND instr(security_time_lineage_digest, char(0)) = 0 AND security_time_lineage_digest NOT GLOB '*[^0-9a-f]*'),
    expected_security_time_generation INTEGER NOT NULL CHECK (expected_security_time_generation > 0),
    expected_security_time_revision INTEGER NOT NULL CHECK (expected_security_time_revision > 0),
    state TEXT NOT NULL CHECK (state IN ('active_resolution', 'anchored', 'terminal')),
    checkpoint_generation INTEGER,
    checkpoint_anchor_digest TEXT CHECK (checkpoint_anchor_digest IS NULL OR (length(checkpoint_anchor_digest) = 64 AND instr(checkpoint_anchor_digest, char(0)) = 0 AND checkpoint_anchor_digest NOT GLOB '*[^0-9a-f]*')),
    result_head_root_digest TEXT CHECK (result_head_root_digest IS NULL OR (length(result_head_root_digest) = 64 AND instr(result_head_root_digest, char(0)) = 0 AND result_head_root_digest NOT GLOB '*[^0-9a-f]*')),
    finalization_digest TEXT CHECK (finalization_digest IS NULL OR (length(finalization_digest) = 64 AND instr(finalization_digest, char(0)) = 0 AND finalization_digest NOT GLOB '*[^0-9a-f]*')),
    finalized_at TEXT CHECK (finalized_at IS NULL OR (length(finalized_at) BETWEEN 1 AND 64 AND trim(finalized_at) = finalized_at AND instr(finalized_at, char(0)) = 0 AND substr(finalized_at, -1) = 'Z' AND julianday(finalized_at) IS NOT NULL)),
    created_at TEXT NOT NULL CHECK (length(created_at) BETWEEN 1 AND 64 AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0 AND substr(created_at, -1) = 'Z' AND julianday(created_at) IS NOT NULL),
    updated_at TEXT NOT NULL CHECK (length(updated_at) BETWEEN 1 AND 64 AND trim(updated_at) = updated_at AND instr(updated_at, char(0)) = 0 AND substr(updated_at, -1) = 'Z' AND julianday(updated_at) IS NOT NULL),
    FOREIGN KEY (operation_id) REFERENCES submissions(operation_id),
    FOREIGN KEY (head_id) REFERENCES authority_heads(head_id),
    CHECK ((state = 'active_resolution' AND checkpoint_generation IS NULL AND checkpoint_anchor_digest IS NULL AND result_head_root_digest IS NULL AND finalization_digest IS NULL AND finalized_at IS NULL)
        OR (state IN ('anchored', 'terminal') AND checkpoint_generation IS NOT NULL AND checkpoint_anchor_digest IS NOT NULL AND result_head_root_digest IS NOT NULL AND finalization_digest IS NOT NULL AND finalized_at IS NOT NULL))
) STRICT;

CREATE TABLE antirollback_checkpoints (
    security_domain_id TEXT NOT NULL,
    authority_profile TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    predecessor_generation INTEGER NOT NULL CHECK (predecessor_generation > 0 AND generation = predecessor_generation + 1),
    predecessor_anchor_digest TEXT NOT NULL CHECK (length(predecessor_anchor_digest) = 64 AND instr(predecessor_anchor_digest, char(0)) = 0 AND predecessor_anchor_digest NOT GLOB '*[^0-9a-f]*'),
    anchor_digest TEXT NOT NULL CHECK (length(anchor_digest) = 64 AND instr(anchor_digest, char(0)) = 0 AND anchor_digest NOT GLOB '*[^0-9a-f]*'),
    result_head_root_digest TEXT NOT NULL CHECK (length(result_head_root_digest) = 64 AND instr(result_head_root_digest, char(0)) = 0 AND result_head_root_digest NOT GLOB '*[^0-9a-f]*'),
    anchor_ref TEXT NOT NULL CHECK (length(anchor_ref) BETWEEN 1 AND 512 AND trim(anchor_ref) = anchor_ref AND instr(anchor_ref, char(0)) = 0),
    finalization_digest TEXT NOT NULL CHECK (length(finalization_digest) = 64 AND instr(finalization_digest, char(0)) = 0 AND finalization_digest NOT GLOB '*[^0-9a-f]*'),
    finalization_ref TEXT NOT NULL CHECK (length(finalization_ref) BETWEEN 1 AND 512 AND trim(finalization_ref) = finalization_ref AND instr(finalization_ref, char(0)) = 0),
    anchored_at TEXT NOT NULL CHECK (length(anchored_at) BETWEEN 1 AND 64 AND trim(anchored_at) = anchored_at AND instr(anchored_at, char(0)) = 0 AND substr(anchored_at, -1) = 'Z' AND julianday(anchored_at) IS NOT NULL),
    resolution_id TEXT NOT NULL UNIQUE,
    PRIMARY KEY (security_domain_id, authority_profile, generation),
    UNIQUE (security_domain_id, authority_profile, anchor_digest),
    FOREIGN KEY (security_domain_id, authority_profile) REFERENCES authority_heads(security_domain_id, authority_profile),
    FOREIGN KEY (resolution_id) REFERENCES resolutions(resolution_id)
) STRICT;

CREATE TABLE terminal_receipts (
    operation_id TEXT PRIMARY KEY NOT NULL,
    resolution_id TEXT NOT NULL UNIQUE,
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    kind TEXT NOT NULL CHECK (kind IN ('availability', 'abort_tombstone')),
    receipt_id TEXT NOT NULL UNIQUE CHECK (length(receipt_id) BETWEEN 1 AND 128 AND trim(receipt_id) = receipt_id AND instr(receipt_id, char(0)) = 0),
    authority_id TEXT NOT NULL CHECK (length(authority_id) BETWEEN 1 AND 256 AND trim(authority_id) = authority_id AND instr(authority_id, char(0)) = 0),
    authority_cluster_id TEXT NOT NULL CHECK (length(authority_cluster_id) BETWEEN 1 AND 256 AND trim(authority_cluster_id) = authority_cluster_id AND instr(authority_cluster_id, char(0)) = 0),
    authority_profile TEXT NOT NULL CHECK (length(authority_profile) BETWEEN 1 AND 128 AND trim(authority_profile) = authority_profile AND instr(authority_profile, char(0)) = 0),
    authority_generation INTEGER NOT NULL CHECK (authority_generation > 0),
    owner_id TEXT NOT NULL CHECK (length(owner_id) BETWEEN 1 AND 256 AND trim(owner_id) = owner_id AND instr(owner_id, char(0)) = 0),
    owner_generation INTEGER NOT NULL CHECK (owner_generation > 0),
    authority_profile_generation INTEGER NOT NULL CHECK (authority_profile_generation > 0),
    authority_profile_digest TEXT NOT NULL CHECK (length(authority_profile_digest) = 64 AND instr(authority_profile_digest, char(0)) = 0 AND authority_profile_digest NOT GLOB '*[^0-9a-f]*'),
    security_domain_id TEXT NOT NULL CHECK (length(security_domain_id) BETWEEN 1 AND 256 AND trim(security_domain_id) = security_domain_id AND instr(security_domain_id, char(0)) = 0),
    head_id TEXT NOT NULL CHECK (length(head_id) BETWEEN 1 AND 256 AND trim(head_id) = head_id AND instr(head_id, char(0)) = 0),
    head_revision INTEGER NOT NULL CHECK (head_revision > 0),
    head_root_digest TEXT NOT NULL CHECK (length(head_root_digest) = 64 AND instr(head_root_digest, char(0)) = 0 AND head_root_digest NOT GLOB '*[^0-9a-f]*'),
    request_digest TEXT NOT NULL CHECK (length(request_digest) = 64 AND instr(request_digest, char(0)) = 0 AND request_digest NOT GLOB '*[^0-9a-f]*'),
    batch_digest TEXT NOT NULL CHECK (length(batch_digest) = 64 AND instr(batch_digest, char(0)) = 0 AND batch_digest NOT GLOB '*[^0-9a-f]*'),
    security_time_lineage_digest TEXT NOT NULL CHECK (length(security_time_lineage_digest) = 64 AND instr(security_time_lineage_digest, char(0)) = 0 AND security_time_lineage_digest NOT GLOB '*[^0-9a-f]*'),
    security_time_generation INTEGER NOT NULL CHECK (security_time_generation > 0),
    security_time_revision INTEGER NOT NULL CHECK (security_time_revision > 0),
    anti_rollback_generation INTEGER NOT NULL CHECK (anti_rollback_generation > 0),
    anti_rollback_anchor_digest TEXT NOT NULL CHECK (length(anti_rollback_anchor_digest) = 64 AND instr(anti_rollback_anchor_digest, char(0)) = 0 AND anti_rollback_anchor_digest NOT GLOB '*[^0-9a-f]*'),
    quorum_attestation_digest TEXT NOT NULL CHECK (length(quorum_attestation_digest) = 64 AND instr(quorum_attestation_digest, char(0)) = 0 AND quorum_attestation_digest NOT GLOB '*[^0-9a-f]*'),
    quorum_attestation_ref TEXT NOT NULL CHECK (length(quorum_attestation_ref) BETWEEN 1 AND 512 AND trim(quorum_attestation_ref) = quorum_attestation_ref AND instr(quorum_attestation_ref, char(0)) = 0),
    signature_digest TEXT NOT NULL CHECK (length(signature_digest) = 64 AND instr(signature_digest, char(0)) = 0 AND signature_digest NOT GLOB '*[^0-9a-f]*'),
    signature_ref TEXT NOT NULL CHECK (length(signature_ref) BETWEEN 1 AND 512 AND trim(signature_ref) = signature_ref AND instr(signature_ref, char(0)) = 0),
    authority_time TEXT NOT NULL CHECK (length(authority_time) BETWEEN 1 AND 64 AND trim(authority_time) = authority_time AND instr(authority_time, char(0)) = 0 AND substr(authority_time, -1) = 'Z' AND julianday(authority_time) IS NOT NULL),
    retention_policy_digest TEXT NOT NULL CHECK (length(retention_policy_digest) = 64 AND instr(retention_policy_digest, char(0)) = 0 AND retention_policy_digest NOT GLOB '*[^0-9a-f]*'),
    retention_until TEXT NOT NULL CHECK (length(retention_until) BETWEEN 1 AND 64 AND trim(retention_until) = retention_until AND instr(retention_until, char(0)) = 0 AND substr(retention_until, -1) = 'Z' AND julianday(retention_until) IS NOT NULL),
    gc_not_before_anchor_generation INTEGER NOT NULL CHECK (gc_not_before_anchor_generation > 0),
    receipt_digest TEXT NOT NULL UNIQUE CHECK (length(receipt_digest) = 64 AND instr(receipt_digest, char(0)) = 0 AND receipt_digest NOT GLOB '*[^0-9a-f]*'),
    receipt_json TEXT NOT NULL CHECK (json_valid(receipt_json) AND json_type(receipt_json) = 'object'),
    checkpoint_generation INTEGER NOT NULL,
    created_at TEXT NOT NULL CHECK (length(created_at) BETWEEN 1 AND 64 AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0 AND substr(created_at, -1) = 'Z' AND julianday(created_at) IS NOT NULL),
    FOREIGN KEY (operation_id) REFERENCES submissions(operation_id),
    FOREIGN KEY (resolution_id) REFERENCES resolutions(resolution_id),
    FOREIGN KEY (security_domain_id, authority_profile, checkpoint_generation) REFERENCES antirollback_checkpoints(security_domain_id, authority_profile, generation)
) STRICT;

CREATE UNIQUE INDEX resolutions_one_active_per_head ON resolutions(head_id) WHERE state = 'active_resolution';

CREATE TRIGGER authority_schema_no_update BEFORE UPDATE ON authority_schema BEGIN SELECT RAISE(ABORT, 'authority_schema is immutable'); END;
CREATE TRIGGER authority_schema_no_delete BEFORE DELETE ON authority_schema BEGIN SELECT RAISE(ABORT, 'authority_schema is immutable'); END;
CREATE TRIGGER authority_meta_no_update BEFORE UPDATE ON authority_meta BEGIN SELECT RAISE(ABORT, 'authority_meta is immutable'); END;
CREATE TRIGGER authority_meta_no_delete BEFORE DELETE ON authority_meta BEGIN SELECT RAISE(ABORT, 'authority_meta is immutable'); END;
CREATE TRIGGER authority_writer_fence_no_delete BEFORE DELETE ON authority_writer_fence BEGIN SELECT RAISE(ABORT, 'authority writer fence cannot be deleted'); END;
CREATE TRIGGER authority_writer_fence_guard_update BEFORE UPDATE ON authority_writer_fence
WHEN NOT (
    OLD.singleton = NEW.singleton AND OLD.schema_version = NEW.schema_version
    AND ((OLD.writer_epoch = NEW.writer_epoch AND OLD.deployment_fence = NEW.deployment_fence)
      OR NEW.writer_epoch > OLD.writer_epoch)
) BEGIN SELECT RAISE(ABORT, 'invalid authority writer fence transition'); END;
CREATE TRIGGER authority_heads_no_delete BEFORE DELETE ON authority_heads BEGIN SELECT RAISE(ABORT, 'authority_heads cannot be deleted'); END;
CREATE TRIGGER submissions_no_delete BEFORE DELETE ON submissions BEGIN SELECT RAISE(ABORT, 'submissions cannot be deleted'); END;
CREATE TRIGGER resolutions_no_delete BEFORE DELETE ON resolutions BEGIN SELECT RAISE(ABORT, 'resolutions cannot be deleted'); END;
CREATE TRIGGER antirollback_checkpoints_no_update BEFORE UPDATE ON antirollback_checkpoints BEGIN SELECT RAISE(ABORT, 'antirollback_checkpoints are immutable'); END;
CREATE TRIGGER antirollback_checkpoints_no_delete BEFORE DELETE ON antirollback_checkpoints BEGIN SELECT RAISE(ABORT, 'antirollback_checkpoints are immutable'); END;
CREATE TRIGGER terminal_receipts_no_update BEFORE UPDATE ON terminal_receipts BEGIN SELECT RAISE(ABORT, 'terminal_receipts are immutable'); END;
CREATE TRIGGER terminal_receipts_no_delete BEFORE DELETE ON terminal_receipts BEGIN SELECT RAISE(ABORT, 'terminal_receipts are immutable'); END;

CREATE TRIGGER submissions_guard_update BEFORE UPDATE ON submissions
WHEN NOT (
    OLD.status = 'pending' AND NEW.status IN ('available', 'aborted') AND NEW.receipt_digest IS NOT NULL
    AND OLD.operation_id = NEW.operation_id AND OLD.schema_version = NEW.schema_version
    AND OLD.security_domain_id = NEW.security_domain_id AND OLD.authority_profile = NEW.authority_profile
    AND OLD.command_id = NEW.command_id AND OLD.command_version = NEW.command_version
    AND OLD.command_profile_digest = NEW.command_profile_digest AND OLD.authorization_digest = NEW.authorization_digest
    AND OLD.identity_fence_digest = NEW.identity_fence_digest AND OLD.content_snapshot_digest = NEW.content_snapshot_digest
    AND OLD.resource_snapshot_digest = NEW.resource_snapshot_digest AND OLD.batch_digest = NEW.batch_digest
    AND OLD.idempotency_key_digest = NEW.idempotency_key_digest AND OLD.request_digest = NEW.request_digest
    AND OLD.handle_digest = NEW.handle_digest AND OLD.request_json = NEW.request_json AND OLD.handle_json = NEW.handle_json
    AND OLD.created_at = NEW.created_at
    AND EXISTS (SELECT 1 FROM terminal_receipts tr WHERE tr.operation_id = OLD.operation_id AND tr.receipt_digest = NEW.receipt_digest
        AND ((NEW.status = 'available' AND tr.kind = 'availability') OR (NEW.status = 'aborted' AND tr.kind = 'abort_tombstone')))
) BEGIN SELECT RAISE(ABORT, 'invalid submission transition'); END;

CREATE TRIGGER resolutions_guard_update BEFORE UPDATE ON resolutions
WHEN NOT (
    OLD.resolution_id = NEW.resolution_id AND OLD.operation_id = NEW.operation_id AND OLD.schema_version = NEW.schema_version
    AND OLD.kind = NEW.kind AND OLD.resolution_request_digest = NEW.resolution_request_digest
    AND OLD.finalization_operation_id = NEW.finalization_operation_id
    AND OLD.expected_resolution_digest = NEW.expected_resolution_digest
    AND OLD.finalization_request_digest = NEW.finalization_request_digest
    AND OLD.request_json = NEW.request_json AND OLD.head_id = NEW.head_id
    AND OLD.expected_head_revision = NEW.expected_head_revision AND OLD.expected_head_root_digest = NEW.expected_head_root_digest
    AND OLD.expected_anti_rollback_generation = NEW.expected_anti_rollback_generation AND OLD.expected_anchor_digest = NEW.expected_anchor_digest
    AND OLD.security_time_lineage_digest = NEW.security_time_lineage_digest
    AND OLD.expected_security_time_generation = NEW.expected_security_time_generation
    AND OLD.expected_security_time_revision = NEW.expected_security_time_revision
    AND OLD.created_at = NEW.created_at
    AND ((OLD.state = 'active_resolution' AND NEW.state = 'anchored')
      OR (OLD.state = 'anchored' AND NEW.state = 'terminal'
        AND OLD.checkpoint_generation = NEW.checkpoint_generation
        AND OLD.checkpoint_anchor_digest = NEW.checkpoint_anchor_digest
        AND OLD.result_head_root_digest = NEW.result_head_root_digest
        AND OLD.finalization_digest = NEW.finalization_digest
        AND OLD.finalized_at = NEW.finalized_at))
) BEGIN SELECT RAISE(ABORT, 'invalid resolution transition'); END;

CREATE TRIGGER terminal_receipts_require_anchor BEFORE INSERT ON terminal_receipts
WHEN NOT EXISTS (
    SELECT 1 FROM resolutions r
    JOIN submissions s ON s.operation_id = r.operation_id
    JOIN antirollback_checkpoints c ON c.resolution_id = r.resolution_id
    JOIN authority_heads h ON h.security_domain_id = NEW.security_domain_id AND h.authority_profile = NEW.authority_profile
    JOIN authority_meta m ON m.singleton = 1
    WHERE r.resolution_id = NEW.resolution_id AND r.operation_id = NEW.operation_id AND r.head_id = NEW.head_id
      AND r.kind = NEW.kind AND r.state = 'anchored' AND NEW.head_revision = r.expected_head_revision + 1
      AND s.request_digest = NEW.request_digest AND s.batch_digest = NEW.batch_digest
      AND s.security_domain_id = NEW.security_domain_id AND s.authority_profile = NEW.authority_profile
      AND r.security_time_lineage_digest = NEW.security_time_lineage_digest
      AND h.head_id = NEW.head_id AND h.security_time_lineage_digest = NEW.security_time_lineage_digest
      AND (NEW.security_time_generation > r.expected_security_time_generation
        OR (NEW.security_time_generation = r.expected_security_time_generation
          AND NEW.security_time_revision > r.expected_security_time_revision))
      AND (NEW.security_time_generation > h.security_time_generation
        OR (NEW.security_time_generation = h.security_time_generation
          AND NEW.security_time_revision > h.security_time_revision))
      AND NEW.authority_id = m.authority_id AND NEW.authority_cluster_id = m.authority_cluster_id
      AND NEW.authority_generation = m.authority_generation AND NEW.owner_id = m.owner_id
      AND NEW.owner_generation = m.owner_generation
      AND NEW.authority_profile_generation = m.authority_profile_generation
      AND NEW.authority_profile_digest = m.authority_profile_digest
      AND NEW.retention_policy_digest = m.retention_policy_digest
      AND r.checkpoint_generation = NEW.checkpoint_generation AND r.checkpoint_generation = NEW.anti_rollback_generation
      AND c.generation = NEW.checkpoint_generation AND c.generation = NEW.anti_rollback_generation
      AND c.security_domain_id = NEW.security_domain_id AND c.authority_profile = NEW.authority_profile
      AND r.checkpoint_anchor_digest = NEW.anti_rollback_anchor_digest AND c.anchor_digest = NEW.anti_rollback_anchor_digest
      AND r.result_head_root_digest = NEW.head_root_digest AND c.result_head_root_digest = NEW.head_root_digest
      AND r.finalization_digest = r.finalization_request_digest
      AND r.finalization_request_digest = c.finalization_digest
) BEGIN SELECT RAISE(ABORT, 'terminal receipt requires anchored resolution'); END;

CREATE TRIGGER authority_heads_guard_update BEFORE UPDATE ON authority_heads
WHEN NOT (
    OLD.security_domain_id = NEW.security_domain_id AND OLD.authority_profile = NEW.authority_profile AND OLD.head_id = NEW.head_id
    AND OLD.security_time_lineage_digest = NEW.security_time_lineage_digest
    AND (
      (OLD.active_resolution_id IS NULL AND NEW.active_resolution_id IS NOT NULL
       AND OLD.head_revision = NEW.head_revision AND OLD.head_root_digest = NEW.head_root_digest
       AND OLD.security_time_generation = NEW.security_time_generation AND OLD.security_time_revision = NEW.security_time_revision
       AND OLD.anti_rollback_generation = NEW.anti_rollback_generation
       AND OLD.anti_rollback_anchor_digest = NEW.anti_rollback_anchor_digest
       AND OLD.anti_rollback_attestation_ref = NEW.anti_rollback_attestation_ref
       AND EXISTS (SELECT 1 FROM resolutions r WHERE r.resolution_id = NEW.active_resolution_id AND r.state = 'active_resolution'))
      OR
      (OLD.active_resolution_id IS NOT NULL AND NEW.active_resolution_id IS NULL
       AND NEW.head_revision = OLD.head_revision + 1
       AND (NEW.security_time_generation > OLD.security_time_generation
         OR (NEW.security_time_generation = OLD.security_time_generation
           AND NEW.security_time_revision > OLD.security_time_revision))
       AND EXISTS (SELECT 1 FROM terminal_receipts tr
         JOIN submissions s ON s.operation_id = tr.operation_id
         JOIN resolutions r ON r.resolution_id = tr.resolution_id AND r.operation_id = tr.operation_id
         JOIN antirollback_checkpoints c ON c.resolution_id = tr.resolution_id
         JOIN authority_meta m ON m.singleton = 1
         WHERE tr.resolution_id = OLD.active_resolution_id
         AND tr.security_domain_id = NEW.security_domain_id AND tr.authority_profile = NEW.authority_profile
         AND tr.head_id = NEW.head_id AND tr.head_revision = NEW.head_revision AND tr.head_root_digest = NEW.head_root_digest
         AND tr.request_digest = s.request_digest AND tr.batch_digest = s.batch_digest
         AND tr.security_time_lineage_digest = r.security_time_lineage_digest
         AND tr.security_time_lineage_digest = NEW.security_time_lineage_digest
         AND tr.security_time_generation = NEW.security_time_generation AND tr.security_time_revision = NEW.security_time_revision
         AND tr.authority_id = m.authority_id AND tr.authority_cluster_id = m.authority_cluster_id
         AND tr.authority_generation = m.authority_generation AND tr.owner_id = m.owner_id
         AND tr.owner_generation = m.owner_generation
         AND tr.authority_profile_generation = m.authority_profile_generation
         AND tr.authority_profile_digest = m.authority_profile_digest
         AND tr.retention_policy_digest = m.retention_policy_digest
         AND tr.anti_rollback_generation = NEW.anti_rollback_generation
         AND tr.anti_rollback_anchor_digest = NEW.anti_rollback_anchor_digest
         AND c.security_domain_id = NEW.security_domain_id AND c.authority_profile = NEW.authority_profile
         AND c.generation = tr.checkpoint_generation AND c.generation = NEW.anti_rollback_generation
         AND c.anchor_digest = tr.anti_rollback_anchor_digest AND c.anchor_digest = NEW.anti_rollback_anchor_digest
         AND c.result_head_root_digest = tr.head_root_digest AND c.result_head_root_digest = NEW.head_root_digest
         AND r.finalization_digest = r.finalization_request_digest
         AND r.finalization_request_digest = c.finalization_digest
         AND c.anchor_ref = NEW.anti_rollback_attestation_ref))
    )
) BEGIN SELECT RAISE(ABORT, 'invalid authority head transition'); END;
`

type schemaManifestQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func boolToSQLite(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

type authoritySchemaObject struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	TableName string `json:"table_name"`
	SQL       string `json:"sql"`
}

type authoritySchemaFingerprint struct {
	DDL     string                  `json:"ddl"`
	Objects []authoritySchemaObject `json:"objects"`
}

// requiredAuthorityColumns 返回 authority schema 中每张表的精确有序列集合。
func requiredAuthorityColumns() map[string][]string {
	return map[string][]string{
		"authority_schema":         {"singleton", "schema_version", "schema_digest", "created_at"},
		"authority_writer_fence":   {"singleton", "schema_version", "writer_epoch", "deployment_fence"},
		"authority_meta":           {"singleton", "external_service", "store_id", "state_path_digest", "authority_id", "authority_cluster_id", "authority_generation", "owner_id", "owner_generation", "owner_fence_digest", "authority_profile_generation", "authority_profile_digest", "retention_policy_digest"},
		"authority_heads":          {"security_domain_id", "authority_profile", "head_id", "head_revision", "head_root_digest", "security_time_lineage_digest", "security_time_generation", "security_time_revision", "anti_rollback_generation", "anti_rollback_anchor_digest", "anti_rollback_attestation_ref", "active_resolution_id"},
		"submissions":              {"operation_id", "schema_version", "security_domain_id", "authority_profile", "command_id", "command_version", "command_profile_digest", "authorization_digest", "identity_fence_digest", "content_snapshot_digest", "resource_snapshot_digest", "batch_digest", "idempotency_key_digest", "request_digest", "handle_digest", "request_json", "handle_json", "status", "receipt_digest", "created_at", "updated_at"},
		"resolutions":              {"resolution_id", "operation_id", "schema_version", "kind", "resolution_request_digest", "finalization_operation_id", "expected_resolution_digest", "finalization_request_digest", "request_json", "head_id", "expected_head_revision", "expected_head_root_digest", "expected_anti_rollback_generation", "expected_anchor_digest", "security_time_lineage_digest", "expected_security_time_generation", "expected_security_time_revision", "state", "checkpoint_generation", "checkpoint_anchor_digest", "result_head_root_digest", "finalization_digest", "finalized_at", "created_at", "updated_at"},
		"antirollback_checkpoints": {"security_domain_id", "authority_profile", "generation", "predecessor_generation", "predecessor_anchor_digest", "anchor_digest", "result_head_root_digest", "anchor_ref", "finalization_digest", "finalization_ref", "anchored_at", "resolution_id"},
		"terminal_receipts":        {"operation_id", "resolution_id", "schema_version", "kind", "receipt_id", "authority_id", "authority_cluster_id", "authority_profile", "authority_generation", "owner_id", "owner_generation", "authority_profile_generation", "authority_profile_digest", "security_domain_id", "head_id", "head_revision", "head_root_digest", "request_digest", "batch_digest", "security_time_lineage_digest", "security_time_generation", "security_time_revision", "anti_rollback_generation", "anti_rollback_anchor_digest", "quorum_attestation_digest", "quorum_attestation_ref", "signature_digest", "signature_ref", "authority_time", "retention_policy_digest", "retention_until", "gc_not_before_anchor_generation", "receipt_digest", "receipt_json", "checkpoint_generation", "created_at"},
	}
}

// requiredAuthorityObjects 返回 authority schema 必须包含的索引和触发器类型。
func requiredAuthorityObjects() map[string]string {
	return map[string]string{
		"resolutions_one_active_per_head":     "index",
		"authority_schema_no_update":          "trigger",
		"authority_schema_no_delete":          "trigger",
		"authority_meta_no_update":            "trigger",
		"authority_meta_no_delete":            "trigger",
		"authority_writer_fence_no_delete":    "trigger",
		"authority_writer_fence_guard_update": "trigger",
		"authority_heads_no_delete":           "trigger",
		"submissions_no_delete":               "trigger",
		"resolutions_no_delete":               "trigger",
		"antirollback_checkpoints_no_update":  "trigger",
		"antirollback_checkpoints_no_delete":  "trigger",
		"terminal_receipts_no_update":         "trigger",
		"terminal_receipts_no_delete":         "trigger",
		"submissions_guard_update":            "trigger",
		"resolutions_guard_update":            "trigger",
		"terminal_receipts_require_anchor":    "trigger",
		"authority_heads_guard_update":        "trigger",
	}
}

// Bootstrap 在有效唯一租约下只初始化全新独立 authority；它不兼容或迁移未知 schema。
func Bootstrap(ctx context.Context, config Config, lease AuthorityLease) error {
	config, scope, err := validateAuthorityInputs(ctx, config, lease)
	if err != nil {
		return err
	}
	return withValidatedAuthorityScope(ctx, lease, scope, config.Path, func() (resultErr error) {
		db, err := platformsqlite.Open(ctx, platformsqlite.OpenOptions{Path: config.Path})
		if err != nil {
			return safeAuthorityError("open authority for bootstrap", err)
		}
		openedIdentity, identityErr := captureAuthorityPathIdentity(config.Path)
		if identityErr != nil || !openedIdentity.main.present {
			_ = db.Close()
			if identityErr != nil {
				return identityErr
			}
			return ErrLeaseRejected
		}
		defer func() {
			if closeErr := db.Close(); closeErr != nil {
				resultErr = errors.Join(resultErr, safeAuthorityError("close authority bootstrap database", closeErr))
			}
		}()
		bootstrapErr := bootstrapDatabase(ctx, db, config, scope)
		if identityErr := validateAuthorityPathIdentity(config.Path, openedIdentity, true); identityErr != nil {
			return errors.Join(bootstrapErr, identityErr, ErrCommitOutcomeUnknown)
		}
		return bootstrapErr
	})
}

// bootstrapDatabase 在立即事务内拒绝非空数据库并安装、播种固定 schema。
func bootstrapDatabase(ctx context.Context, db *sql.DB, config Config, scope AuthorityScope) error {
	return withRawImmediate(ctx, db, func(conn *sql.Conn) error {
		empty, err := databaseIsEmpty(ctx, conn)
		if err != nil {
			return err
		}
		if !empty {
			return fmt.Errorf("%w: bootstrap requires an empty database", ErrSchemaMismatch)
		}
		if _, err := conn.ExecContext(ctx, authoritySchemaSQL); err != nil {
			return fmt.Errorf("install authority schema: %w", err)
		}
		return seedAuthority(ctx, conn, config, scope)
	})
}

// seedAuthority 写入固定 schema 身份、authority 元数据和初始 head。
func seedAuthority(ctx context.Context, conn *sql.Conn, config Config, scope AuthorityScope) error {
	now := canonicalTime(time.Now().UTC())
	digest, err := schemaDigest(ctx, conn)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, insertAuthoritySchemaSQL, AuthorityDatabaseSchemaRevision, digest, now); err != nil {
		return fmt.Errorf("seed authority schema: %w", err)
	}
	if err := seedWriterFence(ctx, conn, config.WriterFence()); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, insertAuthorityMetaSQL, boolToSQLite(config.ExternalService), config.StoreID, scope.StatePathDigest, config.AuthorityID,
		config.AuthorityClusterID, config.AuthorityGeneration, config.OwnerID, config.OwnerGeneration, config.OwnerFenceDigest,
		config.AuthorityProfileGeneration, config.AuthorityProfileDigest, config.RetentionPolicyDigest); err != nil {
		return fmt.Errorf("seed authority metadata: %w", err)
	}
	if _, err := conn.ExecContext(ctx, insertAuthorityHeadSQL, config.SecurityDomainID, config.AuthorityProfile, config.HeadID,
		config.InitialHeadRootDigest, config.SecurityTimeLineageDigest, config.SecurityTimeGeneration, config.SecurityTimeRevision,
		config.AntiRollbackGeneration, config.AntiRollbackAnchorDigest, config.AntiRollbackAttestationRef); err != nil {
		return fmt.Errorf("seed authority head: %w", err)
	}
	return nil
}

// verifyAuthoritySchema 精确校验 schema 结构、摘要、版本、身份和当前 writer fence。
func verifyAuthoritySchema(ctx context.Context, db *sql.DB, config Config, scope AuthorityScope) (WriterFence, error) {
	return verifyAuthoritySchemaWithFence(ctx, db, config, scope, true)
}

// verifyAuthoritySchemaForTakeover 校验除 expected writer fence 外的全部
// authority 身份，让 takeover 的 CAS 能将旧 token 失配分类为 ErrStaleWriter。
func verifyAuthoritySchemaForTakeover(ctx context.Context, db *sql.DB, config Config, scope AuthorityScope) (WriterFence, error) {
	return verifyAuthoritySchemaWithFence(ctx, db, config, scope, false)
}

// verifyAuthoritySchemaWithFence 执行共享 schema/identity 校验，并按调用场景
// 决定是否要求当前 fence 已等于配置 token。
func verifyAuthoritySchemaWithFence(ctx context.Context, db *sql.DB, config Config, scope AuthorityScope, requireFence bool) (WriterFence, error) {
	if err := verifyTablesAndColumns(ctx, db); err != nil {
		return WriterFence{}, err
	}
	digest, err := schemaDigest(ctx, db)
	if err != nil {
		return WriterFence{}, err
	}
	var version int64
	var gotDigest string
	if err := db.QueryRowContext(ctx, "SELECT schema_version, schema_digest FROM authority_schema WHERE singleton = 1").Scan(&version, &gotDigest); err != nil {
		return WriterFence{}, schemaMismatch("read authority schema", err)
	}
	if version != AuthorityDatabaseSchemaRevision || gotDigest != digest {
		return WriterFence{}, fmt.Errorf("%w: schema identity", ErrSchemaMismatch)
	}
	if err := verifyAuthorityDatabaseIntegrity(ctx, db); err != nil {
		return WriterFence{}, err
	}
	if err := verifyAuthorityIdentity(ctx, db, config, scope); err != nil {
		return WriterFence{}, err
	}
	var fence WriterFence
	if requireFence {
		fence, err = verifyAuthorityWriterFence(ctx, db, config.WriterFence())
	} else {
		fence, err = readWriterFence(ctx, db)
	}
	if err != nil {
		return WriterFence{}, err
	}
	return fence, nil
}

// verifyAuthorityDatabaseIntegrity 在身份读取前校验 SQLite 物理页和外键引用完整性。
func verifyAuthorityDatabaseIntegrity(ctx context.Context, db *sql.DB) error {
	if err := verifyAuthorityQuickCheck(ctx, db); err != nil {
		return err
	}
	return verifyAuthorityForeignKeys(ctx, db)
}

// verifyAuthorityQuickCheck 要求 SQLite quick_check 精确且仅返回一行 ok。
func verifyAuthorityQuickCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check(1)")
	if err != nil {
		return schemaMismatch("run authority quick check", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return authorityCheckRowsError(rows, "authority quick check returned no result")
	}
	var result string
	if err := rows.Scan(&result); err != nil {
		return schemaMismatch("scan authority quick check", err)
	}
	if result != "ok" || rows.Next() {
		return fmt.Errorf("%w: authority quick check", ErrSchemaMismatch)
	}
	return authorityCheckRowsError(rows, "")
}

// verifyAuthorityForeignKeys 要求 SQLite foreign_key_check 不返回任何违规行。
func verifyAuthorityForeignKeys(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return schemaMismatch("run authority foreign key check", err)
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("%w: authority foreign key check", ErrSchemaMismatch)
	}
	return authorityCheckRowsError(rows, "")
}

// authorityCheckRowsError 将完整性 PRAGMA 的迭代失败归一为 schema 不匹配。
func authorityCheckRowsError(rows *sql.Rows, emptyResult string) error {
	if err := rows.Err(); err != nil {
		return schemaMismatch("iterate authority integrity check", err)
	}
	if emptyResult != "" {
		return fmt.Errorf("%w: %s", ErrSchemaMismatch, emptyResult)
	}
	return nil
}

// verifyTablesAndColumns 精确校验表集合、各表列顺序以及索引和触发器集合。
func verifyTablesAndColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return schemaMismatch("list authority tables", err)
	}
	defer rows.Close()
	var gotTables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return schemaMismatch("scan authority table", err)
		}
		gotTables = append(gotTables, name)
	}
	if err := rows.Err(); err != nil {
		return schemaMismatch("iterate authority tables", err)
	}
	required := requiredAuthorityColumns()
	wantTables := make([]string, 0, len(required))
	for table := range required {
		wantTables = append(wantTables, table)
	}
	sort.Strings(wantTables)
	if strings.Join(gotTables, "\x00") != strings.Join(wantTables, "\x00") {
		return fmt.Errorf("%w: table set", ErrSchemaMismatch)
	}
	for _, table := range wantTables {
		if err := verifyTableColumns(ctx, db, table, required[table]); err != nil {
			return err
		}
	}
	return verifyAuthorityObjects(ctx, db)
}

// verifyAuthorityObjects 精确校验 authority schema 的索引和触发器名称与类型。
func verifyAuthorityObjects(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT name, type FROM sqlite_master
WHERE type IN ('index', 'trigger') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return schemaMismatch("list authority schema objects", err)
	}
	defer rows.Close()
	got := make(map[string]string)
	for rows.Next() {
		var name, objectType string
		if err := rows.Scan(&name, &objectType); err != nil {
			return schemaMismatch("scan authority schema object", err)
		}
		got[name] = objectType
	}
	if err := rows.Err(); err != nil {
		return schemaMismatch("iterate authority schema objects", err)
	}
	want := requiredAuthorityObjects()
	if len(got) != len(want) {
		return fmt.Errorf("%w: schema object set", ErrSchemaMismatch)
	}
	for name, objectType := range want {
		if got[name] != objectType {
			return fmt.Errorf("%w: schema object identity", ErrSchemaMismatch)
		}
	}
	return nil
}

// verifyTableColumns 按 SQLite 声明顺序精确校验指定表的列集合。
func verifyTableColumns(ctx context.Context, db *sql.DB, table string, want []string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return schemaMismatch("read authority columns", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return schemaMismatch("scan authority columns", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		return schemaMismatch("iterate authority columns", err)
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		return fmt.Errorf("%w: column set for %s", ErrSchemaMismatch, table)
	}
	return nil
}

// verifyAuthorityIdentity 校验不可变元数据与当前 authority head 的身份和防回滚边界。
func verifyAuthorityIdentity(ctx context.Context, db *sql.DB, config Config, scope AuthorityScope) error {
	if err := verifyAuthorityMetadata(ctx, db, config, scope); err != nil {
		return err
	}
	return verifyAuthorityHead(ctx, db, config)
}

// verifyAuthorityMetadata 精确校验数据库中的不可变 authority 元数据。
func verifyAuthorityMetadata(ctx context.Context, db *sql.DB, config Config, scope AuthorityScope) error {
	var values [12]any
	for index := range values {
		values[index] = new(any)
	}
	row := db.QueryRowContext(ctx, selectAuthorityIdentitySQL)
	if err := row.Scan(values[:]...); err != nil {
		return schemaMismatch("read authority identity", err)
	}
	want := []any{boolToSQLite(config.ExternalService), config.StoreID, scope.StatePathDigest, config.AuthorityID, config.AuthorityClusterID,
		config.AuthorityGeneration, config.OwnerID, config.OwnerGeneration, config.OwnerFenceDigest,
		config.AuthorityProfileGeneration, config.AuthorityProfileDigest, config.RetentionPolicyDigest}
	for index, pointer := range values {
		if fmt.Sprint(*(pointer.(*any))) != fmt.Sprint(want[index]) {
			return fmt.Errorf("%w: authority metadata", ErrSchemaMismatch)
		}
	}
	return nil
}

// verifyAuthorityHead 校验当前 head 身份以及安全时间和防回滚下界。
func verifyAuthorityHead(ctx context.Context, db *sql.DB, config Config) error {
	var headID, root, lineage, anchor, attestation string
	var revision, timeGeneration, timeRevision, rollbackGeneration int64
	var active sql.NullString
	err := db.QueryRowContext(ctx, selectAuthorityHeadSQL, config.SecurityDomainID, config.AuthorityProfile).Scan(
		&headID, &revision, &root, &lineage, &timeGeneration, &timeRevision, &rollbackGeneration, &anchor, &attestation, &active,
	)
	if err != nil {
		return schemaMismatch("read authority head", err)
	}
	if !authorityHeadWithinScope(config, headID, lineage, timeGeneration, timeRevision, rollbackGeneration) {
		return fmt.Errorf("%w: authority head identity or rollback", ErrSchemaMismatch)
	}
	if revision == 0 && !initialAuthorityHeadMatches(config, root, anchor, attestation) {
		return fmt.Errorf("%w: initial authority head", ErrSchemaMismatch)
	}
	return nil
}

// authorityHeadWithinScope 判断当前 head 身份和单调计数是否满足配置下界。
func authorityHeadWithinScope(config Config, headID, lineage string, timeGeneration, timeRevision, rollbackGeneration int64) bool {
	return headID == config.HeadID && lineage == config.SecurityTimeLineageDigest &&
		securityTimeAtOrAfter(timeGeneration, timeRevision, config.SecurityTimeGeneration, config.SecurityTimeRevision) &&
		rollbackGeneration >= config.AntiRollbackGeneration
}

// securityTimeAtOrAfter 按 generation、revision 字典序判断安全时间是否达到配置下界。
func securityTimeAtOrAfter(generation, revision, minimumGeneration, minimumRevision int64) bool {
	return generation > minimumGeneration || generation == minimumGeneration && revision >= minimumRevision
}

// initialAuthorityHeadMatches 判断零修订 head 是否仍与初始化锚点完全一致。
func initialAuthorityHeadMatches(config Config, root, anchor, attestation string) bool {
	return root == config.InitialHeadRootDigest && anchor == config.AntiRollbackAnchorDigest &&
		attestation == config.AntiRollbackAttestationRef
}

// databaseIsEmpty 判断数据库是否完全不存在非 SQLite 内部对象。
func databaseIsEmpty(ctx context.Context, conn *sql.Conn) (bool, error) {
	var count int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
		return false, fmt.Errorf("inspect bootstrap database: %w", err)
	}
	return count == 0, nil
}

// schemaDigest 联合 revision-3 固定 DDL 与数据库实际 sqlite_master 清单计算版本化指纹。
func schemaDigest(ctx context.Context, queryer schemaManifestQueryer) (string, error) {
	objects, err := loadAuthoritySchemaManifest(ctx, queryer)
	if err != nil {
		return "", err
	}
	fingerprint := authoritySchemaFingerprint{DDL: authoritySchemaSQL, Objects: objects}
	return digestValue(AuthorityDatabaseSchemaDigestDomain, fingerprint)
}

// loadAuthoritySchemaManifest 读取按身份稳定排序的实际 schema 定义清单。
func loadAuthoritySchemaManifest(ctx context.Context, queryer schemaManifestQueryer) ([]authoritySchemaObject, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT name, type, tbl_name, sql FROM sqlite_master
WHERE name NOT LIKE 'sqlite_%' ORDER BY name, type, tbl_name`)
	if err != nil {
		return nil, schemaMismatch("list authority schema definitions", err)
	}
	defer rows.Close()
	objects := make([]authoritySchemaObject, 0)
	for rows.Next() {
		var object authoritySchemaObject
		if err := rows.Scan(&object.Name, &object.Type, &object.TableName, &object.SQL); err != nil {
			return nil, schemaMismatch("scan authority schema definition", err)
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, schemaMismatch("iterate authority schema definitions", err)
	}
	return objects, nil
}

// schemaMismatch 将底层查询失败归一为不泄漏细节的 schema 不匹配错误。
func schemaMismatch(action string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrSchemaMismatch, action)
	}
	return fmt.Errorf("%w: %s failed", ErrSchemaMismatch, action)
}
