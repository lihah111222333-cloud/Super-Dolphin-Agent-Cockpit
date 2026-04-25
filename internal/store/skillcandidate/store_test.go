package skillcandidate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// stubQuerier is a recording test double that satisfies the store's
// internal querier interface. Each *Fn defaults to a benign return so
// individual tests only wire what they exercise.
type stubQuerier struct {
	insertFn      func(context.Context, sqlc.InsertSkillCandidateParams) (sqlc.SkillCandidate, error)
	getByIDFn     func(context.Context, int64) (sqlc.SkillCandidate, error)
	listPendingFn func(context.Context, sqlc.ListPendingSkillCandidatesParams) ([]sqlc.SkillCandidate, error)
	approveFn     func(context.Context, sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error)
	rejectFn      func(context.Context, sqlc.RejectSkillCandidateParams) (sqlc.SkillCandidate, error)
	promoteFn     func(context.Context, int64) (sqlc.SkillCandidate, error)
	lookupFn      func(context.Context, sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error)
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

func (s *stubQuerier) GetSkillCandidateByID(ctx context.Context, id int64) (sqlc.SkillCandidate, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, id)
	}
	return sqlc.SkillCandidate{ID: id, Status: StatusPendingReview}, nil
}

func (s *stubQuerier) ListPendingSkillCandidates(ctx context.Context, a sqlc.ListPendingSkillCandidatesParams) ([]sqlc.SkillCandidate, error) {
	if s.listPendingFn != nil {
		return s.listPendingFn(ctx, a)
	}
	return nil, nil
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

func (s *stubQuerier) MarkSkillCandidatePromoted(ctx context.Context, id int64) (sqlc.SkillCandidate, error) {
	if s.promoteFn != nil {
		return s.promoteFn(ctx, id)
	}
	return sqlc.SkillCandidate{ID: id, Status: StatusPromoted}, nil
}

func (s *stubQuerier) LookupSkillCandidateApproval(ctx context.Context, a sqlc.LookupSkillCandidateApprovalParams) (sqlc.SkillCandidate, error) {
	if s.lookupFn != nil {
		return s.lookupFn(ctx, a)
	}
	return sqlc.SkillCandidate{}, platformdb.ErrNotFound
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
		RepoFingerprint: "fp:repo-1",
		SkillMD:         "# Skill\n",
		RedactedSample:  "trace-snippet",
	})
	if err != nil {
		t.Fatalf("Insert error = %v", err)
	}
	if got.Scope != "project" || got.Slug != "use-cron-helper" || got.ContentHash != "sha256:abc" {
		t.Fatalf("trim mismatch: %+v", got)
	}
	if got.RepoFingerprint != "fp:repo-1" {
		t.Fatalf("RepoFingerprint must NOT be trimmed: got %q", got.RepoFingerprint)
	}
	if c.ID != 42 || c.Status != StatusPendingReview || c.SkillMD != "# Skill\n" {
		t.Fatalf("returned candidate = %+v", c)
	}
	if c.CreatedAt.IsZero() {
		t.Fatalf("CreatedAt should be populated from row")
	}
}

func TestStore_Insert_WrapsConflict(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		insertFn: func(context.Context, sqlc.InsertSkillCandidateParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, platformdb.ErrConflict
		},
	}
	s := newStoreForTest(stub)
	_, err := s.Insert(context.Background(), InsertParams{Scope: "project", Slug: "x", ContentHash: "h", RepoFingerprint: "fp"})
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
			return sqlc.SkillCandidate{}, platformdb.ErrNotFound
		},
	}
	s := newStoreForTest(stub)
	got, err := s.LookupApproval(context.Background(), "project", "slug", "hash", "fp")
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
	got, err := s.LookupApproval(context.Background(), "project", "slug", "hash", "fp")
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
			return sqlc.SkillCandidate{}, platformdb.ErrNotFound
		},
	}
	s := newStoreForTest(stub)
	if _, err := s.LookupApproval(context.Background(), "project", "slug", "hash", "fp:repo-A"); err != nil {
		t.Fatalf("call A error = %v", err)
	}
	if _, err := s.LookupApproval(context.Background(), "project", "slug", "hash", ""); err != nil {
		t.Fatalf("call B error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 stub calls, got %d", len(calls))
	}
	if calls[0].RepoFingerprint != "fp:repo-A" {
		t.Fatalf("first call RepoFingerprint = %q, want fp:repo-A", calls[0].RepoFingerprint)
	}
	if calls[1].RepoFingerprint != "" {
		t.Fatalf("second call RepoFingerprint must stay literal empty, got %q", calls[1].RepoFingerprint)
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

func TestStore_Approve_NoRowsMapsToConflict(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		approveFn: func(context.Context, sqlc.ApproveSkillCandidateParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, platformdb.ErrNotFound
		},
	}
	s := newStoreForTest(stub)
	_, err := s.Approve(context.Background(), 1, "alice", "lgtm", time.Unix(1700000000, 0).UTC())
	if err == nil {
		t.Fatal("expected error when row missing or wrong status")
	}
	if !errors.Is(err, platformdb.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
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

func TestStore_Reject_NoRowsMapsToConflict(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		rejectFn: func(context.Context, sqlc.RejectSkillCandidateParams) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, platformdb.ErrNotFound
		},
	}
	s := newStoreForTest(stub)
	_, err := s.Reject(context.Background(), 1, "noisy")
	if err == nil {
		t.Fatal("expected error when row missing or wrong status")
	}
	if !errors.Is(err, platformdb.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// ----- MarkPromoted -----

func TestStore_MarkPromoted_NoRowsMapsToConflict(t *testing.T) {
	t.Parallel()
	stub := &stubQuerier{
		promoteFn: func(context.Context, int64) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, platformdb.ErrNotFound
		},
	}
	s := newStoreForTest(stub)
	_, err := s.MarkPromoted(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when status != approved")
	}
	if !errors.Is(err, platformdb.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
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
	if _, err := s.ListPending(context.Background(), 0, 0); err != nil {
		t.Fatalf("ListPending error = %v", err)
	}
	if got.Limit != defaultLimit {
		t.Fatalf("default Limit = %d, want %d", got.Limit, defaultLimit)
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
	rows, err := s.ListPending(context.Background(), 25, 100)
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
		getByIDFn: func(context.Context, int64) (sqlc.SkillCandidate, error) {
			return sqlc.SkillCandidate{}, platformdb.ErrNotFound
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
