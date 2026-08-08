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
)

// SQLAuthorityAuxiliaryTables 返回不属于事实域绑定但仍受唯一 SQLite authority 管理的表。
func SQLAuthorityAuxiliaryTables() []string {
	return []string{
		DurationLedgerMetaTable,
		DurationCalibrationsTable,
		DurationQueryMetaTable,
		ShardTerminalContainersTable,
		ShardTerminalEventsTable,
	}
}

// SQLAuthoritySchemaTables 返回 gate schema 允许创建的完整表名集合。
func SQLAuthoritySchemaTables() []string {
	auxiliary := SQLAuthorityAuxiliaryTables()
	result := make([]string, 0, len(sqlAuthorityBindings)+len(auxiliary))
	for _, binding := range sqlAuthorityBindings {
		result = append(result, binding.Table)
	}
	result = append(result, auxiliary...)
	slices.Sort(result)
	return result
}

// validateSQLAuthoritySchemaTable 拒绝未登记的 SQLite schema 表，防止额外表成为第二 authority。
func validateSQLAuthoritySchemaTable(table string) error {
	if !slices.Contains(SQLAuthoritySchemaTables(), table) {
		return fmt.Errorf("remote CI SQLite schema table %q is not registered by cicontract", table)
	}
	return nil
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
	return nil
}
