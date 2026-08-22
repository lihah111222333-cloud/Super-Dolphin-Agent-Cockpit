package heterociowner

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const postgresqlOwnerMutationAttempts = 3

var (
	// ErrPostgreSQLOwnerTopology 表示注入连接不满足 remote primary-only owner 拓扑。
	ErrPostgreSQLOwnerTopology = errors.New("heteroci PostgreSQL owner topology is invalid")
	// ErrPostgreSQLOwnerCommitUncertain 表示远端 primary 无法证明 mutation 的提交结果。
	ErrPostgreSQLOwnerCommitUncertain = errors.New("heteroci PostgreSQL owner commit outcome is uncertain")
)

// PostgreSQLOwner 是调用方注入 remote PostgreSQL primary 连接后的 topology owner adapter。
type PostgreSQLOwner struct {
	db    *sql.DB
	scope heteroci.RoutingOwnerScope
	now   func() time.Time
}

// NewPostgreSQLOwner 校验 remote primary 拓扑与管理员预置的独立 owner authority。
func NewPostgreSQLOwner(ctx context.Context, db *sql.DB, scope heteroci.RoutingOwnerScope) (*PostgreSQLOwner, error) {
	return NewPostgreSQLOwnerWithClock(ctx, db, scope, time.Now)
}

// NewPostgreSQLOwnerWithClock 只绑定调用方已连接的 PostgreSQL，不打开 driver、凭据或本地 fallback。
func NewPostgreSQLOwnerWithClock(ctx context.Context, db *sql.DB, scope heteroci.RoutingOwnerScope, now func() time.Time) (*PostgreSQLOwner, error) {
	if db == nil {
		return nil, fmt.Errorf("heteroci PostgreSQL owner database is nil")
	}
	if now == nil {
		return nil, fmt.Errorf("heteroci PostgreSQL owner clock is nil")
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if err := validatePostgreSQLOwnerServer(ctx, db); err != nil {
		return nil, err
	}
	if err := validatePostgreSQLOwnerObjects(ctx, db); err != nil {
		return nil, err
	}
	return &PostgreSQLOwner{db: db, scope: scope, now: now}, nil
}

type postgresqlOwnerQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// validatePostgreSQLOwnerServer 拒绝 standby、只读事务与非 remote_apply 同步提交。
func validatePostgreSQLOwnerServer(ctx context.Context, queryer postgresqlOwnerQueryRower) error {
	var inRecovery bool
	var transactionReadOnly, synchronousCommit, serverAddress, currentUser string
	err := queryer.QueryRowContext(ctx, `SELECT pg_is_in_recovery(), current_setting('transaction_read_only'), current_setting('synchronous_commit'), inet_server_addr()::text, current_user`).Scan(&inRecovery, &transactionReadOnly, &synchronousCommit, &serverAddress, &currentUser)
	if err != nil {
		return fmt.Errorf("%w: inspect server: %w", ErrPostgreSQLOwnerTopology, err)
	}
	address := net.ParseIP(serverAddress)
	if inRecovery || transactionReadOnly != "off" || synchronousCommit != PostgreSQLOwnerSynchronousCommitRemoteApply || address == nil || address.IsLoopback() || address.IsUnspecified() || currentUser != PostgreSQLOwnerRole {
		return fmt.Errorf("%w: server must be remote read-write primary with remote_apply and the dedicated writer role", ErrPostgreSQLOwnerTopology)
	}
	return nil
}

func validatePostgreSQLOwnerObjects(ctx context.Context, queryer postgresqlOwnerQueryRower) error {
	var schemaOwner, schemaVersion, tableOwnership string
	query := "SELECT pg_get_userbyid(n.nspowner), COALESCE(obj_description(n.oid, 'pg_namespace'), ''), COALESCE(string_agg(c.relname || ':' || pg_get_userbyid(c.relowner), ',' ORDER BY c.relname), '') FROM pg_namespace n LEFT JOIN pg_class c ON c.relnamespace = n.oid AND c.relkind = 'r' WHERE n.nspname = $1 GROUP BY n.oid, n.nspowner"
	if err := queryer.QueryRowContext(ctx, query, PostgreSQLOwnerSchema).Scan(&schemaOwner, &schemaVersion, &tableOwnership); err != nil {
		return fmt.Errorf("%w: inspect owner schema and tables: %w", ErrPostgreSQLOwnerTopology, err)
	}
	if schemaOwner != PostgreSQLOwnerRole || schemaVersion != PostgreSQLOwnerRuntimeSchemaVersion || tableOwnership != expectedPostgreSQLOwnerTableOwnership() {
		return fmt.Errorf("%w: owner table set or ownership drifted", ErrPostgreSQLOwnerTopology)
	}
	return nil
}

func expectedPostgreSQLOwnerTableOwnership() string {
	parts := make([]string, 0, len(postgresqlOwnerTableNames()))
	for _, name := range postgresqlOwnerTableNames() {
		parts = append(parts, name+":"+PostgreSQLOwnerRole)
	}
	return strings.Join(parts, ",")
}

// beginPostgreSQLOwnerTx 创建 SERIALIZABLE transaction 并在 authority read 前取得 scope advisory lock。
func (owner *PostgreSQLOwner) beginPostgreSQLOwnerTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := owner.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin PostgreSQL heteroci owner transaction: %w", err)
	}
	if err := validatePostgreSQLOwnerServer(ctx, tx); err != nil {
		return nil, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := validatePostgreSQLOwnerObjects(ctx, tx); err != nil {
		return nil, rollbackPostgreSQLOwnerTx(tx, err)
	}
	left, right, err := postgresqlOwnerAdvisoryKeys(owner.scope)
	if err != nil {
		return nil, rollbackPostgreSQLOwnerTx(tx, err)
	}
	var locked any
	if err := tx.QueryRowContext(ctx, "SELECT pg_advisory_xact_lock($1, $2)", left, right).Scan(&locked); err != nil {
		return nil, rollbackPostgreSQLOwnerTx(tx, fmt.Errorf("lock PostgreSQL heteroci owner scope: %w", err))
	}
	return tx, nil
}

func postgresqlOwnerAdvisoryKeys(scope heteroci.RoutingOwnerScope) (int32, int32, error) {
	if err := scope.Validate(); err != nil {
		return 0, 0, err
	}
	digest, err := hex.DecodeString(string(scope)[len("sha256:"):])
	if err != nil || len(digest) != 32 {
		return 0, 0, fmt.Errorf("heteroci PostgreSQL owner scope digest is invalid")
	}
	return int32(binary.BigEndian.Uint32(digest[:4])), int32(binary.BigEndian.Uint32(digest[4:8])), nil
}

func rollbackPostgreSQLOwnerTx(tx *sql.Tx, cause error) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return errors.Join(cause, fmt.Errorf("rollback PostgreSQL heteroci owner transaction: %w", err))
	}
	return cause
}

// Acquire 在 scope transaction lock 内创建或接管 owner incarnation。
func (owner *PostgreSQLOwner) Acquire(ctx context.Context) (heteroci.OwnerFence, error) {
	if owner == nil || owner.db == nil {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci PostgreSQL owner is nil")
	}
	ownerID, err := newOwnerID()
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	for range postgresqlOwnerMutationAttempts {
		fence, err := owner.acquireOnce(ctx, ownerID, nowUnixMilli)
		if !isRetryablePostgreSQLError(err) {
			return fence, err
		}
	}
	return heteroci.OwnerFence{}, fmt.Errorf("acquire PostgreSQL heteroci owner after retries")
}

func (owner *PostgreSQLOwner) acquireOnce(ctx context.Context, ownerID heteroci.RoutingOwnerID, nowUnixMilli int64) (heteroci.OwnerFence, error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	candidate, err := upsertPostgreSQLOwnerHead(ctx, tx, owner.scope, ownerID, nowUnixMilli)
	if err != nil {
		return heteroci.OwnerFence{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		if isRetryablePostgreSQLError(err) {
			return heteroci.OwnerFence{}, err
		}
		observed, readErr := owner.CurrentFence(ctx)
		if readErr == nil && observed == candidate {
			return candidate, nil
		}
		return heteroci.OwnerFence{}, errors.Join(ErrPostgreSQLOwnerCommitUncertain, err, readErr)
	}
	return candidate, nil
}

func upsertPostgreSQLOwnerHead(ctx context.Context, tx *sql.Tx, scope heteroci.RoutingOwnerScope, ownerID heteroci.RoutingOwnerID, nowUnixMilli int64) (heteroci.OwnerFence, error) {
	if _, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.owner_epochs SET retired_at_unix_milli = $1 WHERE scope = $2 AND retired_at_unix_milli IS NULL", nowUnixMilli, scope); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("retire PostgreSQL heteroci owner epoch: %w", err)
	}
	query := "INSERT INTO heteroci_owner.owner_heads (scope, owner_id, owner_epoch, revision, journal_digest, projection_digest, updated_at_unix_milli) VALUES ($1, $2, 1, 1, $3, $3, $4) ON CONFLICT (scope) DO UPDATE SET owner_id = EXCLUDED.owner_id, owner_epoch = heteroci_owner.owner_heads.owner_epoch + 1, revision = heteroci_owner.owner_heads.revision + 1, projection_digest = EXCLUDED.projection_digest, updated_at_unix_milli = EXCLUDED.updated_at_unix_milli WHERE heteroci_owner.owner_heads.owner_epoch < 9223372036854775806 AND heteroci_owner.owner_heads.revision < 9223372036854775806 RETURNING owner_epoch, revision"
	var epoch, revision int64
	if err := tx.QueryRowContext(ctx, query, scope, ownerID, zeroDigest, nowUnixMilli).Scan(&epoch, &revision); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("upsert PostgreSQL heteroci owner head: %w", err)
	}
	fence, err := heteroci.NewOwnerFence(scope, ownerID, heteroci.RoutingOwnerEpoch(epoch), heteroci.RoutingOwnerRevision(revision))
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner.owner_epochs (scope, owner_epoch, owner_id, started_at_unix_milli) VALUES ($1, $2, $3, $4)", scope, fence.OwnerEpoch, ownerID, nowUnixMilli); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("insert PostgreSQL heteroci owner epoch: %w", err)
	}
	return fence, nil
}

// CurrentFence 从 remote primary 的 scope-locked SERIALIZABLE snapshot 读取 current fence。
func (owner *PostgreSQLOwner) CurrentFence(ctx context.Context) (heteroci.OwnerFence, error) {
	if owner == nil || owner.db == nil {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci PostgreSQL owner is nil")
	}
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	fence, found, err := readPostgreSQLOwnerHead(ctx, tx, owner.scope, false)
	if err != nil {
		return heteroci.OwnerFence{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if !found {
		return heteroci.OwnerFence{}, rollbackPostgreSQLOwnerTx(tx, fmt.Errorf("heteroci PostgreSQL owner has not been acquired"))
	}
	if err := tx.Commit(); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("commit PostgreSQL heteroci owner read: %w", err)
	}
	return fence, nil
}

func readPostgreSQLOwnerHead(ctx context.Context, queryer postgresqlOwnerQueryRower, scope heteroci.RoutingOwnerScope, forUpdate bool) (heteroci.OwnerFence, bool, error) {
	query := "SELECT owner_id, owner_epoch, revision FROM heteroci_owner.owner_heads WHERE scope = $1"
	if forUpdate {
		query += " FOR UPDATE"
	}
	var ownerID string
	var epoch, revision int64
	err := queryer.QueryRowContext(ctx, query, scope).Scan(&ownerID, &epoch, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return heteroci.OwnerFence{}, false, nil
	}
	if err != nil {
		return heteroci.OwnerFence{}, false, fmt.Errorf("read PostgreSQL heteroci owner head: %w", err)
	}
	fence, err := heteroci.NewOwnerFence(scope, heteroci.RoutingOwnerID(ownerID), heteroci.RoutingOwnerEpoch(epoch), heteroci.RoutingOwnerRevision(revision))
	return fence, true, err
}

func (owner *PostgreSQLOwner) nowUnixMilli() (int64, error) {
	if owner.now == nil {
		return 0, fmt.Errorf("heteroci PostgreSQL owner clock is nil")
	}
	nowUnixMilli := owner.now().UnixMilli()
	if nowUnixMilli < 0 {
		return 0, fmt.Errorf("heteroci PostgreSQL owner clock is before Unix epoch")
	}
	return nowUnixMilli, nil
}

func requireOnePostgreSQLRow(result sql.Result, action string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", action, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s changed %d rows", action, rows)
	}
	return nil
}

func isRetryablePostgreSQLError(err error) bool {
	type sqlStateError interface{ SQLState() string }
	var state sqlStateError
	if !errors.As(err, &state) {
		return false
	}
	return state.SQLState() == "40001" || state.SQLState() == "40P01"
}
