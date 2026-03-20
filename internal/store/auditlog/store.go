package auditlog

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]AuditEvent, error) {
	rows, err := s.q.ListAuditEvents(ctx, sqlc.ListAuditEventsParams{
		EventType: filter.EventType,
		Action:    filter.Action,
		Actor:     filter.Actor,
		Keyword:   filter.Keyword,
		Limit:     filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]AuditEvent, len(rows))
	for i, row := range rows {
		result[i] = mapAuditEvent(row)
	}
	return result, nil
}

func (s *store) Insert(ctx context.Context, params InsertParams) error {
	return s.q.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
		EventType: params.EventType,
		Action:    params.Action,
		Result:    params.Result,
		Actor:     params.Actor,
		Target:    params.Target,
		Detail:    params.Detail,
		Level:     params.Level,
		Extra:     params.Extra,
	})
}

func mapAuditEvent(row sqlc.AuditEvent) AuditEvent {
	return AuditEvent{
		ID:        row.ID,
		Ts:        row.Ts,
		EventType: row.EventType,
		Action:    row.Action,
		Result:    row.Result,
		Actor:     row.Actor,
		Target:    row.Target,
		Detail:    row.Detail,
		Level:     row.Level,
		Extra:     row.Extra,
	}
}
