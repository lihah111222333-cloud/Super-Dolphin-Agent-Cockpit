package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// requiredContentSchemaColumns 返回CONTENT-1 production读取依赖的完整列集合。
func requiredContentSchemaColumns() map[string][]string {
	return map[string][]string{
		"orchestration_legacy_command_content_quarantine": {
			"command_id", "complete_intent_digest", "reason", "source_schema_version", "schema_version", "created_at",
		},
		"orchestration_content_ingest_reservations": {
			"ingest_reservation_id", "ingest_ticket_id", "object_id", "object_version", "owner_id", "owner_generation",
			"writer_fence_ref", "security_time_generation", "workspace_ref", "caller_principal_ref", "scope_ref",
			"source_agent_id", "source_identity_kind", "source_identity_reservation_id", "source_reservation_generation",
			"source_incarnation_id", "source_incarnation_generation", "source_external_identity_ref", "source_binding_version",
			"source_binding_digest", "source_fence_ref", "source_active_process_receipt_ref", "source_binding_updated_at",
			"idempotency_namespace", "idempotency_key_digest", "proof_request_digest", "candidate_ref_digest",
			"content_intent_digest", "digest_key_id", "digest_key_version", "ingest_kind", "size_bytes", "media_type",
			"permitted_sink_role", "aead_algorithm", "key_wrap_profile", "aad_profile", "quota_snapshot_ref",
			"quota_snapshot_digest", "retention_policy_id", "retention_policy_version", "retention_policy_digest",
			"assembly_deadline_at", "prepare_operation_id", "prepare_started_at", "revision", "state", "schema_version",
			"created_at", "updated_at",
		},
		"orchestration_content_objects": {
			"object_id", "object_version", "ingest_reservation_id", "owner_id", "owner_generation", "writer_fence_ref",
			"security_time_generation", "workspace_ref", "source_agent_id", "source_fence_ref", "aead_algorithm",
			"key_wrap_profile", "key_handle_ref", "key_receipt_ref", "key_receipt_digest", "nonce_hex", "aad_profile",
			"aad_digest", "media_type", "size_bytes", "ciphertext_sha256", "sealed_payload_size_bytes",
			"retention_policy_digest", "content_state", "key_state", "ref_version", "revision", "promoted_at",
			"schema_version", "created_at", "updated_at",
		},
		"orchestration_content_private_payload_receipts": {
			"object_id", "object_version", "ingest_reservation_id", "backend_kind", "object_store_id", "payload_format",
			"private_payload_ref", "sealed_payload_size_bytes", "ciphertext_sha256", "store_operation_id",
			"store_request_digest", "store_receipt_ref", "store_receipt_digest", "stored_at", "schema_version",
		},
		"orchestration_content_refs": {
			"reference_id", "canonical_ref", "object_id", "object_version", "ingest_reservation_id", "ref_version",
			"reference_kind", "reference_state", "caller_principal_ref", "scope_ref", "source_agent_id",
			"source_fence_ref", "proof_request_digest", "proof_id", "proof_revision", "owner_id", "owner_generation",
			"writer_fence_ref", "security_time_generation", "owner_proof_ref", "proof_snapshot_digest",
			"proof_expires_at", "media_type", "size_bytes", "retention_policy_digest", "schema_version", "created_at",
		},
		"orchestration_command_content_deletion_fences": {
			"command_id", "reference_id", "canonical_ref", "object_id", "object_version", "ref_version",
			"fence_kind", "fence_state", "command_profile_digest", "source_fence_ref", "target_fence_ref",
			"schema_version", "created_at",
		},
	}
}

// requiredContentSchemaTriggers 返回CONTENT-1 relation/state/immutability启动必需触发器。
func requiredContentSchemaTriggers() []string {
	return []string{
		"trg_orchestration_legacy_command_content_quarantine_no_update",
		"trg_orchestration_legacy_command_content_quarantine_no_delete",
		"trg_orchestration_content_ingest_exact_identity",
		"trg_orchestration_content_ingest_immutable_metadata",
		"trg_orchestration_content_ingest_state_machine",
		"trg_orchestration_content_ingest_no_delete",
		"trg_orchestration_content_objects_exact_reservation",
		"trg_orchestration_content_objects_immutable_metadata",
		"trg_orchestration_content_objects_prepared_to_active",
		"trg_orchestration_content_objects_no_delete",
		"trg_orchestration_content_payload_exact_relation",
		"trg_orchestration_content_payload_no_update",
		"trg_orchestration_content_payload_no_delete",
		"trg_orchestration_content_refs_exact_active_relation",
		"trg_orchestration_content_refs_no_update",
		"trg_orchestration_content_refs_no_delete",
		"trg_orchestration_command_profiles_content_relation",
		"trg_orchestration_command_profiles_create_content_fence",
		"trg_orchestration_command_content_fences_exact_relation",
		"trg_orchestration_command_content_fences_no_update",
		"trg_orchestration_command_content_fences_no_delete",
	}
}

// verifyContentSchemaColumnPolicy 拒绝CONTENT metadata表出现可承载正文、密文或key material的列。
func verifyContentSchemaColumnPolicy(ctx context.Context, q any) error {
	queryer, ok := q.(sqlContextQuery)
	if !ok {
		return fmt.Errorf("unsupported SQLite CONTENT column-policy queryer %T", q)
	}
	violations, err := contentSchemaColumnPolicyViolations(ctx, queryer)
	if err != nil {
		return fmt.Errorf("verify SQLite CONTENT metadata-only column policy: %w", err)
	}
	if len(violations) > 0 {
		return fmt.Errorf("SQLite CONTENT metadata-only schema contains forbidden column(s): %s", strings.Join(violations, ", "))
	}
	return nil
}

func contentSchemaColumnPolicyViolations(ctx context.Context, q sqlContextQuery) ([]string, error) {
	tables := make([]string, 0, len(requiredContentSchemaColumns()))
	for table := range requiredContentSchemaColumns() {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	var violations []string
	for _, table := range tables {
		columns, err := sqliteDeclaredColumns(ctx, q, table)
		if err != nil {
			return nil, err
		}
		for _, column := range columns {
			if strings.EqualFold(strings.TrimSpace(column.declaredType), "BLOB") || forbiddenContentColumnName(column.name) {
				violations = append(violations, table+"."+column.name)
			}
		}
	}
	sort.Strings(violations)
	return violations, nil
}

type sqliteDeclaredColumn struct {
	name         string
	declaredType string
}

func sqliteDeclaredColumns(ctx context.Context, q sqlContextQuery, table string) ([]sqliteDeclaredColumn, error) {
	rows, err := q.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []sqliteDeclaredColumn
	for rows.Next() {
		var (
			cid        int
			column     sqliteDeclaredColumn
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &column.name, &column.declaredType, &notNull, &defaultVal, &primaryKey); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func forbiddenContentColumnName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "plaintext", "plaintext_bytes", "raw_plaintext", "raw_content", "content_body", "body", "body_bytes",
		"ciphertext", "ciphertext_bytes", "tag", "auth_tag", "gcm_tag", "dek", "raw_dek", "wrapped_dek",
		"kek", "raw_key", "key_bytes", "wrapped_key":
		return true
	default:
		return false
	}
}
