package heterociowner

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

import _ "modernc.org/sqlite"

const (
	// SchemaVersion 是异构 CI durable owner 本地数据库的冻结版本。
	SchemaVersion = "heteroci-owner/v1"
	driverName    = "sqlite"
)

// schemaStatements 构造 durable owner schema 的冻结 DDL registry。
func schemaStatements() []string {
	return append(schemaOwnerStatements(), schemaRoutingStatements()...)
}

// schemaOwnerStatements 构造 owner 身份与 epoch 的冻结 DDL。
func schemaOwnerStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS heteroci_owner_heads (
        scope TEXT PRIMARY KEY CHECK(length(scope)=71 AND substr(scope,1,7)='sha256:' AND substr(scope,8) NOT GLOB '*[^0-9a-f]*'),
        owner_id TEXT NOT NULL CHECK(length(owner_id)=64 AND owner_id NOT GLOB '*[^0-9a-f]*'),
        owner_epoch INTEGER NOT NULL CHECK(owner_epoch > 0 AND owner_epoch < 9223372036854775807),
        revision INTEGER NOT NULL CHECK(revision > 0 AND revision < 9223372036854775807),
        journal_digest TEXT NOT NULL,
        projection_digest TEXT NOT NULL,
        updated_at_unix_milli INTEGER NOT NULL CHECK(updated_at_unix_milli >= 0)
    )`,
		`CREATE TABLE IF NOT EXISTS heteroci_owner_epochs (
        scope TEXT NOT NULL,
        owner_epoch INTEGER NOT NULL CHECK(owner_epoch > 0 AND owner_epoch < 9223372036854775807),
        owner_id TEXT NOT NULL CHECK(length(owner_id)=64 AND owner_id NOT GLOB '*[^0-9a-f]*'),
        started_at_unix_milli INTEGER NOT NULL CHECK(started_at_unix_milli >= 0),
        retired_at_unix_milli INTEGER,
        PRIMARY KEY(scope, owner_epoch),
        UNIQUE(scope, owner_id),
        FOREIGN KEY(scope) REFERENCES heteroci_owner_heads(scope)
    )`,
	}
}

// schemaRoutingStatements 构造 routing authority 的冻结 DDL。
func schemaRoutingStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS heteroci_routing_runs (
        scope TEXT NOT NULL,
        owner_epoch INTEGER NOT NULL,
        run_id TEXT NOT NULL,
        manifest_json BLOB NOT NULL,
        manifest_digest TEXT NOT NULL,
        units_json BLOB NOT NULL,
        units_digest TEXT NOT NULL,
        heartbeat_ttl_millis INTEGER NOT NULL CHECK(heartbeat_ttl_millis > 0),
        decision_json BLOB NOT NULL,
        decision_digest TEXT NOT NULL,
        released INTEGER NOT NULL CHECK(released IN (0,1)),
        reserved_revision INTEGER NOT NULL CHECK(reserved_revision > 0),
        last_revision INTEGER NOT NULL CHECK(last_revision > 0),
        PRIMARY KEY(scope, owner_epoch, run_id),
        FOREIGN KEY(scope, owner_epoch) REFERENCES heteroci_owner_epochs(scope, owner_epoch)
    )`,
		`CREATE TABLE IF NOT EXISTS heteroci_routing_leases (
        scope TEXT NOT NULL,
        owner_epoch INTEGER NOT NULL,
        run_id TEXT NOT NULL,
        unit_id TEXT NOT NULL,
        lease_json BLOB NOT NULL,
        state TEXT NOT NULL CHECK(state IN ('planned','started','succeeded','failed','cancelled')),
        reserved_at_unix_milli INTEGER NOT NULL CHECK(reserved_at_unix_milli >= 0),
        started_at_unix_milli INTEGER NOT NULL CHECK(started_at_unix_milli >= 0),
        finished_at_unix_milli INTEGER NOT NULL CHECK(finished_at_unix_milli >= 0),
        last_revision INTEGER NOT NULL CHECK(last_revision > 0),
        PRIMARY KEY(scope, owner_epoch, run_id, unit_id),
        FOREIGN KEY(scope, owner_epoch, run_id) REFERENCES heteroci_routing_runs(scope, owner_epoch, run_id)
    )`,
		`CREATE TABLE IF NOT EXISTS heteroci_routing_reservations (
        scope TEXT NOT NULL,
        owner_epoch INTEGER NOT NULL,
        run_id TEXT NOT NULL,
        node_id TEXT NOT NULL,
        probe_identity TEXT NOT NULL,
        probe_boot_id TEXT NOT NULL,
        cpu_milli INTEGER NOT NULL CHECK(cpu_milli >= 0),
        memory_bytes INTEGER NOT NULL CHECK(memory_bytes >= 0),
        max_concurrency INTEGER NOT NULL CHECK(max_concurrency >= 0),
        released INTEGER NOT NULL CHECK(released IN (0,1)),
        last_revision INTEGER NOT NULL CHECK(last_revision > 0),
        PRIMARY KEY(scope, owner_epoch, run_id, node_id, probe_identity, probe_boot_id),
        FOREIGN KEY(scope, owner_epoch, run_id) REFERENCES heteroci_routing_runs(scope, owner_epoch, run_id)
    )`,
		`CREATE TABLE IF NOT EXISTS heteroci_routing_operations (
        scope TEXT NOT NULL,
        owner_epoch INTEGER NOT NULL,
        revision INTEGER NOT NULL CHECK(revision > 0),
        operation_id TEXT NOT NULL CHECK(length(operation_id)=64 AND operation_id NOT GLOB '*[^0-9a-f]*'),
        operation_kind TEXT NOT NULL,
        request_bytes BLOB NOT NULL,
        request_digest TEXT NOT NULL,
        result_bytes BLOB NOT NULL,
        result_digest TEXT NOT NULL,
        prev_journal_digest TEXT NOT NULL,
        journal_digest TEXT NOT NULL,
        projection_digest TEXT NOT NULL,
        committed_at_unix_milli INTEGER NOT NULL CHECK(committed_at_unix_milli >= 0),
        PRIMARY KEY(scope, owner_epoch, revision),
        UNIQUE(scope, owner_epoch, operation_id),
        FOREIGN KEY(scope) REFERENCES heteroci_owner_heads(scope)
    )`,
	}
}

// schemaColumns 构造 durable owner schema 的冻结列 registry。
func schemaColumns() map[string][]string {
	return map[string][]string{
		"heteroci_owner_heads":          {"scope", "owner_id", "owner_epoch", "revision", "journal_digest", "projection_digest", "updated_at_unix_milli"},
		"heteroci_owner_epochs":         {"scope", "owner_epoch", "owner_id", "started_at_unix_milli", "retired_at_unix_milli"},
		"heteroci_routing_runs":         {"scope", "owner_epoch", "run_id", "manifest_json", "manifest_digest", "units_json", "units_digest", "heartbeat_ttl_millis", "decision_json", "decision_digest", "released", "reserved_revision", "last_revision"},
		"heteroci_routing_leases":       {"scope", "owner_epoch", "run_id", "unit_id", "lease_json", "state", "reserved_at_unix_milli", "started_at_unix_milli", "finished_at_unix_milli", "last_revision"},
		"heteroci_routing_reservations": {"scope", "owner_epoch", "run_id", "node_id", "probe_identity", "probe_boot_id", "cpu_milli", "memory_bytes", "max_concurrency", "released", "last_revision"},
		"heteroci_routing_operations":   {"scope", "owner_epoch", "revision", "operation_id", "operation_kind", "request_bytes", "request_digest", "result_bytes", "result_digest", "prev_journal_digest", "journal_digest", "projection_digest", "committed_at_unix_milli"},
	}
}

// SchemaStatements 返回 durable owner schema 的只读 DDL 副本。
func SchemaStatements() []string {
	statements := schemaStatements()
	return append([]string(nil), statements...)
}

// EnsureSchema 初始化并严格预检独立的 heteroci owner SQLite 数据库。
// 空库允许创建；部分、旧版本或含未知对象的数据库必须失败，不能静默重建。
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("heteroci owner database is nil")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin heteroci owner schema transaction: %w", err)
	}
	columns := schemaColumns()
	objects, err := inspectSchemaObjects(ctx, tx, columns)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(objects) != 0 && len(objects) != len(columns) {
		_ = tx.Rollback()
		return fmt.Errorf("incomplete heteroci owner schema: found %d of %d tables", len(objects), len(columns))
	}
	for _, statement := range schemaStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("create heteroci owner schema: %w", err)
		}
	}
	if err := validateSchemaTables(ctx, tx, columns); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit heteroci owner schema: %w", err)
	}
	return nil
}

// rejectUnknownObjects 拒绝独立 owner 数据库中的未知 SQLite 对象。
// inspectSchemaObjects 读取并校验独立 owner 数据库中的用户对象集合。
func inspectSchemaObjects(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, columns map[string][]string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type IN ('table','index','trigger','view') AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, fmt.Errorf("inspect heteroci owner schema objects: %w", err)
	}
	defer rows.Close()
	known := make(map[string]bool, len(columns))
	objects := make(map[string]bool, len(columns))
	for name := range columns {
		known[name] = true
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read heteroci owner schema object: %w", err)
		}
		if !known[name] {
			return nil, fmt.Errorf("unknown heteroci owner schema object %q", name)
		}
		objects[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate heteroci owner schema objects: %w", err)
	}
	return objects, nil
}

func validateSchemaTables(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, columns map[string][]string) error {
	for table, want := range columns {
		got, err := tableColumns(ctx, queryer, table)
		if err != nil {
			return err
		}
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			return fmt.Errorf("heteroci owner table %s columns drifted", table)
		}
	}
	return nil
}

func tableColumns(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, fmt.Errorf("inspect heteroci owner table %s: %w", table, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("read heteroci owner table %s: %w", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate heteroci owner table %s: %w", table, err)
	}
	return columns, nil
}
