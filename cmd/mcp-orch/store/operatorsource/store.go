// Package operatorsource owns append-only operator provenance, not authorization.
package operatorsource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

var (
	ErrInvalid  = errors.New("invalid operator source")
	ErrNotFound = errors.New("operator source not found")
	ErrConflict = errors.New("operator source conflict")
	ErrRevoked  = errors.New("operator source revoked")
)

// Key 指定来源代际所属的认证主体和作用域。
type key struct{ PrincipalRef, ScopeRef string }

// ActivateRequest 将明确的认证启动上下文与预期历史代际绑定。
type ActivateRequest struct {
	PrincipalRef, ScopeRef, AuthContext string
	ExpectedGeneration                  int64
}

// Selector 精确定位一个来源代际；不是授权凭据。
type Selector struct {
	PrincipalRef, ScopeRef, AuthContext, SourceID, SourceFenceRef string
	Generation                                                    int64
}

// Source 保存来源全量持久快照，不代表CONTENT或执行许可。
type Source struct {
	SourceID, SourceFenceRef, PrincipalRef, ScopeRef, AuthContext string
	Generation                                                    int64
	State                                                         string
	CreatedAt                                                     time.Time
	RevokedAt                                                     *time.Time
	SchemaVersion                                                 int64
}

// Store 独占来源代际事务，不接触transport或agent身份。
type Store struct {
	db      *sql.DB
	queries *sqlc.Queries
}

// NewStore 要求显式数据库依赖。
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database required", ErrInvalid)
	}
	return &Store{db: db, queries: sqlc.New(db)}, nil
}

// GetLatest 返回最新代际，包括已撤销的最新快照。
func (s *Store) GetLatest(ctx context.Context) (Source, error) {
	if err := s.ready(ctx); err != nil {
		return Source{}, err
	}
	return latest(ctx, s.queries)
}
func latest(ctx context.Context, q *sqlc.Queries) (Source, error) {
	row, err := q.GetLatestOperatorSource(ctx)
	return decodeResult(row, err)
}

// Activate 在IMMEDIATE事务内CAS撤销旧代并创建唯一新代。
func (s *Store) Activate(ctx context.Context, r ActivateRequest) (Source, error) {
	if err := s.ready(ctx); err != nil {
		return Source{}, err
	}
	if err := validateActivation(r); err != nil {
		return Source{}, err
	}
	var out Source
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *sqlc.Queries, _ sqlc.DBTX) error {
		var err error
		out, err = activate(ctx, q, r)
		return err
	})
	if err != nil {
		return Source{}, err
	}
	return out, nil
}

// activate 校验来源身份与持久化状态，拒绝无效或不匹配的数据。
func activate(ctx context.Context, q *sqlc.Queries, r ActivateRequest) (Source, error) {
	row, err := q.GetOperatorSourceBoot(ctx, sqlc.GetOperatorSourceBootParams{PrincipalRef: r.PrincipalRef, ScopeRef: r.ScopeRef, AuthContext: r.AuthContext})
	if err == nil {
		return replay(row, r)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Source{}, err
	}
	old, err := latest(ctx, q)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Source{}, err
	}
	if old.Generation != r.ExpectedGeneration {
		return Source{}, ErrConflict
	}
	if old.State == "active" {
		if _, err = revokeRow(ctx, q, old); err != nil {
			return Source{}, err
		}
	}
	generation := r.ExpectedGeneration + 1
	id, fence, err := identity(r.PrincipalRef, r.ScopeRef, r.AuthContext, generation)
	if err != nil {
		return Source{}, err
	}
	row, err = q.InsertOperatorSource(ctx, sqlc.InsertOperatorSourceParams{SourceID: id, SourceFenceRef: fence, PrincipalRef: r.PrincipalRef, ScopeRef: r.ScopeRef, AuthContext: r.AuthContext, Generation: generation, CreatedAt: time.Now().UTC().Format(timestampLayout)})
	return decodeResult(row, err)
}
func replay(row sqlc.OrchestrationOperatorSource, r ActivateRequest) (Source, error) {
	source, err := decode(row)
	if err != nil {
		return Source{}, err
	}
	if source.State == "revoked" {
		return Source{}, ErrRevoked
	}
	if r.ExpectedGeneration != source.Generation-1 {
		return Source{}, ErrConflict
	}
	return source, nil
}

// ResolveCurrent 只接受最新且active的逐字段精确selector。
func (s *Store) ResolveCurrent(ctx context.Context, sel Selector) (Source, error) {
	if err := s.ready(ctx); err != nil {
		return Source{}, err
	}
	if err := validateSelector(sel); err != nil {
		return Source{}, err
	}
	source, err := latest(ctx, s.queries)
	if err != nil {
		return Source{}, err
	}
	if !matches(source, sel) {
		return Source{}, ErrConflict
	}
	if source.State != "active" {
		return Source{}, ErrRevoked
	}
	return source, nil
}

// Revoke 精确撤销指定代际；旧代close不会撤销新代。
func (s *Store) Revoke(ctx context.Context, sel Selector) (Source, error) {
	if err := s.ready(ctx); err != nil {
		return Source{}, err
	}
	if err := validateSelector(sel); err != nil {
		return Source{}, err
	}
	var out Source
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *sqlc.Queries, _ sqlc.DBTX) error {
		row, err := q.GetOperatorSourceID(ctx, sqlc.GetOperatorSourceIDParams{SourceID: sel.SourceID})
		source, err := decodeResult(row, err)
		if err != nil {
			return err
		}
		if !matches(source, sel) {
			return ErrConflict
		}
		if source.State == "revoked" {
			out = source
			return nil
		}
		out, err = revokeRow(ctx, q, source)
		return err
	})
	if err != nil {
		return Source{}, err
	}
	return out, nil
}
func revokeRow(ctx context.Context, q *sqlc.Queries, s Source) (Source, error) {
	now := time.Now().UTC()
	if now.Before(s.CreatedAt) {
		return Source{}, fmt.Errorf("%w: clock precedes creation", ErrInvalid)
	}
	stamp := now.Format(timestampLayout)
	row, err := q.RevokeOperatorSource(ctx, sqlc.RevokeOperatorSourceParams{RevokedAt: &stamp, SourceID: s.SourceID, Generation: s.Generation})
	return decodeResult(row, err)
}
func validateActivation(r ActivateRequest) error {
	if err := validateKey(key{r.PrincipalRef, r.ScopeRef}); err != nil {
		return err
	}
	if !canonical(r.AuthContext, 512) || r.ExpectedGeneration < 0 || r.ExpectedGeneration == math.MaxInt64 {
		return ErrInvalid
	}
	return nil
}
