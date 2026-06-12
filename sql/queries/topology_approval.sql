-- Legacy V2 store SQL used proposal_hash/proposal_json columns.
-- These queries are aligned to the V2 exported public schema used for the baseline.

-- name: CreateTopologyApproval :one
INSERT INTO topology_approvals (
    id, status, requested_by, reason, created_at, expire_at, arch_hash, proposed_architecture
) VALUES (?, 'pending', ?, ?, ?, ?, ?, ?)
RETURNING id, status, requested_by, reason, created_at, expire_at, reviewed_at, reviewer, review_note, arch_hash, proposed_architecture;

-- name: ApproveTopologyApproval :execrows
UPDATE topology_approvals
SET status = 'approved',
    reviewer = ?,
    reviewed_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = ? AND status = 'pending';

-- name: RejectTopologyApproval :execrows
UPDATE topology_approvals
SET status = 'rejected',
    reviewer = ?,
    reviewed_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = ? AND status = 'pending';

-- name: ListPendingTopologyApprovals :many
SELECT id, status, requested_by, reason, created_at, expire_at, reviewed_at, reviewer, review_note, arch_hash, proposed_architecture
FROM topology_approvals
WHERE status = 'pending' AND expire_at > (CAST(strftime('%s','now') AS INTEGER) * 1000)
ORDER BY created_at DESC;
