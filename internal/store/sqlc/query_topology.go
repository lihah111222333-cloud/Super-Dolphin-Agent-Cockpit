package sqlc

import "context"

const (
	createTopologyApprovalSQL       = `INSERT INTO topology_approvals ( id, status, requested_by, reason, created_at, expire_at, arch_hash, proposed_architecture ) VALUES ($1, 'pending', $2, $3, $4, $5, $6, $7::jsonb) RETURNING id, status, requested_by, reason, created_at, expire_at, reviewed_at, reviewer, review_note, arch_hash, proposed_architecture;`
	approveTopologyApprovalSQL      = `UPDATE topology_approvals SET status = 'approved', reviewer = $1, reviewed_at = NOW() WHERE id = $2 AND status = 'pending';`
	rejectTopologyApprovalSQL       = `UPDATE topology_approvals SET status = 'rejected', reviewer = $1, reviewed_at = NOW() WHERE id = $2 AND status = 'pending';`
	listPendingTopologyApprovalsSQL = `SELECT id, status, requested_by, reason, created_at, expire_at, reviewed_at, reviewer, review_note, arch_hash, proposed_architecture FROM topology_approvals WHERE status = 'pending' AND expire_at > NOW() ORDER BY created_at DESC;`
)

func scanTopologyApproval(row rowScanner) (TopologyApproval, error) {
	var item TopologyApproval
	err := row.Scan(&item.ID, &item.Status, &item.RequestedBy, &item.Reason, &item.CreatedAt, &item.ExpireAt, &item.ReviewedAt, &item.Reviewer, &item.ReviewNote, &item.ArchHash, &item.ProposedArchitecture)
	return item, err
}

func (q *Queries) CreateTopologyApproval(ctx context.Context, arg CreateTopologyApprovalParams) (TopologyApproval, error) {
	return queryOne(ctx, q, createTopologyApprovalSQL, scanTopologyApproval, arg.ID, arg.RequestedBy, arg.Reason, arg.CreatedAt, arg.ExpireAt, arg.ArchHash, arg.ProposedArchitecture)
}

func (q *Queries) ApproveTopologyApproval(ctx context.Context, reviewer, id string) (int64, error) {
	return q.execRows(ctx, approveTopologyApprovalSQL, reviewer, id)
}

func (q *Queries) RejectTopologyApproval(ctx context.Context, reviewer, id string) (int64, error) {
	return q.execRows(ctx, rejectTopologyApprovalSQL, reviewer, id)
}

func (q *Queries) ListPendingTopologyApprovals(ctx context.Context) ([]TopologyApproval, error) {
	return queryMany(ctx, q, listPendingTopologyApprovalsSQL, scanTopologyApproval)
}
