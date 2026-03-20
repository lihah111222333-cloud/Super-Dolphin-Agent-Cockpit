package topologyapproval

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Create(ctx context.Context, approval TopologyApproval) (*TopologyApproval, error) {
	row, err := s.q.CreateTopologyApproval(ctx, sqlc.CreateTopologyApprovalParams{
		ID:                   approval.ID,
		RequestedBy:          approval.RequestedBy,
		Reason:               approval.Reason,
		CreatedAt:            approval.CreatedAt,
		ExpireAt:             approval.ExpireAt,
		ArchHash:             approval.ArchHash,
		ProposedArchitecture: approval.ProposedArchitecture,
	})
	if err != nil {
		return nil, err
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) Approve(ctx context.Context, reviewer, id string) (int64, error) {
	return s.q.ApproveTopologyApproval(ctx, reviewer, id)
}

func (s *store) Reject(ctx context.Context, reviewer, id string) (int64, error) {
	return s.q.RejectTopologyApproval(ctx, reviewer, id)
}

func (s *store) ListPending(ctx context.Context) ([]TopologyApproval, error) {
	rows, err := s.q.ListPendingTopologyApprovals(ctx)
	if err != nil {
		return nil, err
	}
	approvals := make([]TopologyApproval, 0, len(rows))
	for _, row := range rows {
		approvals = append(approvals, fromSQLC(row))
	}
	return approvals, nil
}

func fromSQLC(row sqlc.TopologyApproval) TopologyApproval {
	return TopologyApproval{
		ID:                   row.ID,
		Status:               row.Status,
		RequestedBy:          row.RequestedBy,
		Reason:               row.Reason,
		CreatedAt:            row.CreatedAt,
		ExpireAt:             row.ExpireAt,
		ReviewedAt:           row.ReviewedAt,
		Reviewer:             row.Reviewer,
		ReviewNote:           row.ReviewNote,
		ArchHash:             row.ArchHash,
		ProposedArchitecture: row.ProposedArchitecture,
	}
}
