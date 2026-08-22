package cicontract

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// DurationLedgerMetaTable 是 duration-ledger authority 的 singleton 元数据表。
	DurationLedgerMetaTable = "duration_ledger_meta"
	// DurationCalibrationsTable 是校准摘要 singleton 的唯一 SQLite 权威表。
	DurationCalibrationsTable = "duration_calibrations"
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
	// RemoteRunPlanningReceiptsTable 是远程 CI 规划回执的 CAS 侧表。
	RemoteRunPlanningReceiptsTable = "ci_remote_run_planning_receipts"
	// RemoteRunPlanningReceiptsStatusIndex 是规划回执状态检索索引。
	RemoteRunPlanningReceiptsStatusIndex = "idx_ci_remote_run_planning_receipts_status"
	// RemoteCleanupJournalsTable 是不依赖 ci_runs 的远程副作用恢复根。
	RemoteCleanupJournalsTable = "ci_remote_cleanup_journals"
	// RemoteCleanupTargetsTable 是远程副作用恢复所需的精确 provider 目标表。
	RemoteCleanupTargetsTable = "ci_remote_cleanup_targets"
	// RemoteCleanupTargetsPendingIndex 是按 job 和目标类型读取恢复项的索引。
	RemoteCleanupTargetsPendingIndex = "idx_ci_remote_cleanup_targets_pending"
	// RemoteRunTargetBindingsTable 是 normal portable run 的 immutable target identity 侧表。
	RemoteRunTargetBindingsTable = "ci_remote_run_target_bindings"
	// RemoteRunTargetBindingsProfileIndex 是 authoritative finalizer 按 profile workload-set 精确核对 target 的索引。
	RemoteRunTargetBindingsProfileIndex = "idx_ci_remote_run_target_bindings_profile"
	// RemoteRunTargetBindingsImmutableTrigger 阻断 target binding 的任何原地改写。
	RemoteRunTargetBindingsImmutableTrigger = "trg_ci_remote_run_target_bindings_immutable"
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
		RemoteRunExecutionIdentitiesTable,
		RetainedWorkloadPassProofsTable,
		WorkloadInputReplayCacheTable,
		RemoteRunPlanningReceiptsTable,
		RemoteCleanupJournalsTable,
		RemoteCleanupTargetsTable,
		RemoteRunTargetBindingsTable,
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
		RemoteRunPlanningReceiptsStatusIndex,
		RemoteCleanupTargetsPendingIndex,
		RemoteRunTargetBindingsProfileIndex,
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
