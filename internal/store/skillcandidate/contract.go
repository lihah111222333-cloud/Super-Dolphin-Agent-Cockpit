// Package skillcandidate persists self-learned SKILL candidates that
// the P0b extractor (Step 4) writes and the review gate (Step 5) reads,
// approves, rejects, and promotes. Migration 0064 owns the schema and
// status-machine contract; this package is the thin domain facade so
// callers never touch sqlc rows directly.
package skillcandidate

import (
	"context"
	"time"
)

// Status constants mirror the migration 0064 CHECK domain.
const (
	StatusPendingReview = "pending_review"
	StatusApproved      = "approved"
	StatusRejected      = "rejected"
	StatusPromoted      = "promoted"
	StatusSuperseded    = "superseded"
)

// Scope constants mirror the migration 0064 CHECK domain.
const (
	ScopeProject = "project"
	ScopeSystem  = "system"
)

// Candidate is the domain DTO. ApprovedAt is *time.Time so callers can
// distinguish "never approved" from "approved at the zero time".
type Candidate struct {
	ID              int64
	Scope           string
	Slug            string
	ContentHash     string
	RepoFingerprint string
	Status          string
	SkillMD         string
	ApprovedBy      string
	ApprovedAt      *time.Time
	Reason          string
	RedactedSample  string
	CreatedAt       time.Time
}

// InsertParams drives Store.Insert. The extractor (Step 4) is the only
// production caller; SkillMD must be the full SKILL.md text so the
// review gate (Step 5) can hand it to the P0a CreateSkill flow on
// approve.
type InsertParams struct {
	Scope           string
	Slug            string
	ContentHash     string
	RepoFingerprint string
	SkillMD         string
	RedactedSample  string
}

// Store is the domain facade exported from this package.
//
// LookupApproval returns (nil, nil) on cache miss. All other methods
// return a non-nil error when the requested row is absent or the
// status guard rejects the transition.
type Store interface {
	Insert(ctx context.Context, p InsertParams) (Candidate, error)
	GetByID(ctx context.Context, id int64) (Candidate, error)
	ListPending(ctx context.Context, limit, offset int32) ([]Candidate, error)
	Approve(ctx context.Context, id int64, approvedBy, reason string, approvedAt time.Time) (Candidate, error)
	Reject(ctx context.Context, id int64, reason string) (Candidate, error)
	MarkPromoted(ctx context.Context, id int64) (Candidate, error)
	LookupApproval(ctx context.Context, scope, slug, contentHash, repoFingerprint string) (*Candidate, error)
}
