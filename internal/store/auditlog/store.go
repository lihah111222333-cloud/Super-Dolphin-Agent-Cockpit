package auditlog

import (
	"context"
	"encoding/json"
	"time"

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

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func newStoreForTest(q querier) Store { return &store{q: q} }

func (s *store) List(ctx context.Context, filter ListFilter) ([]AuditEvent, error) {
	rows, err := s.q.ListAuditEvents(ctx, sqlc.ListAuditEventsParams{
		EventTypeFilter: filter.EventType,
		ActionFilter:    filter.Action,
		ActorFilter:     filter.Actor,
		Keyword:         filter.Keyword,
		KeywordPattern:  platformdb.LikeContainsFold(filter.Keyword),
		LimitCount:      int64(filter.Limit),
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

func (s *store) Insert(ctx context.Context, params InsertParams) error {
	if err := platformdb.ValidateJSONRaw(params.Extra); err != nil {
		return wrapAuditLogError(err, "insert")
	}
	return wrapAuditLogError(s.q.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
		Ts:        platformdb.Millis(time.Now().UTC()),
		EventType: params.EventType,
		Action:    params.Action,
		Result:    params.Result,
		Actor:     params.Actor,
		Target:    params.Target,
		Detail:    params.Detail,
		Level:     params.Level,
		Extra:     string(params.Extra),
	}), "insert")
}

func mapAuditEvent(row sqlc.ListAuditEventsRow) AuditEvent {
	return AuditEvent{
		ID:        row.ID,
		Ts:        platformdb.TimeFromMillis(row.Ts),
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
