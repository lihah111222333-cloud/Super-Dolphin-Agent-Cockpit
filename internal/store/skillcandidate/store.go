package skillcandidate

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

const (
	entity       = "skill_candidate"
	defaultLimit = int32(50)
)

// querier is the narrow subset of *sqlc.Queries the store consumes.
// Splitting it out keeps unit tests off the live pgx pool.
type querier interface {
	InsertSkillCandidate(ctx context.Context, arg sqlc.InsertSkillCandidateParams) (sqlc.SkillCandidate, error)
	GetSkillCandidateByID(ctx context.Context, arg sqlc.GetSkillCandidateByIDParams) (sqlc.SkillCandidate, error)
	ListPendingSkillCandidates(ctx context.Context, arg sqlc.ListPendingSkillCandidatesParams) ([]sqlc.SkillCandidate, error)
	MarkSkillCandidatesSuperseded(ctx context.Context, arg sqlc.MarkSkillCandidatesSupersededParams) (int64, error)
	ApproveSkillCandidate(ctx context.Context, arg sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error)
	RejectSkillCandidate(ctx context.Context, arg sqlc.RejectSkillCandidateParams) (sqlc.SkillCandidate, error)
	MarkSkillCandidatePromoted(ctx context.Context, arg sqlc.MarkSkillCandidatePromotedParams) (sqlc.SkillCandidate, error)
	LookupSkillCandidateApproval(ctx context.Context, arg sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error)
}

type store struct{ q querier }

// NewStore wires the production sqlc-backed Store. Pool injection is
// handled at the fx layer via *sqlc.Queries.
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// newStoreForTest exposes a constructor that accepts the package-private
// querier interface so tests can plug in a stub without a live pool.
func newStoreForTest(q querier) Store { return &store{q: q} }

func wrap(err error, op string) error {
	return platformdb.WrapStoreError(err, op, entity)
}

// fromTS turns a pgtype.Timestamptz into a *time.Time (nil when
// !Valid). Used for the nullable approved_at column.
func fromTS(p pgtype.Timestamptz) *time.Time {
	if !p.Valid {
		return nil
	}
	t := p.Time
	return &t
}

// fromTSNonNull is the non-null companion (created_at). A row with
// !Valid here means the DB returned NULL where NOT NULL is declared,
// which is genuinely a corrupt row; we surface it as the zero time.
func fromTSNonNull(p pgtype.Timestamptz) time.Time {
	if !p.Valid {
		return time.Time{}
	}
	return p.Time
}

func toTS(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func fromRow(r sqlc.SkillCandidate) Candidate {
	return Candidate{
		ID:              r.ID,
		Scope:           r.Scope,
		Slug:            r.Slug,
		ContentHash:     r.ContentHash,
		RepoFingerprint: r.RepoFingerprint,
		Status:          r.Status,
		SkillMD:         r.SkillMd,
		ApprovedBy:      r.ApprovedBy,
		ApprovedAt:      fromTS(r.ApprovedAt),
		Reason:          r.Reason,
		RedactedSample:  r.RedactedSample,
		CreatedAt:       fromTSNonNull(r.CreatedAt),
	}
}

func (s *store) transitionNoRowsError(ctx context.Context, id int64, op string) error {
	if _, err := s.q.GetSkillCandidateByID(ctx, sqlc.GetSkillCandidateByIDParams{ID: id}); err != nil {
		if platformdb.IsNotFound(err) {
			return wrap(ErrCandidateNotFound, op)
		}
		return wrap(err, op)
	}
	return wrap(ErrCandidateStateMismatch, op)
}

func (s *store) Insert(ctx context.Context, p InsertParams) (Candidate, error) {
	row, err := s.q.InsertSkillCandidate(ctx, sqlc.InsertSkillCandidateParams{
		Scope:           strings.TrimSpace(p.Scope),
		Slug:            strings.TrimSpace(p.Slug),
		ContentHash:     strings.TrimSpace(p.ContentHash),
		RepoFingerprint: p.RepoFingerprint, // literal: business layer matches by exact bytes
		SkillMd:         p.SkillMD,
		RedactedSample:  p.RedactedSample,
	})
	if err != nil {
		return Candidate{}, wrap(err, "insert")
	}
	return fromRow(row), nil
}

func (s *store) GetByID(ctx context.Context, id int64) (Candidate, error) {
	row, err := s.q.GetSkillCandidateByID(ctx, sqlc.GetSkillCandidateByIDParams{ID: id})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Candidate{}, wrap(platformdb.ErrNotFound, "get_by_id")
		}
		return Candidate{}, wrap(err, "get_by_id")
	}
	return fromRow(row), nil
}

func (s *store) ListPending(ctx context.Context, repoFingerprint string, limit, offset int32) ([]Candidate, error) {
	repoFingerprint = strings.TrimSpace(repoFingerprint)
	if !IsValidRepoFingerprint(repoFingerprint) {
		return nil, wrap(ErrInvalidRepoFingerprint, "list_pending")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.q.ListPendingSkillCandidates(ctx, sqlc.ListPendingSkillCandidatesParams{
		RepoFingerprint: repoFingerprint,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, wrap(err, "list_pending")
	}
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out, nil
}

func (s *store) MarkSuperseded(ctx context.Context, scope, slug, repoFingerprint string, keepID int64) (int64, error) {
	scope = strings.TrimSpace(scope)
	slug = strings.TrimSpace(slug)
	repoFingerprint = strings.TrimSpace(repoFingerprint)
	if scope == ScopeProject && !IsValidRepoFingerprint(repoFingerprint) {
		return 0, wrap(ErrInvalidRepoFingerprint, "mark_superseded")
	}
	rows, err := s.q.MarkSkillCandidatesSuperseded(ctx, sqlc.MarkSkillCandidatesSupersededParams{
		Scope:           scope,
		Slug:            slug,
		RepoFingerprint: repoFingerprint,
		ID:              keepID,
	})
	if err != nil {
		return 0, wrap(err, "mark_superseded")
	}
	return rows, nil
}

// Approve transitions pending_review -> approved. The query's WHERE
// status='pending_review' guard means a non-pending row returns
// pgx.ErrNoRows, which we surface as ErrConflict so callers can tell
// "wrong state" apart from "row missing".
func (s *store) Approve(ctx context.Context, id int64, approvedBy, reason string, approvedAt time.Time) (Candidate, error) {
	approvedBy = strings.TrimSpace(approvedBy)
	if approvedBy == "" {
		return Candidate{}, wrap(errors.New("skillcandidate: approved_by required"), "approve")
	}
	row, err := s.q.ApproveSkillCandidate(ctx, sqlc.ApproveSkillCandidateParams{
		ID:         id,
		ApprovedBy: approvedBy,
		ApprovedAt: toTS(approvedAt),
		Reason:     reason,
	})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Candidate{}, s.transitionNoRowsError(ctx, id, "approve")
		}
		return Candidate{}, wrap(err, "approve")
	}
	return fromRow(row), nil
}

func (s *store) Reject(ctx context.Context, id int64, reason string) (Candidate, error) {
	row, err := s.q.RejectSkillCandidate(ctx, sqlc.RejectSkillCandidateParams{
		ID:     id,
		Reason: reason,
	})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Candidate{}, s.transitionNoRowsError(ctx, id, "reject")
		}
		return Candidate{}, wrap(err, "reject")
	}
	return fromRow(row), nil
}

func (s *store) MarkPromoted(ctx context.Context, id int64) (Candidate, error) {
	row, err := s.q.MarkSkillCandidatePromoted(ctx, sqlc.MarkSkillCandidatePromotedParams{ID: id})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Candidate{}, s.transitionNoRowsError(ctx, id, "mark_promoted")
		}
		return Candidate{}, wrap(err, "mark_promoted")
	}
	return fromRow(row), nil
}

// LookupApproval returns (nil, nil) on cache miss. repo_fingerprint is
// matched by literal value (including the empty string) so callers
// cannot accidentally cross-pollinate decisions across projects.
func (s *store) LookupApproval(ctx context.Context, scope, slug, contentHash, repoFingerprint string) (*Candidate, error) {
	repoFingerprint = strings.TrimSpace(repoFingerprint)
	if strings.TrimSpace(scope) == ScopeProject && !IsValidRepoFingerprint(repoFingerprint) {
		return nil, wrap(ErrInvalidRepoFingerprint, "lookup_approval")
	}
	row, err := s.q.LookupSkillCandidateApproval(ctx, sqlc.LookupSkillCandidateApprovalParams{
		Scope:           scope,
		Slug:            slug,
		ContentHash:     contentHash,
		RepoFingerprint: repoFingerprint,
	})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, wrap(err, "lookup_approval")
	}
	c := fromRow(row)
	return &c, nil
}
