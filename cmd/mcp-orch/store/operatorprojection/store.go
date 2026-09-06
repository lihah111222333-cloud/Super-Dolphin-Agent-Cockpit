package operatorprojection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	"strings"
)

var (
	ErrInvalid  = errors.New("invalid operator projection")
	ErrNotFound = errors.New("operator projection not found")
)

// Store 持有operator object/ref projection的SQLite owner。
type Store struct {
	db *sql.DB
	q  *sqlc.Queries
}

// NewStore 构造显式operator projection数据库owner。
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database required", ErrInvalid)
	}
	return &Store{db, sqlc.New(db)}, nil
}

// CreatePrepared 原子登记已密封对象的prepared元数据，不执行加密。
func (s *Store) CreatePrepared(ctx context.Context, p sqlc.CreatePreparedObjectParams) (sqlc.OrchestrationOperatorContentObject, error) {
	if strings.TrimSpace(p.ObjectID) == "" || p.SizeBytes <= 0 || p.CreatedAt == "" || p.UpdatedAt == "" {
		return sqlc.OrchestrationOperatorContentObject{}, ErrInvalid
	}
	var out sqlc.OrchestrationOperatorContentObject
	e := sqlctx.WithImmediateTx(ctx, s.db, s.q, func(q *sqlc.Queries, _ sqlc.DBTX) error {
		var err error
		out, err = q.CreatePreparedObject(ctx, p)
		return err
	})
	return out, e
}

// Promote 原子推进prepared object到active，并要求调用方提供更新时间。
func (s *Store) Promote(ctx context.Context, p sqlc.PromoteObjectParams) (sqlc.OrchestrationOperatorContentObject, error) {
	if strings.TrimSpace(p.ObjectID) == "" || p.UpdatedAt == "" {
		return sqlc.OrchestrationOperatorContentObject{}, ErrInvalid
	}
	var out sqlc.OrchestrationOperatorContentObject
	e := sqlctx.WithImmediateTx(ctx, s.db, s.q, func(q *sqlc.Queries, _ sqlc.DBTX) error {
		var err error
		out, err = q.PromoteObject(ctx, p)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return out, e
}

// PromoteAndCreateRef 在同一IMMEDIATE事务中推进对象并创建active ref。
func (s *Store) PromoteAndCreateRef(ctx context.Context, objectID string, ref sqlc.CreateActiveRefParams, updatedAt string) (sqlc.OrchestrationOperatorContentRef, error) {
	if strings.TrimSpace(objectID) == "" || updatedAt == "" || strings.TrimSpace(ref.ReferenceID) == "" || strings.TrimSpace(ref.CanonicalRef) == "" || strings.TrimSpace(ref.ObjectID) == "" || ref.ObjectID != objectID || ref.CreatedAt == "" {
		return sqlc.OrchestrationOperatorContentRef{}, ErrInvalid
	}
	var out sqlc.OrchestrationOperatorContentRef
	err := sqlctx.WithImmediateTx(ctx, s.db, s.q, func(q *sqlc.Queries, _ sqlc.DBTX) error {
		if _, err := q.PromoteObject(ctx, sqlc.PromoteObjectParams{ObjectID: objectID, UpdatedAt: updatedAt}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var err error
		out, err = q.CreateActiveRef(ctx, ref)
		return err
	})
	return out, err
}

// CreateActiveRef 原子创建active ref，source/current校验由数据库触发器执行。
func (s *Store) CreateActiveRef(ctx context.Context, p sqlc.CreateActiveRefParams) (sqlc.OrchestrationOperatorContentRef, error) {
	if strings.TrimSpace(p.CanonicalRef) == "" || strings.TrimSpace(p.ObjectID) == "" || p.CreatedAt == "" {
		return sqlc.OrchestrationOperatorContentRef{}, ErrInvalid
	}
	var out sqlc.OrchestrationOperatorContentRef
	e := sqlctx.WithImmediateTx(ctx, s.db, s.q, func(q *sqlc.Queries, _ sqlc.DBTX) error {
		var err error
		out, err = q.CreateActiveRef(ctx, p)
		return err
	})
	return out, e
}

// ResolveActiveRef 读取active ref；完整source/current读侧仍需C1c补强。
func (s *Store) ResolveActiveRef(ctx context.Context, canonical string) (sqlc.OrchestrationOperatorContentRef, error) {
	if strings.TrimSpace(canonical) == "" {
		return sqlc.OrchestrationOperatorContentRef{}, ErrInvalid
	}
	r, e := s.q.ResolveActiveRef(ctx, sqlc.ResolveActiveRefParams{CanonicalRef: canonical})
	if errors.Is(e, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, e
}
