package interaction

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of sqlc.Queries this store depends on.
// NewStore still accepts the concrete *sqlc.Queries for fx wiring.
type querier interface {
	CreateInteraction(ctx context.Context, arg sqlc.CreateInteractionParams) (sqlc.AgentInteraction, error)
	GetInteraction(ctx context.Context, arg sqlc.GetInteractionParams) (sqlc.AgentInteraction, error)
	ListInteractions(ctx context.Context, arg sqlc.ListInteractionsParams) ([]sqlc.AgentInteraction, error)
	ReviewInteraction(ctx context.Context, arg sqlc.ReviewInteractionParams) (sqlc.AgentInteraction, error)
}

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// Create 创建interaction存储。
func (s *store) Create(ctx context.Context, interaction Interaction) (*Interaction, error) {
	row, err := s.q.CreateInteraction(ctx, sqlc.CreateInteractionParams{
		ThreadID:       interaction.ThreadID,
		ParentID:       interaction.ParentID,
		Sender:         interaction.Sender,
		Receiver:       interaction.Receiver,
		MsgType:        interaction.MsgType,
		Status:         interaction.Status,
		RequiresReview: interaction.RequiresReview,
		Column8:        interaction.Payload,
	})
	if err != nil {
		return nil, wrapInteractionError(err, "create")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

// Get 读取interaction存储。
func (s *store) Get(ctx context.Context, id int64) (*Interaction, error) {
	row, err := s.q.GetInteraction(ctx, sqlc.GetInteractionParams{ID: id})
	if err != nil {
		return nil, wrapInteractionError(err, "get")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

// List 列出interaction存储。
func (s *store) List(ctx context.Context, filter ListFilter) ([]Interaction, error) {
	rows, err := s.q.ListInteractions(ctx, sqlc.ListInteractionsParams{Column1: filter.ThreadID, Column2: filter.Keyword, Limit: filter.Limit})
	if err != nil {
		return nil, wrapInteractionError(err, "list")
	}
	interactions := make([]Interaction, 0, len(rows))
	for _, row := range rows {
		interactions = append(interactions, fromSQLC(row))
	}
	return interactions, nil
}

// Review 记录交互评审结果。
func (s *store) Review(ctx context.Context, input ReviewInput) (*Interaction, error) {
	row, err := s.q.ReviewInteraction(ctx, sqlc.ReviewInteractionParams{Status: input.Status, ReviewedBy: input.ReviewedBy, ReviewNote: input.ReviewNote, ID: input.ID})
	if err != nil {
		return nil, wrapInteractionError(err, "review")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func fromSQLC(row sqlc.AgentInteraction) Interaction {
	return Interaction{
		ID:             row.ID,
		ThreadID:       row.ThreadID,
		ParentID:       row.ParentID,
		Sender:         row.Sender,
		Receiver:       row.Receiver,
		MsgType:        row.MsgType,
		Status:         row.Status,
		RequiresReview: row.RequiresReview,
		ReviewedBy:     row.ReviewedBy,
		ReviewNote:     row.ReviewNote,
		ReviewedAt:     row.ReviewedAt,
		Payload:        json.RawMessage(row.Payload),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}

func wrapInteractionError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "interaction")
}
