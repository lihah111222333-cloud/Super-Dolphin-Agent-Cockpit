package interaction

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Create(ctx context.Context, interaction Interaction) (*Interaction, error) {
	row, err := s.q.CreateInteraction(ctx, sqlc.CreateInteractionParams{
		ThreadID:       interaction.ThreadID,
		ParentID:       interaction.ParentID,
		Sender:         interaction.Sender,
		Receiver:       interaction.Receiver,
		MsgType:        interaction.MsgType,
		Status:         interaction.Status,
		RequiresReview: interaction.RequiresReview,
		Payload:        interaction.Payload,
	})
	if err != nil {
		return nil, err
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) Get(ctx context.Context, id int64) (*Interaction, error) {
	row, err := s.q.GetInteraction(ctx, id)
	if err != nil {
		return nil, err
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]Interaction, error) {
	rows, err := s.q.ListInteractions(ctx, sqlc.ListInteractionsParams{ThreadID: filter.ThreadID, Keyword: filter.Keyword, Limit: filter.Limit})
	if err != nil {
		return nil, err
	}
	interactions := make([]Interaction, 0, len(rows))
	for _, row := range rows {
		interactions = append(interactions, fromSQLC(row))
	}
	return interactions, nil
}

func (s *store) Review(ctx context.Context, input ReviewInput) (*Interaction, error) {
	row, err := s.q.ReviewInteraction(ctx, sqlc.ReviewInteractionParams{Status: input.Status, ReviewedBy: input.ReviewedBy, ReviewNote: input.ReviewNote, ID: input.ID})
	if err != nil {
		return nil, err
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
		Payload:        row.Payload,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}
