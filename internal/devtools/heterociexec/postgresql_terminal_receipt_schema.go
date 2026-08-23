package heterociexec

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	// PostgreSQLReceiptTerminalManifestIndex 支持按 manifest 查询 terminal receipt。
	PostgreSQLReceiptTerminalManifestIndex = "idx_heteroci_receipt_terminal_manifest_operation"
	// PostgreSQLReceiptEventStageIndex 支持按 operation、stage 重放事件序列。
	PostgreSQLReceiptEventStageIndex = "idx_heteroci_receipt_events_operation_stage"
	// PostgreSQLReceiptTerminalShardIndex 支持按 operation 精确读取排序 shard。
	PostgreSQLReceiptTerminalShardIndex = "idx_heteroci_receipt_terminal_shards_operation_node"
	// PostgreSQLReceiptEventShardIndex 支持按 operation、seq 精确读取 event shard。
	PostgreSQLReceiptEventShardIndex = "idx_heteroci_receipt_event_shards_operation_seq_node"
)

const postgreSQLTerminalReceiptDDL = `CREATE TABLE heteroci_receipt.terminal_receipts (
    schema_version TEXT NOT NULL CHECK (schema_version = 'remote-ci-result/v1'),
    operation_id TEXT NOT NULL CHECK (operation_id ~ '^[0-9a-f]{64}$'),
    request_digest TEXT NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    manifest_digest TEXT NOT NULL CHECK (manifest_digest ~ '^sha256:[0-9a-f]{64}$'),
    plan_digest TEXT NOT NULL CHECK (plan_digest ~ '^sha256:[0-9a-f]{64}$'),
    executor_binding_digest TEXT NOT NULL CHECK (executor_binding_digest ~ '^sha256:[0-9a-f]{64}$'),
    node_set_digest TEXT NOT NULL CHECK (node_set_digest ~ '^sha256:[0-9a-f]{64}$'),
    authority_generation BIGINT NOT NULL CHECK (authority_generation > 0),
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    exit_code INTEGER NOT NULL CHECK (exit_code BETWEEN -1 AND 255),
    cleanup_state TEXT NOT NULL CHECK (cleanup_state = 'verified'),
    error_code TEXT NOT NULL CHECK (length(error_code) <= 128),
    capacity_code TEXT NOT NULL CHECK (length(capacity_code) <= 128),
    status TEXT NOT NULL CHECK (status IN ('succeeded', 'failed', 'blocked')),
    shard_set_bytes BYTEA NOT NULL,
    shard_set_digest TEXT NOT NULL CHECK (shard_set_digest ~ '^sha256:[0-9a-f]{64}$'),
    event_seq BIGINT NOT NULL CHECK (event_seq > 0),
    event_digest TEXT NOT NULL CHECK (event_digest ~ '^sha256:[0-9a-f]{64}$'),
    receipt_bytes BYTEA NOT NULL CHECK (octet_length(receipt_bytes) > 0),
    receipt_digest TEXT NOT NULL CHECK (receipt_digest ~ '^sha256:[0-9a-f]{64}$'),
    PRIMARY KEY (operation_id)
);

CREATE TABLE heteroci_receipt.terminal_shards (
    operation_id TEXT NOT NULL CHECK (operation_id ~ '^[0-9a-f]{64}$'),
    child_operation_id TEXT NOT NULL CHECK (child_operation_id ~ '^[0-9a-f]{64}$'),
    node_id TEXT NOT NULL CHECK (length(node_id) BETWEEN 1 AND 256),
    node_id_digest TEXT NOT NULL CHECK (node_id_digest ~ '^sha256:[0-9a-f]{64}$'),
    executor_kind TEXT NOT NULL CHECK (length(executor_kind) BETWEEN 1 AND 256),
    shard_digest TEXT NOT NULL CHECK (shard_digest ~ '^sha256:[0-9a-f]{64}$'),
    batch_set_digest TEXT NOT NULL CHECK (batch_set_digest ~ '^sha256:[0-9a-f]{64}$'),
    unit_set_digest TEXT NOT NULL CHECK (unit_set_digest ~ '^sha256:[0-9a-f]{64}$'),
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    exit_code INTEGER NOT NULL CHECK (exit_code BETWEEN 0 AND 255),
    cleanup_state TEXT NOT NULL CHECK (cleanup_state = 'verified'),
    error_code TEXT NOT NULL CHECK (length(error_code) <= 128),
    capacity_code TEXT NOT NULL CHECK (capacity_code = ''),
    status TEXT NOT NULL CHECK (status IN ('succeeded', 'failed')),
    PRIMARY KEY (operation_id, node_id),
    UNIQUE (operation_id, child_operation_id),
    FOREIGN KEY (operation_id) REFERENCES heteroci_receipt.terminal_receipts(operation_id)
);

CREATE TABLE heteroci_receipt.receipt_events (
    schema_version TEXT NOT NULL CHECK (schema_version = 'remote-ci-event/v1'),
    operation_id TEXT NOT NULL CHECK (operation_id ~ '^[0-9a-f]{64}$'),
    manifest_digest TEXT NOT NULL CHECK (manifest_digest ~ '^sha256:[0-9a-f]{64}$'),
    plan_digest TEXT NOT NULL CHECK (plan_digest ~ '^sha256:[0-9a-f]{64}$'),
    executor_binding_digest TEXT NOT NULL CHECK (executor_binding_digest ~ '^sha256:[0-9a-f]{64}$'),
    node_set_digest TEXT NOT NULL CHECK (node_set_digest ~ '^sha256:[0-9a-f]{64}$'),
    authority_generation BIGINT NOT NULL CHECK (authority_generation > 0),
    seq BIGINT NOT NULL CHECK (seq > 0),
    occurred_at TEXT NOT NULL,
    stage TEXT NOT NULL CHECK (stage IN ('request_validation', 'manifest_binding', 'capacity_snapshot', 'reservation', 'materialize', 'execute', 'cleanup', 'terminal')),
    state TEXT NOT NULL CHECK (state IN ('started', 'succeeded', 'completed', 'failed', 'blocked', 'skipped')),
    code TEXT NOT NULL CHECK (length(code) <= 128),
    shard_set_bytes BYTEA NOT NULL,
    shard_set_digest TEXT NOT NULL CHECK (shard_set_digest ~ '^sha256:[0-9a-f]{64}$'),
    event_bytes BYTEA NOT NULL CHECK (octet_length(event_bytes) > 0),
    event_digest TEXT NOT NULL CHECK (event_digest ~ '^sha256:[0-9a-f]{64}$'),
    PRIMARY KEY (operation_id, seq),
    UNIQUE (operation_id, event_digest)
);

CREATE TABLE heteroci_receipt.receipt_event_shards (
    operation_id TEXT NOT NULL CHECK (operation_id ~ '^[0-9a-f]{64}$'),
    seq BIGINT NOT NULL CHECK (seq > 0),
    node_id TEXT NOT NULL CHECK (length(node_id) BETWEEN 1 AND 256),
    node_id_digest TEXT NOT NULL CHECK (node_id_digest ~ '^sha256:[0-9a-f]{64}$'),
    executor_kind TEXT NOT NULL CHECK (length(executor_kind) BETWEEN 1 AND 256),
    shard_digest TEXT NOT NULL CHECK (shard_digest ~ '^sha256:[0-9a-f]{64}$'),
    batch_set_digest TEXT NOT NULL CHECK (batch_set_digest ~ '^sha256:[0-9a-f]{64}$'),
    unit_set_digest TEXT NOT NULL CHECK (unit_set_digest ~ '^sha256:[0-9a-f]{64}$'),
    state TEXT NOT NULL CHECK (state IN ('started', 'succeeded', 'completed', 'failed', 'blocked', 'skipped')),
    code TEXT NOT NULL CHECK (length(code) <= 128),
    PRIMARY KEY (operation_id, seq, node_id),
    FOREIGN KEY (operation_id, seq) REFERENCES heteroci_receipt.receipt_events(operation_id, seq)
);

CREATE INDEX ` + PostgreSQLReceiptTerminalManifestIndex + ` ON heteroci_receipt.terminal_receipts (manifest_digest, operation_id);
CREATE INDEX ` + PostgreSQLReceiptEventStageIndex + ` ON heteroci_receipt.receipt_events (operation_id, stage, seq);
CREATE INDEX ` + PostgreSQLReceiptTerminalShardIndex + ` ON heteroci_receipt.terminal_shards (operation_id, node_id);
CREATE INDEX ` + PostgreSQLReceiptEventShardIndex + ` ON heteroci_receipt.receipt_event_shards (operation_id, seq, node_id);

ALTER TABLE heteroci_receipt.terminal_receipts OWNER TO heteroci_receipt_writer;
ALTER TABLE heteroci_receipt.terminal_shards OWNER TO heteroci_receipt_writer;
ALTER TABLE heteroci_receipt.receipt_events OWNER TO heteroci_receipt_writer;
ALTER TABLE heteroci_receipt.receipt_event_shards OWNER TO heteroci_receipt_writer;
REVOKE ALL ON TABLE heteroci_receipt.terminal_receipts FROM PUBLIC;
REVOKE ALL ON TABLE heteroci_receipt.terminal_shards FROM PUBLIC;
REVOKE ALL ON TABLE heteroci_receipt.receipt_events FROM PUBLIC;
REVOKE ALL ON TABLE heteroci_receipt.receipt_event_shards FROM PUBLIC;
GRANT SELECT, INSERT ON TABLE heteroci_receipt.terminal_receipts TO heteroci_receipt_writer;
GRANT SELECT, INSERT ON TABLE heteroci_receipt.terminal_shards TO heteroci_receipt_writer;
GRANT SELECT, INSERT ON TABLE heteroci_receipt.receipt_events TO heteroci_receipt_writer;
GRANT SELECT, INSERT ON TABLE heteroci_receipt.receipt_event_shards TO heteroci_receipt_writer;`

// PostgreSQLTerminalReceiptDDL 是管理员在远端 receipt schema 上一次性重建 terminal/event 表的 create-only DDL。
const PostgreSQLTerminalReceiptDDL = postgreSQLTerminalReceiptDDL

const postgreSQLStructuredReceiptObjectsQuery = `SELECT
    to_regclass('heteroci_receipt.terminal_receipts') IS NOT NULL,
    to_regclass('heteroci_receipt.receipt_events') IS NOT NULL,
    to_regclass('heteroci_receipt.terminal_shards') IS NOT NULL,
    to_regclass('heteroci_receipt.receipt_event_shards') IS NOT NULL,
    COALESCE((SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'heteroci_receipt'), ''),
    COALESCE((SELECT string_agg(c.relname || ':' || pg_get_userbyid(c.relowner), ',' ORDER BY c.relname)
              FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
              WHERE n.nspname = 'heteroci_receipt' AND c.relkind = 'r'), ''),
    COALESCE((SELECT string_agg(i.relname, ',' ORDER BY i.relname)
              FROM pg_class i JOIN pg_index ix ON ix.indexrelid = i.oid
              JOIN pg_class t ON t.oid = ix.indrelid JOIN pg_namespace n ON n.oid = t.relnamespace
              WHERE n.nspname = 'heteroci_receipt' AND i.relname LIKE 'idx_heteroci_receipt_%'
                AND ix.indisvalid AND ix.indisready), '')`

func expectedPostgreSQLReceiptTableOwnership() string {
	return "batch_receipts:" + PostgreSQLReceiptWriterRole + ",receipt_event_shards:" + PostgreSQLReceiptWriterRole + ",receipt_events:" + PostgreSQLReceiptWriterRole + ",terminal_receipts:" + PostgreSQLReceiptWriterRole + ",terminal_shards:" + PostgreSQLReceiptWriterRole
}

func expectedPostgreSQLReceiptIndexNames() string {
	names := []string{PostgreSQLReceiptEventShardIndex, PostgreSQLReceiptEventStageIndex, PostgreSQLReceiptTerminalManifestIndex, PostgreSQLReceiptTerminalShardIndex}
	return strings.Join(names, ",")
}

func validatePostgreSQLStructuredReceiptObjects(ctx context.Context, tx *sql.Tx) error {
	var terminalExists, eventExists, terminalShardsExist, eventShardsExist bool
	var schemaOwner, tableOwnership, indexNames string
	if err := tx.QueryRowContext(ctx, postgreSQLStructuredReceiptObjectsQuery).Scan(&terminalExists, &eventExists, &terminalShardsExist, &eventShardsExist, &schemaOwner, &tableOwnership, &indexNames); err != nil {
		return fmt.Errorf("%w: inspect structured receipt schema: %w", ErrPostgreSQLReceiptStore, err)
	}
	if !terminalExists || !eventExists || !terminalShardsExist || !eventShardsExist || schemaOwner != PostgreSQLReceiptWriterRole || tableOwnership != expectedPostgreSQLReceiptTableOwnership() || indexNames != expectedPostgreSQLReceiptIndexNames() {
		return fmt.Errorf("%w: structured receipt schema or index set drifted", ErrPostgreSQLReceiptStore)
	}
	return nil
}
