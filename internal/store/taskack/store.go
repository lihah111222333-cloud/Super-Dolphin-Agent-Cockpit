package taskack

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
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
		Progress:      ack.Progress,
		AckMessage:    ack.AckMessage,
		ResultSummary: ack.ResultSummary,
		Metadata:      ack.Metadata,
		DueAt:         ack.DueAt,
	})
	if err != nil {
		return nil, wrapTaskAckError(err, "upsert")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]TaskAck, error) {
	rows, err := s.q.ListTaskAcks(ctx, sqlc.ListTaskAcksParams{
		Status:     filter.Status,
		Priority:   filter.Priority,
		AssignedTo: filter.AssignedTo,
		Keyword:    filter.Keyword,
		Limit:      filter.Limit,
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
		Metadata:      row.Metadata,
		DueAt:         row.DueAt,
		AckedAt:       row.AckedAt,
		StartedAt:     row.StartedAt,
		FinishedAt:    row.FinishedAt,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

func wrapTaskAckError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "task_ack")
}
