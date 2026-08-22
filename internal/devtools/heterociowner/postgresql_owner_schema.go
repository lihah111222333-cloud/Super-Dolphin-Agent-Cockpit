package heterociowner

import (
	"slices"
	"strings"
)

// PostgreSQL owner 的 schema、最小权限 writer role 与提交同步语义属于 owner
// 核心边界；它们不再通过已退役的旧 protocol 配置暴露。
const (
	PostgreSQLOwnerSchema                       = "heteroci_owner"
	PostgreSQLOwnerRole                         = "heteroci_owner_writer"
	PostgreSQLOwnerSynchronousCommitRemoteApply = "remote_apply"
)

// PostgreSQLOwnerRuntimeSchemaVersion 是包含 external binding audit 列的 remote PostgreSQL-only owner schema 版本。
const PostgreSQLOwnerRuntimeSchemaVersion = "heteroci-owner-postgresql/v2"

const (
	// PostgreSQLOwnerActiveEpochIndex 保证一个 owner scope 只有一个未退休 epoch。
	PostgreSQLOwnerActiveEpochIndex = "idx_heteroci_owner_epochs_active_scope"
	// PostgreSQLOwnerScopeRunIndex 支持跨 epoch 按 scope/run 定位并按 epoch 排序。
	PostgreSQLOwnerScopeRunIndex = "idx_heteroci_owner_routing_runs_scope_run_epoch"
	// PostgreSQLOwnerActiveReservationIndex 支持规划时只读取 planned/started 容量占用。
	PostgreSQLOwnerActiveReservationIndex = "idx_heteroci_owner_routing_reservations_active_scope_epoch"
)

type postgresqlOwnerIndexDefinition struct {
	name      string
	statement string
}

// postgresqlOwnerIndexDefinitions 是显式 owner index 的唯一注册表。
// PRIMARY KEY 与 UNIQUE 约束生成的隐式索引不在这里重复登记。
func postgresqlOwnerIndexDefinitions() []postgresqlOwnerIndexDefinition {
	return []postgresqlOwnerIndexDefinition{
		{
			name:      PostgreSQLOwnerActiveEpochIndex,
			statement: "CREATE UNIQUE INDEX " + PostgreSQLOwnerActiveEpochIndex + " ON heteroci_owner.owner_epochs (scope) WHERE retired_at_unix_milli IS NULL",
		},
		{
			name:      PostgreSQLOwnerScopeRunIndex,
			statement: "CREATE INDEX " + PostgreSQLOwnerScopeRunIndex + " ON heteroci_owner.routing_runs (scope, run_id, owner_epoch)",
		},
		{
			name:      PostgreSQLOwnerActiveReservationIndex,
			statement: "CREATE INDEX " + PostgreSQLOwnerActiveReservationIndex + " ON heteroci_owner.routing_reservations (scope, owner_epoch, run_id) WHERE state IN ('planned', 'started')",
		},
	}
}

// postgresqlOwnerSchemaStatements 返回 operator clean create-only DDL；重复应用必须 fail-fast。
// 管理员必须预创建 LOGIN 最小权限 writer role；backend 不创建 role、credential 或运行这些语句。
func postgresqlOwnerSchemaStatements() []string {
	statements := []string{
		"CREATE SCHEMA " + PostgreSQLOwnerSchema + " AUTHORIZATION " + PostgreSQLOwnerRole,
		"COMMENT ON SCHEMA " + PostgreSQLOwnerSchema + " IS '" + PostgreSQLOwnerRuntimeSchemaVersion + "'",
		`CREATE TABLE heteroci_owner.owner_heads (scope TEXT PRIMARY KEY CHECK (scope ~ '^sha256:[0-9a-f]{64}$'), owner_id TEXT NOT NULL CHECK (owner_id ~ '^[0-9a-f]{64}$'), owner_epoch BIGINT NOT NULL CHECK (owner_epoch > 0 AND owner_epoch < 9223372036854775807), revision BIGINT NOT NULL CHECK (revision > 0 AND revision < 9223372036854775807), journal_digest TEXT NOT NULL CHECK (journal_digest ~ '^sha256:[0-9a-f]{64}$'), projection_digest TEXT NOT NULL CHECK (projection_digest ~ '^sha256:[0-9a-f]{64}$'), updated_at_unix_milli BIGINT NOT NULL CHECK (updated_at_unix_milli >= 0))`,
		`CREATE TABLE heteroci_owner.owner_epochs (scope TEXT NOT NULL REFERENCES heteroci_owner.owner_heads(scope), owner_epoch BIGINT NOT NULL CHECK (owner_epoch > 0 AND owner_epoch < 9223372036854775807), owner_id TEXT NOT NULL CHECK (owner_id ~ '^[0-9a-f]{64}$'), started_at_unix_milli BIGINT NOT NULL CHECK (started_at_unix_milli >= 0), retired_at_unix_milli BIGINT CHECK (retired_at_unix_milli >= started_at_unix_milli), PRIMARY KEY (scope, owner_epoch), UNIQUE (scope, owner_id))`,
		`CREATE TABLE heteroci_owner.operation_journal (scope TEXT NOT NULL, operation_id TEXT NOT NULL CHECK (operation_id ~ '^[0-9a-f]{64}$'), owner_id TEXT NOT NULL CHECK (owner_id ~ '^[0-9a-f]{64}$'), owner_epoch BIGINT NOT NULL, revision BIGINT NOT NULL CHECK (revision > 1 AND revision < 9223372036854775807), kind TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 128), request_bytes BYTEA NOT NULL CHECK (octet_length(request_bytes) > 0), request_digest TEXT NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'), result_bytes BYTEA NOT NULL CHECK (octet_length(result_bytes) > 0), result_digest TEXT NOT NULL CHECK (result_digest ~ '^sha256:[0-9a-f]{64}$'), previous_journal_digest TEXT NOT NULL CHECK (previous_journal_digest ~ '^sha256:[0-9a-f]{64}$'), journal_digest TEXT NOT NULL CHECK (journal_digest ~ '^sha256:[0-9a-f]{64}$'), projection_digest TEXT NOT NULL CHECK (projection_digest ~ '^sha256:[0-9a-f]{64}$'), committed_at_unix_milli BIGINT NOT NULL CHECK (committed_at_unix_milli >= 0), external_binding_digest TEXT CHECK (external_binding_digest ~ '^sha256:[0-9a-f]{64}$'), manifest_blob_sha256 TEXT CHECK (manifest_blob_sha256 ~ '^sha256:[0-9a-f]{64}$'), runtime_policy_digest TEXT CHECK (runtime_policy_digest ~ '^sha256:[0-9a-f]{64}$'), PRIMARY KEY (scope, operation_id), UNIQUE (scope, owner_epoch, revision), FOREIGN KEY (scope, owner_epoch) REFERENCES heteroci_owner.owner_epochs(scope, owner_epoch))`,
		`CREATE TABLE heteroci_owner.routing_runs (scope TEXT NOT NULL, owner_epoch BIGINT NOT NULL, run_id TEXT NOT NULL CHECK (length(run_id) BETWEEN 1 AND 512), repository_id TEXT NOT NULL CHECK (length(repository_id) BETWEEN 1 AND 512), source_tree_sha TEXT NOT NULL CHECK (source_tree_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'), plan_operation_id TEXT NOT NULL, heartbeat_ttl_milli BIGINT NOT NULL CHECK (heartbeat_ttl_milli > 0), last_revision BIGINT NOT NULL CHECK (last_revision > 1), created_at_unix_milli BIGINT NOT NULL CHECK (created_at_unix_milli >= 0), PRIMARY KEY (scope, owner_epoch, run_id), UNIQUE (scope, plan_operation_id), FOREIGN KEY (scope, plan_operation_id) REFERENCES heteroci_owner.operation_journal(scope, operation_id), FOREIGN KEY (scope, owner_epoch) REFERENCES heteroci_owner.owner_epochs(scope, owner_epoch))`,
		`CREATE TABLE heteroci_owner.routing_reservations (scope TEXT NOT NULL, owner_epoch BIGINT NOT NULL, run_id TEXT NOT NULL, node_id TEXT NOT NULL, probe_identity TEXT NOT NULL, probe_boot_id TEXT NOT NULL, cpu_milli BIGINT NOT NULL CHECK (cpu_milli > 0), memory_bytes BIGINT NOT NULL CHECK (memory_bytes > 0), max_concurrency BIGINT NOT NULL CHECK (max_concurrency > 0), state TEXT NOT NULL CHECK (state IN ('planned', 'started', 'finished')), last_revision BIGINT NOT NULL CHECK (last_revision > 1), PRIMARY KEY (scope, owner_epoch, run_id, node_id, probe_identity, probe_boot_id), FOREIGN KEY (scope, owner_epoch, run_id) REFERENCES heteroci_owner.routing_runs(scope, owner_epoch, run_id))`,
		`CREATE TABLE heteroci_owner.routing_leases (scope TEXT NOT NULL, owner_epoch BIGINT NOT NULL, run_id TEXT NOT NULL, unit_id TEXT NOT NULL, state TEXT NOT NULL CHECK (state IN ('planned', 'started', 'finished')), lease_bytes BYTEA NOT NULL CHECK (octet_length(lease_bytes) > 0), started_bytes BYTEA, terminal_bytes BYTEA, last_revision BIGINT NOT NULL CHECK (last_revision > 1), started_at_unix_milli BIGINT CHECK (started_at_unix_milli >= 0), finished_at_unix_milli BIGINT CHECK (finished_at_unix_milli >= started_at_unix_milli), PRIMARY KEY (scope, owner_epoch, run_id, unit_id), FOREIGN KEY (scope, owner_epoch, run_id) REFERENCES heteroci_owner.routing_runs(scope, owner_epoch, run_id))`,
	}
	for _, index := range postgresqlOwnerIndexDefinitions() {
		statements = append(statements, index.statement)
	}
	statements = append(statements, "REVOKE ALL ON SCHEMA "+PostgreSQLOwnerSchema+" FROM PUBLIC")
	for _, table := range postgresqlOwnerTableNames() {
		statements = append(statements, "ALTER TABLE "+PostgreSQLOwnerSchema+"."+table+" OWNER TO "+PostgreSQLOwnerRole)
		statements = append(statements, "REVOKE ALL ON TABLE "+PostgreSQLOwnerSchema+"."+table+" FROM PUBLIC")
	}
	return statements
}

// PostgreSQLOwnerDDL 返回由受信管理员一次性应用的 clean create-only DDL。
// backend 不调用本函数；重复应用必须由 PostgreSQL 原子拒绝。
func PostgreSQLOwnerDDL() string {
	return strings.Join(postgresqlOwnerSchemaStatements(), ";\n") + ";"
}

// PostgreSQLOwnerAdditiveIndexDDL 返回供已创建空库一次性应用的 create-only index DDL。
// 它不创建 schema/table，不使用 IF NOT EXISTS，重复应用必须由 PostgreSQL 拒绝。
func PostgreSQLOwnerAdditiveIndexDDL() string {
	definitions := postgresqlOwnerIndexDefinitions()
	statements := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		statements = append(statements, definition.statement)
	}
	return strings.Join(statements, ";\n") + ";"
}

func postgresqlOwnerIndexNames() []string {
	definitions := postgresqlOwnerIndexDefinitions()
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.name)
	}
	slices.Sort(names)
	return names
}

func postgresqlOwnerTableNames() []string {
	return []string{"operation_journal", "owner_epochs", "owner_heads", "routing_leases", "routing_reservations", "routing_runs"}
}
