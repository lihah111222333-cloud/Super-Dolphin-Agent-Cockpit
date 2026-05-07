package skillcandidate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

const validStoreFP = "0123456789abcdef0123456789abcdef"
const validStoreFPB = "fedcba9876543210fedcba9876543210"

// stubQuerier is a recording test double that satisfies the store's
// internal querier interface. Each *Fn defaults to a benign return so
// individual tests only wire what they exercise.
type stubQuerier struct {
	insertFn         func(context.Context, sqlc.InsertSkillCandidateParams) (sqlc.SkillCandidate, error)
	getByIDFn        func(context.Context, sqlc.GetSkillCandidateByIDParams) (sqlc.SkillCandidate, error)
	listPendingFn    func(context.Context, sqlc.ListPendingSkillCandidatesParams) ([]sqlc.SkillCandidate, error)
	markSupersededFn func(context.Context, sqlc.MarkSkillCandidatesSupersededParams) (int64, error)
	approveFn        func(context.Context, sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error)
	rejectFn         func(context.Context, sqlc.RejectSkillCandidateParams) (sqlc.SkillCandidate, error)
	promoteFn        func(context.Context, sqlc.MarkSkillCandidatePromotedParams) (sqlc.SkillCandidate, error)
	lookupFn         func(context.Context, sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error)
}

func (s *stubQuerier) InsertSkillCandidate(ctx context.Context, a sqlc.InsertSkillCandidateParams) (sqlc.SkillCandidate, error) {
	if s.insertFn != nil {
		return s.insertFn(ctx, a)
	}
	return sqlc.SkillCandidate{
		ID:              1,
		Scope:           a.Scope,
		Slug:            a.Slug,
		ContentHash:     a.ContentHash,
		RepoFingerprint: a.RepoFingerprint,
		Status:          StatusPendingReview,
		SkillMd:         a.SkillMd,
		RedactedSample:  a.RedactedSample,
		CreatedAt:       pgtype.Timestamptz{Time: time.Unix(1700000000, 0).UTC(), Valid: true},
	}, nil
}

func (s *stubQuerier) GetSkillCandidateByID(ctx context.Context, arg sqlc.GetSkillCandidateByIDParams) (sqlc.SkillCandidate, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, arg)
	}
	return sqlc.SkillCandidate{ID: arg.ID, Status: StatusPendingReview}, nil
}

func (s *stubQuerier) ListPendingSkillCandidates(ctx context.Context, a sqlc.ListPendingSkillCandidatesParams) ([]sqlc.SkillCandidate, error) {
	if s.listPendingFn != nil {
		return s.listPendingFn(ctx, a)
	}
	return nil, nil
}

func (s *stubQuerier) MarkSkillCandidatesSuperseded(ctx context.Context, a sqlc.MarkSkillCandidatesSupersededParams) (int64, error) {
	if s.markSupersededFn != nil {
		return s.markSupersededFn(ctx, a)
	}
	return 0, nil
}

func (s *stubQuerier) ApproveSkillCandidate(ctx context.Context, a sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error) {
	if s.approveFn != nil {
		return s.approveFn(ctx, a)
	}
	return sqlc.SkillCandidate{
		ID:         a.ID,
		Status:     StatusApproved,
		ApprovedBy: a.ApprovedBy,
		ApprovedAt: a.ApprovedAt,
		Reason:     a.Reason,
	}, nil
}

func (s *stubQuerier) RejectSkillCandidate(ctx context.Context, a sqlc.RejectSkillCandidateParams) (sqlc.SkillCandidate, error) {
	if s.rejectFn != nil {
		return s.rejectFn(ctx, a)
	}
	return sqlc.SkillCandidate{ID: a.ID, Status: StatusRejected, Reason: a.Reason}, nil
}

func (s *stubQuerier) MarkSkillCandidatePromoted(ctx context.Context, arg sqlc.MarkSkillCandidatePromotedParams) (sqlc.SkillCandidate, error) {
	if s.promoteFn != nil {
		return s.promoteFn(ctx, arg)
	}
	return sqlc.SkillCandidate{ID: arg.ID, Status: StatusPromoted}, nil
}

func (s *stubQuerier) LookupSkillCandidateApproval(ctx context.Context, a sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error) {
	if s.lookupFn != nil {
		return s.lookupFn(ctx, a)
	}
	return sqlc.SkillCandidate{}, pgx.ErrNoRows
}

// ----- Insert -----

func TestStore_Insert_Roundtrip(t *testing.T) {
	t.Parallel()
	var got sqlc.InsertSkillCandidateParams
	stub := &stubQuerier{
		insertFn: func(_ context.Context, a sqlc.InsertSkillCandidateParams) (sqlc.SkillCandidate, error) {
			got = a
			return sqlc.SkillCandidate{
				ID:              42,
				Scope:           a.Scope,
				Slug:            a.Slug,
				ContentHash:     a.ContentHash,
				RepoFingerprint: a.RepoFingerprint,
				Status:          StatusPendingReview,
				SkillMd:         a.SkillMd,
				RedactedSample:  a.RedactedSample,
				CreatedAt:       pgtype.Timestamptz{Time: time.Unix(1700000000, 0).UTC(), Valid: true},
			}, nil
		},
	}
	s := newStoreForTest(stub)
	c, err := s.Insert(context.Background(), InsertParams{
		Scope:           "  project  ",
		Slug:            "  use-cron-helper  ",
		ContentHash:     "  sha256:abc  ",
		RepoFingerprint: validStoreFP,
		SkillMD:         "# Skill\n",
		RedactedSample:  "trace-snippet",
	})
	if err != nil {
		t.Fatalf("Insert error = %v", err)
	}
	if got.Scope != "project" || got.Slug != "use-cron-helper" || got.ContentHash != "sha256:abc" {
		t.Fatalf("trim mismatch: %+v", got)
	}
	if got.RepoFingerprint != validStoreFP {
		t.Fatalf("RepoFingerprint must NOT be trimmed: got %q", got.RepoFingerprint)
	}
	if c.ID != 42 || c.Status != StatusPendingReview || c.SkillMD != "# Skill\n" {
		t.Fatalf("returned candidate = %+v", c)
	}
	if c.CreatedAt.IsZero() {
		t.Fatalf("CreatedAt should be populated from row")
	}
}

func TestStore_Insert_WrapsUniqueViolation(t *testing.T) {
	t.Parallel()
	pgErr := &pgconn.PgError{Code: "23505", Message: "duplicate key"}
	stub := &stubQuerier{
		insertFn: func(context.Context, sqlc.InsertSkillCandidateParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, pgErr
		},
	}
	s := newStoreForTest(stub)
	_, err := s.Insert(context.Background(), InsertParams{Scope: "project", Slug: "x", ContentHash: "h", RepoFingerprint: validStoreFP})
	if err == nil {
		t.Fatal("expected error from unique violation")
	}
	var se *platformdb.StoreError
	if !errors.As(err, &se) {
		t.Fatalf("expected platformdb.StoreError, got %T: %v", err, err)
	}
	if !errors.Is(err, platformdb.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// ----- LookupApproval -----

func TestStore_LookupApproval_Miss_ReturnsNilNil(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		lookupFn: func(context.Context, sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, pgx.ErrNoRows
		},
	}
	s := newStoreForTest(stub)
	got, err := s.LookupApproval(context.Background(), "project", "slug", "hash", validStoreFP)
	if err != nil {
		t.Fatalf("LookupApproval error = %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil candidate on miss, got %+v", got)
	}
}

func TestStore_LookupApproval_Hit_ReturnsCandidate(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		lookupFn: func(_ context.Context, a sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{
				ID:              7,
				Scope:           a.Scope,
				Slug:            a.Slug,
				ContentHash:     a.ContentHash,
				RepoFingerprint: a.RepoFingerprint,
				Status:          StatusApproved,
				ApprovedBy:      "alice",
				ApprovedAt:      pgtype.Timestamptz{Time: time.Unix(1700000123, 0).UTC(), Valid: true},
			}, nil
		},
	}
	s := newStoreForTest(stub)
	got, err := s.LookupApproval(context.Background(), "project", "slug", "hash", validStoreFP)
	if err != nil {
		t.Fatalf("LookupApproval error = %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil candidate on hit")
	}
	if got.ID != 7 || got.Status != StatusApproved || got.ApprovedBy != "alice" {
		t.Fatalf("candidate = %+v", got)
	}
	if got.ApprovedAt == nil || got.ApprovedAt.Unix() != 1700000123 {
		t.Fatalf("ApprovedAt = %v, want unix 1700000123", got.ApprovedAt)
	}
}

func TestStore_LookupApproval_DistinctRepoFingerprintIsolation(t *testing.T) {
	t.Parallel()
	calls := []sqlc.LookupSkillCandidateApprovalParams{}
	stub := &stubQuerier{
		lookupFn: func(_ context.Context, a sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error) {
			calls = append(calls, a)
			return sqlc.SkillCandidate{}, pgx.ErrNoRows
		},
	}
	s := newStoreForTest(stub)
	if _, err := s.LookupApproval(context.Background(), "project", "slug", "hash", validStoreFP); err != nil {
		t.Fatalf("call A error = %v", err)
	}
	if _, err := s.LookupApproval(context.Background(), "project", "slug", "hash", validStoreFPB); err != nil {
		t.Fatalf("call B error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 stub calls, got %d", len(calls))
	}
	if calls[0].RepoFingerprint != validStoreFP {
		t.Fatalf("first call RepoFingerprint = %q, want validStoreFP", calls[0].RepoFingerprint)
	}
	if calls[1].RepoFingerprint != validStoreFPB {
		t.Fatalf("second call RepoFingerprint = %q, want validStoreFPB", calls[1].RepoFingerprint)
	}
}

func TestStore_LookupApproval_ProjectRejectsInvalidFingerprint(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		lookupFn: func(context.Context, sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error) {
			t.Fatal("query must not execute for invalid repo fingerprint")
			return sqlc.SkillCandidate{}, nil
		},
	}
	s := newStoreForTest(stub)
	_, err := s.LookupApproval(context.Background(), "project", "slug", "hash", "")
	if !errors.Is(err, ErrInvalidRepoFingerprint) {
		t.Fatalf("err = %v, want ErrInvalidRepoFingerprint", err)
	}
}

// ----- Approve -----

func TestStore_Approve_RequiresApprovedBy(t *testing.T) {
	t.Parallel()
	called := false
	stub := &stubQuerier{
		approveFn: func(context.Context, sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error) {
			called = true
			return sqlc.SkillCandidate{}, nil
		},
	}
	s := newStoreForTest(stub)
	_, err := s.Approve(context.Background(), 1, "   ", "ok", time.Now())
	if err == nil {
		t.Fatal("expected error when approved_by blank")
	}
	if called {
		t.Fatal("stub must not be invoked when approved_by is blank")
	}
}

func TestStore_Approve_NoRowsExistingCandidateMapsToStateMismatch(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		approveFn: func(context.Context, sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, pgx.ErrNoRows
		},
	}
	s := newStoreForTest(stub)
	_, err := s.Approve(context.Background(), 1, "alice", "lgtm", time.Unix(1700000000, 0).UTC())
	if err == nil {
		t.Fatal("expected error when status mismatches")
	}
	if !errors.Is(err, ErrCandidateStateMismatch) {
		t.Fatalf("expected ErrCandidateStateMismatch, got %v", err)
	}
}

func TestStore_Approve_NoRowsMissingCandidateMapsToNotFound(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		approveFn: func(context.Context, sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, pgx.ErrNoRows
		},
		getByIDFn: func(_ context.Context, arg sqlc.GetSkillCandidateByIDParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, pgx.ErrNoRows
		},
	}
	s := newStoreForTest(stub)
	_, err := s.Approve(context.Background(), 1, "alice", "lgtm", time.Unix(1700000000, 0).UTC())
	if !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("expected ErrCandidateNotFound, got %v", err)
	}
}

func TestStore_Approve_ForwardsApprovedAt(t *testing.T) {
	t.Parallel()
	var got sqlc.ApproveSkillCandidateParams
	stub := &stubQuerier{
		approveFn: func(_ context.Context, a sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error) {
			got = a
			return sqlc.SkillCandidate{
				ID:         a.ID,
				Status:     StatusApproved,
				ApprovedBy: a.ApprovedBy,
				ApprovedAt: a.ApprovedAt,
			}, nil
		},
	}
	s := newStoreForTest(stub)
	when := time.Unix(1700000456, 0).UTC()
	c, err := s.Approve(context.Background(), 5, "  alice  ", "lgtm", when)
	if err != nil {
		t.Fatalf("Approve error = %v", err)
	}
	if got.ApprovedBy != "alice" {
		t.Fatalf("ApprovedBy must be trimmed: got %q", got.ApprovedBy)
	}
	if !got.ApprovedAt.Valid || !got.ApprovedAt.Time.Equal(when) {
		t.Fatalf("ApprovedAt forwarded = %+v, want %v", got.ApprovedAt, when)
	}
	if c.Status != StatusApproved || c.ApprovedAt == nil || !c.ApprovedAt.Equal(when) {
		t.Fatalf("returned candidate = %+v", c)
	}
}

// ----- Reject -----

func TestStore_Reject_NoRowsExistingCandidateMapsToStateMismatch(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		rejectFn: func(context.Context, sqlc.RejectSkillCandidateParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, pgx.ErrNoRows
		},
	}
	s := newStoreForTest(stub)
	_, err := s.Reject(context.Background(), 1, "noisy")
	if err == nil {
		t.Fatal("expected error when status mismatches")
	}
	if !errors.Is(err, ErrCandidateStateMismatch) {
		t.Fatalf("expected ErrCandidateStateMismatch, got %v", err)
	}
}

// ----- MarkPromoted -----

func TestStore_MarkPromoted_NoRowsExistingCandidateMapsToStateMismatch(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		promoteFn: func(_ context.Context, arg sqlc.MarkSkillCandidatePromotedParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, pgx.ErrNoRows
		},
	}
	s := newStoreForTest(stub)
	_, err := s.MarkPromoted(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when status != approved")
	}
	if !errors.Is(err, ErrCandidateStateMismatch) {
		t.Fatalf("expected ErrCandidateStateMismatch, got %v", err)
	}
}

// ----- MarkSuperseded -----

func TestStore_MarkSuperseded_Passthrough(t *testing.T) {
	t.Parallel()
	var got sqlc.MarkSkillCandidatesSupersededParams
	stub := &stubQuerier{
		markSupersededFn: func(_ context.Context, a sqlc.MarkSkillCandidatesSupersededParams) (int64, error) {
			got = a
			return 2, nil
		},
	}
	s := newStoreForTest(stub)
	rows, err := s.MarkSuperseded(context.Background(), " project ", " demo ", validStoreFP, 7)
	if err != nil {
		t.Fatalf("MarkSuperseded error = %v", err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2", rows)
	}
	if got.Scope != "project" || got.Slug != "demo" || got.RepoFingerprint != validStoreFP || got.ID != 7 {
		t.Fatalf("params = %+v", got)
	}
}

func TestStore_MarkSuperseded_RejectsInvalidProjectFingerprint(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		markSupersededFn: func(context.Context, sqlc.MarkSkillCandidatesSupersededParams) (int64, error) {
			t.Fatal("query must not execute for invalid repo fingerprint")
			return 0, nil
		},
	}
	s := newStoreForTest(stub)
	_, err := s.MarkSuperseded(context.Background(), "project", "demo", "bad", 7)
	if !errors.Is(err, ErrInvalidRepoFingerprint) {
		t.Fatalf("err = %v, want ErrInvalidRepoFingerprint", err)
	}
}

// ----- ListPending -----

func TestStore_ListPending_DefaultLimit(t *testing.T) {
	t.Parallel()
	var got sqlc.ListPendingSkillCandidatesParams
	stub := &stubQuerier{
		listPendingFn: func(_ context.Context, a sqlc.ListPendingSkillCandidatesParams) ([]sqlc.SkillCandidate, error) {
			got = a
			return nil, nil
		},
	}
	s := newStoreForTest(stub)
	if _, err := s.ListPending(context.Background(), validStoreFP, 0, 0); err != nil {
		t.Fatalf("ListPending error = %v", err)
	}
	if got.Limit != defaultLimit {
		t.Fatalf("default Limit = %d, want %d", got.Limit, defaultLimit)
	}
	if got.RepoFingerprint != validStoreFP {
		t.Fatalf("RepoFingerprint = %q, want validStoreFP", got.RepoFingerprint)
	}
}

func TestStore_ListPending_RejectsInvalidFingerprint(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		listPendingFn: func(context.Context, sqlc.ListPendingSkillCandidatesParams) ([]sqlc.SkillCandidate, error) {
			t.Fatal("query must not execute for invalid repo fingerprint")
			return nil, nil
		},
	}
	s := newStoreForTest(stub)
	_, err := s.ListPending(context.Background(), "not-a-fp", 10, 0)
	if !errors.Is(err, ErrInvalidRepoFingerprint) {
		t.Fatalf("err = %v, want ErrInvalidRepoFingerprint", err)
	}
}

func TestStore_ListPending_PassthroughPagination(t *testing.T) {
	t.Parallel()
	var got sqlc.ListPendingSkillCandidatesParams
	stub := &stubQuerier{
		listPendingFn: func(_ context.Context, a sqlc.ListPendingSkillCandidatesParams) ([]sqlc.SkillCandidate, error) {
			got = a
			return []sqlc.SkillCandidate{
				{ID: 1, Status: StatusPendingReview, CreatedAt: pgtype.Timestamptz{Time: time.Unix(1700000001, 0), Valid: true}},
				{ID: 2, Status: StatusPendingReview, CreatedAt: pgtype.Timestamptz{Time: time.Unix(1700000002, 0), Valid: true}},
			}, nil
		},
	}
	s := newStoreForTest(stub)
	rows, err := s.ListPending(context.Background(), validStoreFP, 25, 100)
	if err != nil {
		t.Fatalf("ListPending error = %v", err)
	}
	if got.Limit != 25 || got.Offset != 100 {
		t.Fatalf("pagination passthrough: got limit=%d offset=%d", got.Limit, got.Offset)
	}
	if len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

// ----- GetByID -----

func TestStore_GetByID_NoRowsMapsToNotFound(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		getByIDFn: func(_ context.Context, arg sqlc.GetSkillCandidateByIDParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, pgx.ErrNoRows
		},
	}
	s := newStoreForTest(stub)
	_, err := s.GetByID(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when row missing")
	}
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
