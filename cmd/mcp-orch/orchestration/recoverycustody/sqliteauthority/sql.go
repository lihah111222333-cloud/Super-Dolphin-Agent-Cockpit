package sqliteauthority

const (
	insertAuthoritySchemaSQL = `INSERT INTO authority_schema(singleton, schema_version, schema_digest, created_at) VALUES(1, ?, ?, ?)`
	insertAuthorityMetaSQL   = `INSERT INTO authority_meta(singleton, external_service, store_id, state_path_digest, authority_id, authority_cluster_id,
authority_generation, owner_id, owner_generation, owner_fence_digest, authority_profile_generation, authority_profile_digest,
retention_policy_digest) VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	insertAuthorityHeadSQL = `INSERT INTO authority_heads(security_domain_id, authority_profile, head_id, head_revision,
head_root_digest, security_time_lineage_digest, security_time_generation, security_time_revision, anti_rollback_generation,
anti_rollback_anchor_digest, anti_rollback_attestation_ref, active_resolution_id) VALUES(?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, NULL)`
	selectAuthorityIdentitySQL = `SELECT external_service, store_id, state_path_digest, authority_id, authority_cluster_id, authority_generation,
owner_id, owner_generation, owner_fence_digest, authority_profile_generation, authority_profile_digest, retention_policy_digest
FROM authority_meta WHERE singleton = 1`
	selectAuthorityHeadSQL = `SELECT head_id, head_revision, head_root_digest, security_time_lineage_digest,
security_time_generation, security_time_revision, anti_rollback_generation, anti_rollback_anchor_digest,
anti_rollback_attestation_ref, active_resolution_id FROM authority_heads WHERE security_domain_id = ? AND authority_profile = ?`

	insertSubmissionSQL = `INSERT INTO submissions(operation_id, schema_version, security_domain_id, authority_profile,
command_id, command_version, command_profile_digest, authorization_digest, identity_fence_digest, content_snapshot_digest,
resource_snapshot_digest, batch_digest, idempotency_key_digest, request_digest, handle_digest, request_json, handle_json,
	status, receipt_digest, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', NULL, ?, ?)
ON CONFLICT (security_domain_id, authority_profile, idempotency_key_digest) DO NOTHING`
	selectSubmissionSQL = `SELECT schema_version, operation_id, security_domain_id, authority_profile, command_id,
command_version, command_profile_digest, authorization_digest, identity_fence_digest, content_snapshot_digest,
resource_snapshot_digest, batch_digest, idempotency_key_digest, request_digest, handle_digest, request_json, handle_json,
status, receipt_digest FROM submissions WHERE operation_id = ?`
	selectTerminalReceiptSQL = `SELECT tr.operation_id, tr.resolution_id, tr.schema_version, tr.kind, tr.receipt_id,
tr.authority_id, tr.authority_cluster_id, tr.authority_profile, tr.authority_generation, tr.owner_id, tr.owner_generation,
tr.authority_profile_generation, tr.authority_profile_digest, tr.security_domain_id, tr.head_id, tr.head_revision,
tr.head_root_digest, tr.request_digest, tr.batch_digest, tr.security_time_lineage_digest, tr.security_time_generation,
tr.security_time_revision, tr.anti_rollback_generation, tr.anti_rollback_anchor_digest,
tr.quorum_attestation_digest, tr.quorum_attestation_ref, tr.signature_digest, tr.signature_ref, tr.authority_time,
tr.retention_policy_digest, tr.retention_until, tr.gc_not_before_anchor_generation, tr.receipt_digest, tr.receipt_json,
tr.checkpoint_generation,
s.status, s.receipt_digest,
r.schema_version, r.resolution_id, r.operation_id, s.schema_version, s.operation_id, s.security_domain_id,
s.authority_profile, s.command_id, s.command_version, s.request_digest, s.batch_digest, s.handle_digest, r.kind,
r.resolution_request_digest, r.finalization_operation_id, r.expected_resolution_digest, r.finalization_request_digest,
r.request_json, r.head_id,
r.expected_head_revision, r.expected_head_root_digest, r.expected_anti_rollback_generation, r.expected_anchor_digest,
r.security_time_lineage_digest, r.expected_security_time_generation, r.expected_security_time_revision, r.state,
r.checkpoint_generation, r.checkpoint_anchor_digest, r.result_head_root_digest, r.finalization_digest,
c.generation, c.predecessor_generation, c.predecessor_anchor_digest, c.anchor_digest, c.result_head_root_digest,
c.finalization_digest
FROM terminal_receipts tr
JOIN submissions s ON s.operation_id = tr.operation_id
JOIN resolutions r ON r.operation_id = tr.operation_id AND r.resolution_id = tr.resolution_id
JOIN antirollback_checkpoints c ON c.resolution_id = tr.resolution_id
 AND c.security_domain_id = tr.security_domain_id AND c.authority_profile = tr.authority_profile
 AND c.generation = tr.checkpoint_generation
WHERE tr.operation_id = ?`

	selectResolutionSQL = `SELECT r.schema_version, r.resolution_id, r.operation_id, s.schema_version, s.operation_id,
s.security_domain_id, s.authority_profile, s.command_id, s.command_version, s.request_digest, s.batch_digest,
s.handle_digest, r.kind, r.resolution_request_digest, r.finalization_operation_id, r.expected_resolution_digest,
r.finalization_request_digest, r.request_json, r.head_id, r.expected_head_revision, r.expected_head_root_digest, r.expected_anti_rollback_generation,
r.expected_anchor_digest, r.security_time_lineage_digest, r.expected_security_time_generation,
r.expected_security_time_revision, r.state
FROM resolutions r JOIN submissions s ON s.operation_id = r.operation_id WHERE r.operation_id = ?`
	selectActiveResolutionsSQL = `SELECT r.schema_version, r.resolution_id, r.operation_id, s.schema_version, s.operation_id,
s.security_domain_id, s.authority_profile, s.command_id, s.command_version, s.request_digest, s.batch_digest,
s.handle_digest, r.kind, r.resolution_request_digest, r.finalization_operation_id, r.expected_resolution_digest,
r.finalization_request_digest, r.request_json, r.head_id, r.expected_head_revision, r.expected_head_root_digest,
r.expected_anti_rollback_generation, r.expected_anchor_digest, r.security_time_lineage_digest,
r.expected_security_time_generation, r.expected_security_time_revision, r.state
FROM resolutions r JOIN submissions s ON s.operation_id = r.operation_id
WHERE r.state = 'active_resolution' ORDER BY r.created_at, r.resolution_id`
	insertResolutionSQL = `INSERT INTO resolutions(resolution_id, operation_id, schema_version, kind,
resolution_request_digest, finalization_operation_id, expected_resolution_digest, finalization_request_digest, request_json, head_id,
expected_head_revision, expected_head_root_digest, expected_anti_rollback_generation, expected_anchor_digest,
security_time_lineage_digest, expected_security_time_generation, expected_security_time_revision, state,
checkpoint_generation, checkpoint_anchor_digest, result_head_root_digest, finalization_digest, finalized_at, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active_resolution', NULL, NULL, NULL, NULL, NULL, ?, ?)`
	claimHeadSQL = `UPDATE authority_heads SET active_resolution_id = ?
WHERE security_domain_id = ? AND authority_profile = ? AND head_id = ? AND head_revision = ? AND head_root_digest = ?
AND security_time_lineage_digest = ? AND security_time_generation = ? AND security_time_revision = ?
AND anti_rollback_generation = ? AND anti_rollback_anchor_digest = ? AND active_resolution_id IS NULL`

	anchorResolutionSQL = `UPDATE resolutions SET state = 'anchored', checkpoint_generation = ?, checkpoint_anchor_digest = ?,
result_head_root_digest = ?, finalization_digest = ?, finalized_at = ?, updated_at = ?
WHERE operation_id = ? AND resolution_id = ? AND state = 'active_resolution'
AND resolution_request_digest = ? AND finalization_operation_id = ? AND expected_resolution_digest = ?
AND finalization_request_digest = ? AND head_id = ?
AND expected_head_revision = ? AND expected_head_root_digest = ? AND expected_anti_rollback_generation = ?
AND expected_anchor_digest = ? AND security_time_lineage_digest = ? AND expected_security_time_generation = ?
AND expected_security_time_revision = ?`
	insertCheckpointSQL = `INSERT INTO antirollback_checkpoints(security_domain_id, authority_profile, generation,
predecessor_generation, predecessor_anchor_digest, anchor_digest, result_head_root_digest, anchor_ref, finalization_digest,
finalization_ref, anchored_at, resolution_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	insertTerminalReceiptSQL = `INSERT INTO terminal_receipts(operation_id, resolution_id, schema_version, kind, receipt_id,
authority_id, authority_cluster_id, authority_profile, authority_generation, owner_id, owner_generation,
authority_profile_generation, authority_profile_digest, security_domain_id, head_id, head_revision, head_root_digest,
request_digest, batch_digest, security_time_lineage_digest, security_time_generation, security_time_revision,
anti_rollback_generation, anti_rollback_anchor_digest, quorum_attestation_digest, quorum_attestation_ref,
signature_digest, signature_ref, authority_time, retention_policy_digest, retention_until,
gc_not_before_anchor_generation, receipt_digest, receipt_json, checkpoint_generation, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	advanceHeadSQL = `UPDATE authority_heads SET head_revision = ?, head_root_digest = ?, security_time_generation = ?,
security_time_revision = ?, anti_rollback_generation = ?, anti_rollback_anchor_digest = ?,
anti_rollback_attestation_ref = ?, active_resolution_id = NULL
WHERE security_domain_id = ? AND authority_profile = ? AND head_id = ? AND head_revision = ?
AND head_root_digest = ? AND security_time_lineage_digest = ? AND security_time_generation = ? AND security_time_revision = ?
AND anti_rollback_generation = ? AND anti_rollback_anchor_digest = ? AND active_resolution_id = ?`
	terminalizeSubmissionSQL = `UPDATE submissions SET status = ?, receipt_digest = ?, updated_at = ?
WHERE operation_id = ? AND status = 'pending' AND receipt_digest IS NULL`
	terminalizeResolutionSQL = `UPDATE resolutions SET state = 'terminal', updated_at = ?
WHERE operation_id = ? AND resolution_id = ? AND state = 'anchored'`
)
