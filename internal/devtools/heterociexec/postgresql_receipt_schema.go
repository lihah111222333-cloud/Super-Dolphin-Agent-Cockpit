package heterociexec

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
)

const (
	PostgreSQLReceiptSchema     = "heteroci_receipt"
	PostgreSQLReceiptWriterRole = "heteroci_receipt_writer"
)

// PostgreSQLReceiptDDL 是由受信数据库管理员单独应用的 receipt authority 基线 DDL。
const PostgreSQLReceiptDDL = `CREATE ROLE heteroci_receipt_writer
    LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;

CREATE SCHEMA heteroci_receipt AUTHORIZATION heteroci_receipt_writer;
REVOKE ALL ON SCHEMA heteroci_receipt FROM PUBLIC;

CREATE TABLE heteroci_receipt.batch_receipts (
    schema_version TEXT NOT NULL CHECK (schema_version = 'heteroci-batch-receipt/v1'),
    batch_id TEXT NOT NULL CHECK (batch_id ~ '^[0-9a-f]{64}$'),
    material_digest TEXT NOT NULL CHECK (material_digest ~ '^sha256:[0-9a-f]{64}$'),
    command_digest TEXT NOT NULL CHECK (command_digest ~ '^sha256:[0-9a-f]{64}$'),
    state TEXT NOT NULL CHECK (state IN ('claimed', 'terminal')),
    claim_bytes BYTEA NOT NULL,
    claim_digest TEXT NOT NULL CHECK (claim_digest ~ '^sha256:[0-9a-f]{64}$'),
    result_bytes BYTEA,
    result_digest TEXT,
    PRIMARY KEY (batch_id, material_digest),
    CHECK (
        (state = 'claimed' AND result_bytes IS NULL AND result_digest IS NULL) OR
        (state = 'terminal' AND result_bytes IS NOT NULL AND
         result_digest ~ '^sha256:[0-9a-f]{64}$')
    )
);
ALTER TABLE heteroci_receipt.batch_receipts OWNER TO heteroci_receipt_writer;
REVOKE ALL ON TABLE heteroci_receipt.batch_receipts FROM PUBLIC;
GRANT USAGE ON SCHEMA heteroci_receipt TO heteroci_receipt_writer;
GRANT SELECT, INSERT, UPDATE ON TABLE heteroci_receipt.batch_receipts TO heteroci_receipt_writer;` + "\n\n" + postgreSQLTerminalReceiptDDL

var (
	// ErrPostgreSQLReceiptStore 表示 PostgreSQL receipt authority 输入、连接或数据无效。
	ErrPostgreSQLReceiptStore = errors.New("heteroci PostgreSQL receipt store is invalid")
	// ErrPostgreSQLReceiptConflict 表示相同 batch/material identity 已绑定不同材料或终态。
	ErrPostgreSQLReceiptConflict = errors.New("heteroci PostgreSQL receipt conflicts with persisted identity")
	// ErrPostgreSQLReceiptCommitUncertain 表示提交确认丢失，必须通过 primary readback 收敛。
	ErrPostgreSQLReceiptCommitUncertain = errors.New("heteroci PostgreSQL receipt commit outcome is uncertain")
)

const postgreSQLReceiptAuthorityQuery = `SELECT
current_user::text,
current_setting('transaction_read_only'),
current_setting('synchronous_commit'),
pg_is_in_recovery(),
COALESCE(inet_server_addr()::text, ''),
to_regclass('heteroci_receipt.batch_receipts') IS NOT NULL,
COALESCE((SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'heteroci_receipt'), ''),
COALESCE((SELECT pg_get_userbyid(c.relowner) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'heteroci_receipt' AND c.relname = 'batch_receipts'), '')`

type postgreSQLReceiptAuthority struct {
	CurrentUser         string
	TransactionReadOnly string
	SynchronousCommit   string
	InRecovery          bool
	ServerAddress       string
	TableExists         bool
	SchemaOwner         string
	TableOwner          string
}

// PostgreSQLReceiptStore 只通过调用方注入的远程 PostgreSQL primary 保存 batch receipt。
type PostgreSQLReceiptStore struct {
	database *sql.DB
}

// NewPostgreSQLReceiptStore 校验注入连接指向远程 read-write primary 和独立 receipt authority。
func NewPostgreSQLReceiptStore(ctx context.Context, database *sql.DB) (*PostgreSQLReceiptStore, error) {
	if ctx == nil || database == nil {
		return nil, ErrPostgreSQLReceiptStore
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrPostgreSQLReceiptStore, err)
	}
	store := &PostgreSQLReceiptStore{database: database}
	tx, err := store.beginTransaction(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: validate authority commit: %w", ErrPostgreSQLReceiptStore, err)
	}
	return store, nil
}

// beginTransaction 创建并验证每次 correctness read/mutation 使用的 SERIALIZABLE authority。
func (store *PostgreSQLReceiptStore) beginTransaction(ctx context.Context) (*sql.Tx, error) {
	if store == nil || store.database == nil || ctx == nil {
		return nil, ErrPostgreSQLReceiptStore
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrPostgreSQLReceiptStore, err)
	}
	tx, err := store.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("%w: begin serializable transaction: %w", ErrPostgreSQLReceiptStore, err)
	}
	if err := validatePostgreSQLReceiptAuthority(ctx, tx); err != nil {
		return nil, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	return tx, nil
}

// validatePostgreSQLReceiptAuthority 拒绝 standby、本机连接、异步提交、错误 role 或 schema owner。
func validatePostgreSQLReceiptAuthority(ctx context.Context, tx *sql.Tx) error {
	var authority postgreSQLReceiptAuthority
	err := tx.QueryRowContext(ctx, postgreSQLReceiptAuthorityQuery).Scan(
		&authority.CurrentUser,
		&authority.TransactionReadOnly,
		&authority.SynchronousCommit,
		&authority.InRecovery,
		&authority.ServerAddress,
		&authority.TableExists,
		&authority.SchemaOwner,
		&authority.TableOwner,
	)
	if err != nil {
		return fmt.Errorf("%w: inspect authority: %w", ErrPostgreSQLReceiptStore, err)
	}
	if err := validatePostgreSQLReceiptEndpoint(authority.ServerAddress); err != nil {
		return err
	}
	return validatePostgreSQLReceiptAuthorityFacts(authority)
}

func validatePostgreSQLReceiptEndpoint(serverAddress string) error {
	address := net.ParseIP(serverAddress)
	if address == nil || address.IsLoopback() || address.IsUnspecified() {
		return fmt.Errorf("%w: endpoint is not remote", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func validatePostgreSQLReceiptAuthorityFacts(authority postgreSQLReceiptAuthority) error {
	if authority.CurrentUser != PostgreSQLReceiptWriterRole || authority.SchemaOwner != PostgreSQLReceiptWriterRole || authority.TableOwner != PostgreSQLReceiptWriterRole {
		return fmt.Errorf("%w: receipt role ownership drifted", ErrPostgreSQLReceiptStore)
	}
	if authority.TransactionReadOnly != "off" || authority.InRecovery {
		return fmt.Errorf("%w: endpoint is not a read-write primary", ErrPostgreSQLReceiptStore)
	}
	if authority.SynchronousCommit != "remote_apply" {
		return fmt.Errorf("%w: synchronous_commit is not remote_apply", ErrPostgreSQLReceiptStore)
	}
	if !authority.TableExists {
		return fmt.Errorf("%w: receipt table is missing", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func rollbackPostgreSQLReceiptTransaction(tx *sql.Tx, cause error) error {
	if err := tx.Rollback(); err != nil {
		return errors.Join(cause, fmt.Errorf("%w: rollback transaction: %w", ErrPostgreSQLReceiptStore, err))
	}
	return cause
}
