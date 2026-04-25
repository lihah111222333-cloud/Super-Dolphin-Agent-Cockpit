-- Queries for skill_candidates. See migration 0064 for the table layout
-- and status-machine contract. State-changing UPDATEs use RETURNING *
-- with a status-guard in the WHERE clause: a non-matching state yields
-- pgx.ErrNoRows, which the store layer maps to platformdb.ErrConflict.

-- name: InsertSkillCandidate :one
INSERT INTO skill_candidates (
    scope, slug, content_hash, repo_fingerprint, skill_md, redacted_sample
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetSkillCandidateByID :one
SELECT * FROM skill_candidates WHERE id = $1;

-- name: ListPendingSkillCandidates :many
SELECT * FROM skill_candidates
WHERE status = 'pending_review'
ORDER BY created_at ASC, id ASC
LIMIT $1 OFFSET $2;

-- name: ApproveSkillCandidate :one
UPDATE skill_candidates
SET status      = 'approved',
    approved_by = $2,
    approved_at = $3,
    reason      = $4
WHERE id = $1 AND status = 'pending_review'
RETURNING *;

-- name: RejectSkillCandidate :one
UPDATE skill_candidates
SET status = 'rejected',
    reason = $2
WHERE id = $1 AND status = 'pending_review'
RETURNING *;

-- name: MarkSkillCandidatePromoted :one
UPDATE skill_candidates
SET status = 'promoted'
WHERE id = $1 AND status = 'approved'
RETURNING *;

-- name: LookupSkillCandidateApproval :one
SELECT * FROM skill_candidates
WHERE scope = $1
  AND slug = $2
  AND content_hash = $3
  AND repo_fingerprint = $4
LIMIT 1;
