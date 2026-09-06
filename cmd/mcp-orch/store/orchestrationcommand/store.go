package orchestrationcommand

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store 拥有不可变命令意图与生命周期日志的 SQL 访问，不持有 provider 或运行时调度接口。
type Store struct{ queries *storedb.Queries }

// NewStore 通过已有数据库创建命令意图 owner，缺数据库时立即失败。
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("command intent store database is required")
	}
	return &Store{queries: storedb.New(db)}, nil
}

// Reserve 原子创建或读取同一幂等键；不会派发命令，也不返回“可执行”标记。
// INSERT 的唯一键在数据库内裁决并发；失败不自动重试，调用方可以按原 Key 查询。
func (s *Store) Reserve(ctx context.Context, intent Intent) (Record, error) {
	if err := intent.Validate(); err != nil {
		return Record{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Record{}, err
	}
	row, err := s.queries.CreateOrchestrationCommandIntent(ctx, intentParams(intent))
	if errors.Is(err, sql.ErrNoRows) {
		existing, readErr := s.GetByKey(ctx, Key{Namespace: intent.IdempotencyNamespace, Digest: intent.IdempotencyKeyDigest})
		if readErr != nil {
			return Record{}, readErr
		}
		return matchExistingIntent(intent, existing)
	}
	if err != nil {
		return Record{}, classifyInsertError(err)
	}
	return recordFromRow(row)
}

// Get 严格按原 command ID 读取不可变意图，不补建缺失记录。
func (s *Store) Get(ctx context.Context, commandID string) (Record, error) {
	if err := validateText("command_id", commandID, 128); err != nil {
		return Record{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Record{}, err
	}
	row, err := s.queries.GetOrchestrationCommandIntent(ctx, storedb.GetOrchestrationCommandIntentParams{CommandID: commandID})
	if err != nil {
		return Record{}, classifyReadError(err)
	}
	return recordFromRow(row)
}

// GetTx 在调用方事务内重读不可变意图，供 candidate reconcile 与 submission 做一致性绑定。
func (s *Store) GetTx(ctx context.Context, dbtx storedb.DBTX, commandID string) (Record, error) {
	if s == nil || dbtx == nil {
		return Record{}, errors.New("command intent store database is required")
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return Record{}, err
	}
	if err := validateText("command_id", commandID, 128); err != nil {
		return Record{}, err
	}
	if ctx == nil {
		return Record{}, ErrContextRequired
	}
	row, err := storedb.New(dbtx).GetOrchestrationCommandIntent(ctx, storedb.GetOrchestrationCommandIntentParams{CommandID: commandID})
	if err != nil {
		return Record{}, classifyReadError(err)
	}
	return recordFromRow(row)
}

// GetByKey 用原幂等查询键找回提交结果；不生成第二条意图。
func (s *Store) GetByKey(ctx context.Context, key Key) (Record, error) {
	if err := key.Validate(); err != nil {
		return Record{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Record{}, err
	}
	row, err := s.queries.GetOrchestrationCommandIntentByKey(ctx, storedb.GetOrchestrationCommandIntentByKeyParams{
		IdempotencyNamespace: key.Namespace, IdempotencyKeyDigest: key.Digest,
	})
	if err != nil {
		return Record{}, classifyReadError(err)
	}
	return recordFromRow(row)
}

func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.queries == nil {
		return errors.New("command intent store database is required")
	}
	if ctx == nil {
		return ErrContextRequired
	}
	return ctx.Err()
}

// matchExistingIntent 允许重试者携带新的候选 ID，但其余任何字段变化都冲突。
func matchExistingIntent(candidate Intent, existing Record) (Record, error) {
	candidate.CommandID = existing.Intent.CommandID
	if candidate != existing.Intent {
		return Record{}, ErrIdempotencyConflict
	}
	return existing, nil
}

func classifyReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("read command intent: %w", err)
}

func classifyInsertError(err error) error {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY {
		return errors.Join(ErrCommandIDConflict, err)
	}
	return fmt.Errorf("record command intent: %w", err)
}

// intentParams 显式传递所有意图字段；版本和审计时间由 SQL 的固定插入合同产生。
func intentParams(intent Intent) storedb.CreateOrchestrationCommandIntentParams {
	return storedb.CreateOrchestrationCommandIntentParams{
		CommandID: intent.CommandID, CommandKind: intent.CommandKind,
		CallerPrincipalRef: intent.CallerPrincipalRef, ScopeRef: intent.ScopeRef,
		IdempotencyNamespace: intent.IdempotencyNamespace, IdempotencyKeyDigest: intent.IdempotencyKeyDigest,
		RequestDigest: intent.RequestDigest, DigestKeyID: intent.DigestKeyID,
		RequestContentRef: intent.RequestContentRef, SourceFenceRef: intent.SourceFenceRef, TargetFenceRef: intent.TargetFenceRef,
	}
}

// recordFromRow 验证持久记录版本、引用和审计时间；损坏记录不能变为成功响应。
func recordFromRow(row storedb.OrchestrationCommandIntent) (Record, error) {
	record := Record{Intent: Intent{
		CommandID: row.CommandID, CommandKind: row.CommandKind,
		CallerPrincipalRef: row.CallerPrincipalRef, ScopeRef: row.ScopeRef,
		IdempotencyNamespace: row.IdempotencyNamespace, IdempotencyKeyDigest: row.IdempotencyKeyDigest,
		RequestDigest: row.RequestDigest, DigestKeyID: row.DigestKeyID,
		RequestContentRef: row.RequestContentRef, SourceFenceRef: row.SourceFenceRef, TargetFenceRef: row.TargetFenceRef,
	}, SchemaVersion: row.SchemaVersion, CommandVersion: row.CommandVersion}
	if err := record.Intent.Validate(); err != nil {
		return Record{}, err
	}
	if record.SchemaVersion != 1 || record.CommandVersion != 1 {
		return Record{}, errors.New("command intent record version is unsupported")
	}
	recordedAt, err := time.Parse(time.RFC3339Nano, row.RecordedAt)
	if err != nil || recordedAt.IsZero() {
		return Record{}, errors.New("command intent record timestamp is invalid")
	}
	record.RecordedAt = recordedAt
	return record, nil
}
