package heterociowner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const zeroDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// Owner 是一个 scope 内唯一负责 owner fence 生命周期的本地 durable owner。
type Owner struct {
	db    *sql.DB
	scope heteroci.RoutingOwnerScope
	now   func() time.Time
}

// NewOwner 校验 scope 并绑定已打开的独立 owner 数据库。
func NewOwner(ctx context.Context, db *sql.DB, scope heteroci.RoutingOwnerScope) (*Owner, error) {
	return NewOwnerWithClock(ctx, db, scope, time.Now)
}

// NewOwnerWithClock 校验 scope、数据库与显式时钟，并绑定已打开的独立 owner 数据库。
func NewOwnerWithClock(ctx context.Context, db *sql.DB, scope heteroci.RoutingOwnerScope, now func() time.Time) (*Owner, error) {
	if now == nil {
		return nil, fmt.Errorf("heteroci owner clock is nil")
	}
	if db == nil {
		return nil, fmt.Errorf("heteroci owner database is nil")
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if err := configureOwnerDatabase(ctx, db); err != nil {
		return nil, err
	}
	if err := EnsureSchema(ctx, db); err != nil {
		return nil, err
	}
	return &Owner{db: db, scope: scope, now: now}, nil
}

// configureOwnerDatabase 将独立 owner authority 固定为单连接并启用 SQLite 外键。
func configureOwnerDatabase(ctx context.Context, db *sql.DB) error {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable heteroci owner foreign keys: %w", err)
	}
	var enabled int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return fmt.Errorf("verify heteroci owner foreign keys: %w", err)
	}
	if enabled != 1 {
		return fmt.Errorf("heteroci owner foreign keys are disabled")
	}
	return nil
}

// Acquire 创建新的 owner incarnation，并在同一 immediate transaction 内推进 epoch/revision。
func (owner *Owner) Acquire(ctx context.Context) (heteroci.OwnerFence, error) {
	if owner == nil || owner.db == nil {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci owner is nil")
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	ownerID, err := newOwnerID()
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	var fence heteroci.OwnerFence
	err = WithImmediateTx(ctx, owner.db, func(tx *sql.Tx) error {
		current, found, err := readOwnerHead(ctx, tx, owner.scope)
		if err != nil {
			return err
		}
		if !found {
			fence, err = createInitialOwner(ctx, tx, owner.scope, ownerID, nowUnixMilli)
			return err
		}
		fence, err = takeoverOwner(ctx, tx, owner.scope, ownerID, current, nowUnixMilli)
		return err
	})
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	return fence, nil
}

// ErrLocalExecutorBindingRetired 标记 SQLite/local owner 不再拥有 remote executor binding authority。
var ErrLocalExecutorBindingRetired = errors.New("heteroci local executor binding authority is retired")

// BindExecutors 明确阻断 local owner 伪造 remote binding；唯一生产实现是 PostgreSQLDurableOwner。
func (owner *Owner) BindExecutors(context.Context, heteroci.ExecutorBindingMutation) (heteroci.ExecutorBindingMutationResult, error) {
	return heteroci.ExecutorBindingMutationResult{}, ErrLocalExecutorBindingRetired
}

// nowUnixMilli 返回 owner 时钟的正 Unix 毫秒；所有 durable mutation 共享该时钟。
func (owner *Owner) nowUnixMilli() (int64, error) {
	if owner == nil || owner.now == nil {
		return 0, fmt.Errorf("heteroci owner clock is nil")
	}
	now := owner.now()
	value := now.UnixMilli()
	if value <= 0 {
		return 0, fmt.Errorf("heteroci owner clock must be after Unix epoch")
	}
	return value, nil
}

// CurrentFence 读取当前 scope 的 durable head；缺失 head 直接报错。
func (owner *Owner) CurrentFence(ctx context.Context) (heteroci.OwnerFence, error) {
	if owner == nil || owner.db == nil {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci owner is nil")
	}
	current, found, err := readOwnerHead(ctx, owner.db, owner.scope)
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	if !found {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci owner head is missing")
	}
	return current, nil
}

func newOwnerID() (heteroci.RoutingOwnerID, error) {
	raw := make([]byte, heteroci.RoutingOwnerIDHexLength/2)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate heteroci owner identity: %w", err)
	}
	ownerID, err := heteroci.ParseRoutingOwnerID(hex.EncodeToString(raw))
	if err != nil {
		return "", err
	}
	return ownerID, nil
}

// readOwnerHead 读取并验证 scope 当前的 owner fence；无记录返回 found=false。
func readOwnerHead(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope heteroci.RoutingOwnerScope) (heteroci.OwnerFence, bool, error) {
	var ownerID string
	var epoch, revision int64
	row := queryer.QueryRowContext(ctx, "SELECT owner_id, owner_epoch, revision FROM heteroci_owner_heads WHERE scope = ?", scope)
	if err := row.Scan(&ownerID, &epoch, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return heteroci.OwnerFence{}, false, nil
		}
		return heteroci.OwnerFence{}, false, fmt.Errorf("read heteroci owner head: %w", err)
	}
	parsedOwnerID, err := heteroci.ParseRoutingOwnerID(ownerID)
	if err != nil {
		return heteroci.OwnerFence{}, false, err
	}
	parsedEpoch, err := heteroci.NewRoutingOwnerEpoch(epoch)
	if err != nil {
		return heteroci.OwnerFence{}, false, err
	}
	parsedRevision, err := heteroci.NewRoutingOwnerRevision(revision)
	if err != nil {
		return heteroci.OwnerFence{}, false, err
	}
	fence, err := heteroci.NewOwnerFence(scope, parsedOwnerID, parsedEpoch, parsedRevision)
	if err != nil {
		return heteroci.OwnerFence{}, false, err
	}
	return fence, true, nil
}

func createInitialOwner(ctx context.Context, tx *sql.Tx, scope heteroci.RoutingOwnerScope, ownerID heteroci.RoutingOwnerID, nowUnixMilli int64) (heteroci.OwnerFence, error) {
	fence, err := heteroci.NewOwnerFence(scope, ownerID, heteroci.RoutingOwnerEpoch(1), heteroci.RoutingOwnerRevision(1))
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner_heads(scope, owner_id, owner_epoch, revision, journal_digest, projection_digest, updated_at_unix_milli) VALUES (?, ?, ?, ?, ?, ?, ?)", scope, ownerID, 1, 1, zeroDigest, zeroDigest, nowUnixMilli); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("insert initial heteroci owner head: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner_epochs(scope, owner_epoch, owner_id, started_at_unix_milli) VALUES (?, ?, ?, ?)", scope, 1, ownerID, nowUnixMilli); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("insert initial heteroci owner epoch: %w", err)
	}
	return fence, nil
}

// takeoverOwner 在 CAS 成功时推进 epoch 与全局 revision，并登记新 incarnation。
func takeoverOwner(ctx context.Context, tx *sql.Tx, scope heteroci.RoutingOwnerScope, ownerID heteroci.RoutingOwnerID, current heteroci.OwnerFence, nowUnixMilli int64) (heteroci.OwnerFence, error) {
	if current.OwnerEpoch >= heteroci.RoutingOwnerEpoch(math.MaxInt64-1) || current.Revision >= heteroci.RoutingOwnerRevision(math.MaxInt64-1) {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci owner fence counter exhausted")
	}
	nextEpoch := current.OwnerEpoch + 1
	nextRevision := current.Revision + 1
	fence, err := heteroci.NewOwnerFence(scope, ownerID, nextEpoch, nextRevision)
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	retired, err := tx.ExecContext(ctx, "UPDATE heteroci_owner_epochs SET retired_at_unix_milli = ? WHERE scope = ? AND owner_epoch = ? AND owner_id = ? AND retired_at_unix_milli IS NULL", nowUnixMilli, scope, current.OwnerEpoch, current.OwnerID)
	if err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("retire previous heteroci owner epoch: %w", err)
	}
	if err := requireSingleRow(retired, "retire previous heteroci owner epoch"); err != nil {
		return heteroci.OwnerFence{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner_epochs(scope, owner_epoch, owner_id, started_at_unix_milli) VALUES (?, ?, ?, ?)", scope, nextEpoch, ownerID, nowUnixMilli); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("insert heteroci owner epoch: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_owner_heads SET owner_id = ?, owner_epoch = ?, revision = ?, updated_at_unix_milli = ? WHERE scope = ? AND owner_id = ? AND owner_epoch = ? AND revision = ?", ownerID, nextEpoch, nextRevision, nowUnixMilli, scope, current.OwnerID, current.OwnerEpoch, current.Revision)
	if err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("update heteroci owner head: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci owner head compare-and-swap affected %d rows", rows)
	}
	return fence, nil
}
