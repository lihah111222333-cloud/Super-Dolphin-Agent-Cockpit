package topologyapproval

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of sqlc.Queries this store depends on.
type querier interface {
	CreateTopologyApproval(ctx context.Context, arg sqlc.CreateTopologyApprovalParams) (sqlc.TopologyApproval, error)
	ApproveTopologyApproval(ctx context.Context, arg sqlc.ApproveTopologyApprovalParams) (int64, error)
	RejectTopologyApproval(ctx context.Context, arg sqlc.RejectTopologyApprovalParams) (int64, error)
	ListPendingTopologyApprovals(ctx context.Context) ([]sqlc.TopologyApproval, error)
}

type store struct {
	q querier
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Create(ctx context.Context, approval TopologyApproval) (*TopologyApproval, error) {
	row, err := s.q.CreateTopologyApproval(ctx, sqlc.CreateTopologyApprovalParams{
		ID:                   approval.ID,
		RequestedBy:          approval.RequestedBy,
		Reason:               approval.Reason,
		CreatedAt:            platformdb.Millis(approval.CreatedAt),
		ExpireAt:             platformdb.Millis(approval.ExpireAt),
		ArchHash:             approval.ArchHash,
		ProposedArchitecture: string(approval.ProposedArchitecture),
	})
	if err != nil {
		return nil, wrapTopologyApprovalError(err, "create")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) Approve(ctx context.Context, reviewer, id string) (int64, error) {
	count, err := s.q.ApproveTopologyApproval(ctx, sqlc.ApproveTopologyApprovalParams{Reviewer: reviewer, ID: id})
	if err != nil {
		return 0, wrapTopologyApprovalError(err, "approve")
	}
	return count, nil
}

func (s *store) Reject(ctx context.Context, reviewer, id string) (int64, error) {
	count, err := s.q.RejectTopologyApproval(ctx, sqlc.RejectTopologyApprovalParams{Reviewer: reviewer, ID: id})
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

func reviewedAtPtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := platformdb.TimeFromMillis(*ms)
	return &t
}

func fromSQLC(row sqlc.TopologyApproval) TopologyApproval {
	return TopologyApproval{
		ID:                   row.ID,
		Status:               row.Status,
		RequestedBy:          row.RequestedBy,
		Reason:               row.Reason,
		CreatedAt:            platformdb.TimeFromMillis(row.CreatedAt),
		ExpireAt:             platformdb.TimeFromMillis(row.ExpireAt),
		ReviewedAt:           reviewedAtPtr(row.ReviewedAt),
		Reviewer:             row.Reviewer,
		ReviewNote:           row.ReviewNote,
		ArchHash:             row.ArchHash,
		ProposedArchitecture: json.RawMessage(row.ProposedArchitecture),
	}
}

func wrapTopologyApprovalError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "topology_approval")
}
