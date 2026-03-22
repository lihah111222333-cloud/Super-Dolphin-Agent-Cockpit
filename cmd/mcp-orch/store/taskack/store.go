package taskack

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Upsert(ctx context.Context, ack TaskAck) (*TaskAck, error) {
	row, err := s.q.UpsertTaskAck(ctx, sqlc.UpsertTaskAckParams{
		AckKey:        ack.AckKey,
		Title:         ack.Title,
		Description:   ack.Description,
		AssignedTo:    ack.AssignedTo,
		RequestedBy:   ack.RequestedBy,
		Priority:      ack.Priority,
		Status:        ack.Status,
		Column8:       ack.Progress,
		AckMessage:    ack.AckMessage,
		ResultSummary: ack.ResultSummary,
		Column11:      ack.Metadata,
		Column12:      toTimestamptz(ack.DueAt),
	})
	if err != nil {
		return nil, wrapTaskAckError(err, "upsert")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]TaskAck, error) {
	rows, err := s.q.ListTaskAcks(ctx, sqlc.ListTaskAcksParams{
		Column1: filter.Status,
		Column2: filter.Priority,
		Column3: filter.AssignedTo,
		Column4: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, wrapTaskAckError(err, "list")
	}
	acks := make([]TaskAck, 0, len(rows))
	for _, row := range rows {
		acks = append(acks, fromSQLC(row))
	}
	return acks, nil
}

func fromSQLC(row sqlc.TaskAck) TaskAck {
	return TaskAck{
		ID:            row.ID,
		AckKey:        row.AckKey,
		Title:         row.Title,
		Description:   row.Description,
		AssignedTo:    row.AssignedTo,
		RequestedBy:   row.RequestedBy,
		Priority:      row.Priority,
		Status:        row.Status,
		Progress:      row.Progress,
		AckMessage:    row.AckMessage,
		ResultSummary: row.ResultSummary,
		Metadata:      json.RawMessage(row.Metadata),
		DueAt:         sqlc.TimePtr(row.DueAt),
		AckedAt:       sqlc.TimePtr(row.AckedAt),
		StartedAt:     sqlc.TimePtr(row.StartedAt),
		FinishedAt:    sqlc.TimePtr(row.FinishedAt),
		CreatedAt:     sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:     sqlc.TimeValue(row.UpdatedAt),
	}
}

func toTimestamptz(ts *time.Time) sqlc.Timestamptz { return sqlc.TimeValuePtr(ts) }

func wrapTaskAckError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "task_ack")
}
