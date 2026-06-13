package auditlog

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of *sqlc.Queries this store calls.
type querier interface {
	ListAuditEvents(ctx context.Context, arg sqlc.ListAuditEventsParams) ([]sqlc.ListAuditEventsRow, error)
	InsertAuditEvent(ctx context.Context, arg sqlc.InsertAuditEventParams) error
}

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func newStoreForTest(q querier) Store { return &store{q: q} }

// List 列出auditlog存储。
func (s *store) List(ctx context.Context, filter ListFilter) ([]AuditEvent, error) {
	rows, err := s.q.ListAuditEvents(ctx, sqlc.ListAuditEventsParams{
		Column1: filter.EventType,
		Column2: filter.Action,
		Column3: filter.Actor,
		Column4: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, wrapAuditLogError(err, "list")
	}
	result := make([]AuditEvent, len(rows))
	for i, row := range rows {
		result[i] = mapAuditEvent(row)
	}
	return result, nil
}

// Insert 插入auditlog存储。
func (s *store) Insert(ctx context.Context, params InsertParams) error {
	return wrapAuditLogError(s.q.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
		EventType: params.EventType,
		Action:    params.Action,
		Result:    params.Result,
		Actor:     params.Actor,
		Target:    params.Target,
		Detail:    params.Detail,
		Level:     params.Level,
		Column8:   params.Extra,
	}), "insert")
}

func mapAuditEvent(row sqlc.ListAuditEventsRow) AuditEvent {
	return AuditEvent{
		Ts:        row.Ts,
		EventType: row.EventType,
		Action:    row.Action,
		Result:    row.Result,
		Actor:     row.Actor,
		Target:    row.Target,
		Detail:    row.Detail,
		Level:     row.Level,
		Extra:     json.RawMessage(row.Extra),
	}
}

func wrapAuditLogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "audit_event")
}
