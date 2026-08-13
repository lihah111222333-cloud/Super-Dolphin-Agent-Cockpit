package cicontract

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// DurationLedgerMetaTable 是 duration-ledger authority 的 singleton 元数据表。
	DurationLedgerMetaTable = "duration_ledger_meta"
	// DurationQueryMetaTable 是 SQLite schema 查询元数据表，不承载远程 CI 事实。
	DurationQueryMetaTable = "ci_query_meta"
	// ShardTerminalContainersTable 是 ECI 终态容器证据表。
	ShardTerminalContainersTable = "ci_shard_terminal_containers"
	// ShardTerminalEventsTable 是 ECI 终态事件证据表。
	ShardTerminalEventsTable = "ci_shard_terminal_events"
	// LocalAuthorityStateTable 是本地 PASS 独立 authority 的 singleton 状态表。
	LocalAuthorityStateTable = "ci_local_authority_state"
	// LocalWorkloadOriginsTable 是本地 PASS 执行 origin 审计表。
	LocalWorkloadOriginsTable = "ci_local_workload_origins"
	// LocalWorkloadExecutionsTable 是本地 PASS 直接执行投影表。
	LocalWorkloadExecutionsTable = "ci_local_workload_executions"
	// LocalWorkloadPassEvidenceTable 是本地 PASS 证据投影表。
	LocalWorkloadPassEvidenceTable = "ci_local_workload_pass_evidence"
	// WorkloadInputReplayCacheTable 是 immutable source-tree 的派生输入索引表。
	WorkloadInputReplayCacheTable = "ci_workload_input_replay_cache"
)

// SQLAuthorityAuxiliaryTables 返回不属于事实域绑定但仍受唯一 SQLite authority 管理的表。
func SQLAuthorityAuxiliaryTables() []string {
	return []string{
		DurationLedgerMetaTable,
		DurationCalibrationsTable,
		DurationQueryMetaTable,
		ShardTerminalContainersTable,
		ShardTerminalEventsTable,
		LocalAuthorityStateTable,
		LocalWorkloadOriginsTable,
		LocalWorkloadExecutionsTable,
		LocalWorkloadPassEvidenceTable,
		RemoteRunExecutionScopesTable,
		RetainedWorkloadPassProofsTable,
		WorkloadInputReplayCacheTable,
	}
}

// SQLAuthorityAdditiveSchemaIndexes 返回 additive remote-CI side-table 的受管索引。
// 它们属于唯一 SQLite authority，但不是独立事实域、retention root 或第二 authority。
func SQLAuthorityAdditiveSchemaIndexes() []string {
	return []string{
		"idx_ci_remote_run_execution_scopes_job",
		"idx_ci_remote_run_execution_scopes_generation",
		RetainedWorkloadPassProofLookupIndex,
		RunWorkloadResultsRetentionIndex,
		WorkloadPassEvidenceSourceReplayIndex,
		RetainedWorkloadPassProofSourceReplayIndex,
	}
}

// SQLAuthoritySchemaTables 返回 gate schema 允许创建的完整表名集合。
func SQLAuthoritySchemaTables() []string {
	auxiliary := SQLAuthorityAuxiliaryTables()
	bindings := sqlAuthorityBindingList()
	result := make([]string, 0, len(bindings)+len(auxiliary))
	for _, binding := range bindings {
		result = append(result, binding.Table)
	}
	result = append(result, auxiliary...)
	slices.Sort(result)
	return result
}

// validateSQLAuthoritySchemaTables 校验 authority 表和 schema 辅助表没有重名或空名。
func validateSQLAuthoritySchemaTables() error {
	seen := make(map[string]struct{}, len(SQLAuthoritySchemaTables()))
	for _, table := range SQLAuthoritySchemaTables() {
		if table == "" || strings.TrimSpace(table) != table {
			return fmt.Errorf("remote CI SQLite schema table %q is invalid", table)
		}
		if _, exists := seen[table]; exists {
			return fmt.Errorf("remote CI SQLite schema table %q is registered more than once", table)
		}
		seen[table] = struct{}{}
	}
	seen = make(map[string]struct{}, len(SQLAuthorityAdditiveSchemaIndexes()))
	for _, index := range SQLAuthorityAdditiveSchemaIndexes() {
		if index == "" || strings.TrimSpace(index) != index {
			return fmt.Errorf("remote CI SQLite schema index %q is invalid", index)
		}
		if _, exists := seen[index]; exists {
			return fmt.Errorf("remote CI SQLite schema index %q is registered more than once", index)
		}
		seen[index] = struct{}{}
	}
	return nil
}
