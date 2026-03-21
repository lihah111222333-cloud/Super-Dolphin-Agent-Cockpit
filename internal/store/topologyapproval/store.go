package topologyapproval

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
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
		return nil, wrapTopologyApprovalError(err, "create")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) Approve(ctx context.Context, reviewer, id string) (int64, error) {
	count, err := s.q.ApproveTopologyApproval(ctx, reviewer, id)
	if err != nil {
		return 0, wrapTopologyApprovalError(err, "approve")
	}
	return count, nil
}

func (s *store) Reject(ctx context.Context, reviewer, id string) (int64, error) {
	count, err := s.q.RejectTopologyApproval(ctx, reviewer, id)
	if err != nil {
		return 0, wrapTopologyApprovalError(err, "reject")
	}
	return count, nil
}

func (s *store) ListPending(ctx context.Context) ([]TopologyApproval, error) {
	rows, err := s.q.ListPendingTopologyApprovals(ctx)
	if err != nil {
		return nil, wrapTopologyApprovalError(err, "list_pending")
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

func wrapTopologyApprovalError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "topology_approval")
}
