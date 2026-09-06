package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// requiredResourceSchemaColumns 返回RESOURCE reserved-only生产路径依赖的完整列集合。
func requiredResourceSchemaColumns() map[string][]string {
	return map[string][]string{
		"orchestration_resource_effective_policy_snapshots": {
			"policy_snapshot_id", "policy_lineage_id", "policy_revision", "requested_kind",
			"caller_principal_ref", "scope_ref", "policy_applicability_digest", "dimension_set",
			"source_policy_set_ref", "source_policy_set_digest", "precedence_trace_ref", "precedence_trace_digest",
			"canonicalization_profile", "canonical_policy_hash", "policy_snapshot_digest", "effective_limits_digest",
			"release_condition_digest", "limit_agent_count", "limit_in_flight_count", "limit_fanout_count",
			"limit_concurrency_count", "limit_wall_time_ms", "limit_input_tokens", "limit_output_tokens",
			"limit_cost_microunit", "limit_content_bytes", "limit_ingress_bytes", "idempotency_namespace",
			"idempotency_key_digest", "publish_request_digest", "publish_operation_id", "publish_operation_digest",
			"publish_receipt_ref", "publish_receipt_digest", "policy_owner_id", "policy_owner_generation",
			"policy_writer_fence_ref", "policy_owner_proof_ref", "deployment_fence_ref", "security_time_generation",
			"effective_at", "valid_until", "schema_version", "created_at",
		},
		"orchestration_resource_reservations": {
			"reservation_id", "reservation_version", "resource_snapshot_id", "resource_snapshot_digest",
			"resource_status", "requested_kind", "command_id", "command_version", "command_profile_digest",
			"caller_principal_ref", "scope_ref", "source_agent_id", "source_binding_version", "source_fence_ref",
			"target_agent_id", "target_binding_version", "target_fence_ref", "identity_fence_digest",
			"content_reference_id", "content_ref", "content_object_id", "content_object_version", "content_ref_version",
			"content_owner_snapshot_digest", "content_admission_snapshot_digest", "policy_snapshot_id", "policy_revision",
			"policy_snapshot_digest", "policy_canonical_hash", "effective_limits_digest", "reserved_agent_count",
			"reserved_in_flight_count", "reserved_fanout_count", "reserved_concurrency_count", "reserved_wall_time_ms",
			"reserved_input_tokens", "reserved_output_tokens", "reserved_cost_microunit", "reserved_content_bytes",
			"reserved_ingress_bytes", "usage_digest_domain", "usage_watermark", "usage_digest", "release_condition_digest",
			"idempotency_namespace", "idempotency_key_digest", "reservation_request_digest", "reservation_operation_id",
			"reservation_operation_digest", "reservation_receipt_ref", "reservation_receipt_digest", "admission_owner_id",
			"admission_owner_generation", "admission_writer_fence_ref", "admission_deployment_fence_ref",
			"security_time_generation", "owner_proof_ref", "valid_until", "schema_version", "created_at",
		},
		"orchestration_resource_usage_ledger": {
			"usage_entry_id", "reservation_id", "reservation_version", "ledger_sequence", "previous_usage_digest",
			"usage_entry_digest", "command_id", "command_version", "command_profile_digest", "identity_fence_digest",
			"content_admission_snapshot_digest", "policy_snapshot_id", "policy_revision", "policy_snapshot_digest",
			"effective_limits_digest", "release_condition_digest", "dimension_set", "dimension", "unit", "delta_value",
			"cumulative_value", "attempt_id", "attempt_fence_ref", "measurement_source_ref", "measurement_id",
			"source_sequence", "observed_at", "measurement_receipt_ref", "measurement_receipt_digest",
			"usage_operation_id", "usage_request_digest", "admission_owner_id", "admission_owner_generation",
			"admission_writer_fence_ref", "admission_deployment_fence_ref", "security_time_generation",
			"schema_version", "recorded_at",
		},
	}
}

// requiredResourceSchemaTriggers 返回RESOURCE relation、immutability与disabled-writer启动必需触发器。
func requiredResourceSchemaTriggers() []string {
	return []string{
		"trg_orchestration_resource_policy_revision_contiguous",
		"trg_orchestration_resource_policy_no_update",
		"trg_orchestration_resource_policy_no_delete",
		"trg_orchestration_resource_reservation_exact_relation",
		"trg_orchestration_resource_reservation_no_update",
		"trg_orchestration_resource_reservation_no_delete",
		"trg_orchestration_resource_usage_insert_disabled",
		"trg_orchestration_resource_usage_no_update",
		"trg_orchestration_resource_usage_no_delete",
	}
}

// verifyResourceSchemaColumnPolicy 拒绝RESOURCE metadata表出现BLOB或原始凭据、secret、payload列。
func verifyResourceSchemaColumnPolicy(ctx context.Context, q any) error {
	queryer, ok := q.(sqlContextQuery)
	if !ok {
		return fmt.Errorf("unsupported SQLite RESOURCE column-policy queryer %T", q)
	}
	violations, err := resourceSchemaColumnPolicyViolations(ctx, queryer)
	if err != nil {
		return fmt.Errorf("verify SQLite RESOURCE metadata-only column policy: %w", err)
	}
	if len(violations) > 0 {
		return fmt.Errorf("SQLite RESOURCE metadata-only schema contains forbidden column(s): %s", strings.Join(violations, ", "))
	}
	return nil
}

// resourceSchemaColumnPolicyViolations 汇总三张RESOURCE表中的禁止列并稳定排序。
func resourceSchemaColumnPolicyViolations(ctx context.Context, q sqlContextQuery) ([]string, error) {
	tables := make([]string, 0, len(requiredResourceSchemaColumns()))
	for table := range requiredResourceSchemaColumns() {
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
			if strings.EqualFold(strings.TrimSpace(column.declaredType), "BLOB") || forbiddenResourceColumnName(column.name) {
				violations = append(violations, table+"."+column.name)
			}
		}
	}
	sort.Strings(violations)
	return violations, nil
}

func forbiddenResourceColumnName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, part := range strings.FieldsFunc(normalized, func(r rune) bool {
		return r < 'a' || r > 'z'
	}) {
		switch part {
		case "payload", "credential", "credentials", "secret", "secrets", "password", "passwd":
			return true
		}
	}

	switch normalized {
	case "raw_key", "private_key", "api_key", "access_token", "refresh_token", "bearer_token":
		return true
	default:
		return false
	}
}
