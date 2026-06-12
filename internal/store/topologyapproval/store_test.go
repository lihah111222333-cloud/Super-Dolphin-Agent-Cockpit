package topologyapproval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

type topologyApprovalQuerierStub struct {
	createFn      func(context.Context, sqlc.CreateTopologyApprovalParams) (sqlc.TopologyApproval, error)
	approveFn     func(context.Context, sqlc.ApproveTopologyApprovalParams) (int64, error)
	rejectFn      func(context.Context, sqlc.RejectTopologyApprovalParams) (int64, error)
	listPendingFn func(context.Context) ([]sqlc.TopologyApproval, error)
}

func (s *topologyApprovalQuerierStub) CreateTopologyApproval(ctx context.Context, arg sqlc.CreateTopologyApprovalParams) (sqlc.TopologyApproval, error) {
	if s.createFn != nil {
		return s.createFn(ctx, arg)
	}
	return sqlc.TopologyApproval{}, nil
}

func (s *topologyApprovalQuerierStub) ApproveTopologyApproval(ctx context.Context, arg sqlc.ApproveTopologyApprovalParams) (int64, error) {
	if s.approveFn != nil {
		return s.approveFn(ctx, arg)
	}
	return 0, nil
}

func (s *topologyApprovalQuerierStub) RejectTopologyApproval(ctx context.Context, arg sqlc.RejectTopologyApprovalParams) (int64, error) {
	if s.rejectFn != nil {
		return s.rejectFn(ctx, arg)
	}
	return 0, nil
}

func (s *topologyApprovalQuerierStub) ListPendingTopologyApprovals(ctx context.Context) ([]sqlc.TopologyApproval, error) {
	if s.listPendingFn != nil {
		return s.listPendingFn(ctx)
	}
	return nil, nil
}

func fullTopologyApprovalFixture() sqlc.TopologyApproval {
	reviewed := time.Unix(1_700_000_000, 0).UTC().UnixMilli()
	return sqlc.TopologyApproval{
		ID:                   "appr-1",
		Status:               "pending",
		RequestedBy:          "alice",
		Reason:               "add-node",
		CreatedAt:            time.Unix(1_699_000_000, 0).UTC().UnixMilli(),
		ExpireAt:             time.Unix(1_800_000_000, 0).UTC().UnixMilli(),
		ReviewedAt:           &reviewed,
		Reviewer:             "bob",
		ReviewNote:           "ok",
		ArchHash:             "hash-xyz",
		ProposedArchitecture: `{"nodes":1}`,
	}
}

func TestCreateForwardsParamsAndMapsResult(t *testing.T) {
	t.Parallel()

	fixture := fullTopologyApprovalFixture()
	var captured sqlc.CreateTopologyApprovalParams
	s := newCreateTopologyApprovalTestStore(fixture, &captured)

	got, err := s.Create(context.Background(), topologyApprovalCreateInput(fixture))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	requireCreateTopologyApprovalParams(t, captured, fixture)
	requireCreatedTopologyApproval(t, got, fixture)
}

func newCreateTopologyApprovalTestStore(fixture sqlc.TopologyApproval, captured *sqlc.CreateTopologyApprovalParams) *store {
	return &store{q: &topologyApprovalQuerierStub{
		createFn: func(_ context.Context, arg sqlc.CreateTopologyApprovalParams) (sqlc.TopologyApproval, error) {
			*captured = arg
			return fixture, nil
		},
	}}
}

func topologyApprovalCreateInput(fixture sqlc.TopologyApproval) TopologyApproval {
	return TopologyApproval{
		ID:                   "appr-1",
		RequestedBy:          "alice",
		Reason:               "add-node",
		CreatedAt:            platformdb.TimeFromMillis(fixture.CreatedAt),
		ExpireAt:             platformdb.TimeFromMillis(fixture.ExpireAt),
		ArchHash:             "hash-xyz",
		ProposedArchitecture: json.RawMessage(`{"nodes":1}`),
	}
}

func requireCreateTopologyApprovalParams(t *testing.T, captured sqlc.CreateTopologyApprovalParams, fixture sqlc.TopologyApproval) {
	t.Helper()
	if captured.ID != "appr-1" {
		t.Fatalf("Create() ID = %q, want appr-1", captured.ID)
	}
	if captured.RequestedBy != "alice" || captured.Reason != "add-node" {
		t.Fatalf("Create() forwarded wrong params: %+v", captured)
	}
	if captured.CreatedAt != fixture.CreatedAt || captured.ExpireAt != fixture.ExpireAt {
		t.Fatalf("Create() forwarded wrong times: %+v", captured)
	}
	if captured.ArchHash != "hash-xyz" || captured.ProposedArchitecture != `{"nodes":1}` {
		t.Fatalf("Create() forwarded wrong payload: %+v", captured)
	}
}

func requireCreatedTopologyApproval(t *testing.T, got *TopologyApproval, fixture sqlc.TopologyApproval) {
	t.Helper()
	if got == nil {
		t.Fatal("Create() = nil, want mapped result")
	}
	if got.ID != "appr-1" || got.Status != "pending" || got.Reviewer != "bob" {
		t.Fatalf("Create() identity/status = %+v", got)
	}
	if got.ReviewNote != "ok" || got.ArchHash != "hash-xyz" {
		t.Fatalf("Create() review/hash = %+v", got)
	}
	wantReviewedAt := platformdb.TimeFromMillis(*fixture.ReviewedAt)
	if got.ReviewedAt == nil || !got.ReviewedAt.Equal(wantReviewedAt) {
		t.Fatalf("Create() ReviewedAt = %+v, want %+v", got.ReviewedAt, wantReviewedAt)
	}
	if string(got.ProposedArchitecture) != `{"nodes":1}` {
		t.Fatalf("Create() ProposedArchitecture = %s", got.ProposedArchitecture)
	}
}

func TestCreateWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("insert fail")
	s := &store{q: &topologyApprovalQuerierStub{
		createFn: func(context.Context, sqlc.CreateTopologyApprovalParams) (sqlc.TopologyApproval, error) {
			return sqlc.TopologyApproval{}, sentinel
		},
	}}
	got, err := s.Create(context.Background(), TopologyApproval{})
	if got != nil {
		t.Fatalf("Create() got = %+v, want nil on error", got)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Create() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "create" || storeErr.Entity != "topology_approval" {
		t.Fatalf("Create() error metadata = %+v", err)
	}
}

func TestApproveForwardsParamsAndReturnsCount(t *testing.T) {
	t.Parallel()

	var captured sqlc.ApproveTopologyApprovalParams
	s := &store{q: &topologyApprovalQuerierStub{
		approveFn: func(_ context.Context, arg sqlc.ApproveTopologyApprovalParams) (int64, error) {
			captured = arg
			return 1, nil
		},
	}}
	count, err := s.Approve(context.Background(), "bob", "appr-1")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("Approve() count = %d, want 1", count)
	}
	if captured.Reviewer != "bob" || captured.ID != "appr-1" {
		t.Fatalf("Approve() forwarded wrong params: %+v", captured)
	}
}

func TestApproveWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("approve fail")
	s := &store{q: &topologyApprovalQuerierStub{
		approveFn: func(context.Context, sqlc.ApproveTopologyApprovalParams) (int64, error) { return 0, sentinel },
	}}
	_, err := s.Approve(context.Background(), "bob", "appr-1")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Approve() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "approve" {
		t.Fatalf("Approve() error metadata = %+v", err)
	}
}

func TestRejectForwardsParamsAndReturnsCount(t *testing.T) {
	t.Parallel()

	var captured sqlc.RejectTopologyApprovalParams
	s := &store{q: &topologyApprovalQuerierStub{
		rejectFn: func(_ context.Context, arg sqlc.RejectTopologyApprovalParams) (int64, error) {
			captured = arg
			return 2, nil
		},
	}}
	count, err := s.Reject(context.Background(), "carol", "appr-2")
	if err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("Reject() count = %d, want 2", count)
	}
	if captured.Reviewer != "carol" || captured.ID != "appr-2" {
		t.Fatalf("Reject() forwarded wrong params: %+v", captured)
	}
}

func TestRejectWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("reject fail")
	s := &store{q: &topologyApprovalQuerierStub{
		rejectFn: func(context.Context, sqlc.RejectTopologyApprovalParams) (int64, error) { return 0, sentinel },
	}}
	_, err := s.Reject(context.Background(), "x", "y")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Reject() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "reject" {
		t.Fatalf("Reject() error metadata = %+v", err)
	}
}

func TestListPendingMapsRows(t *testing.T) {
	t.Parallel()

	fixture := fullTopologyApprovalFixture()
	s := &store{q: &topologyApprovalQuerierStub{
		listPendingFn: func(context.Context) ([]sqlc.TopologyApproval, error) {
			return []sqlc.TopologyApproval{fixture}, nil
		},
	}}
	got, err := s.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != fixture.ID || got[0].ArchHash != fixture.ArchHash {
		t.Fatalf("ListPending() = %+v", got)
	}
	if string(got[0].ProposedArchitecture) != `{"nodes":1}` {
		t.Fatalf("ListPending() proposed_architecture = %s", got[0].ProposedArchitecture)
	}
}

func TestListPendingReturnsEmptySliceWhenNoRows(t *testing.T) {
	t.Parallel()

	s := &store{q: &topologyApprovalQuerierStub{
		listPendingFn: func(context.Context) ([]sqlc.TopologyApproval, error) { return nil, nil },
	}}
	got, err := s.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ListPending() = %+v, want non-nil empty slice", got)
	}
}

func TestListPendingWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("list fail")
	s := &store{q: &topologyApprovalQuerierStub{
		listPendingFn: func(context.Context) ([]sqlc.TopologyApproval, error) { return nil, sentinel },
	}}
	_, err := s.ListPending(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("ListPending() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "list_pending" {
		t.Fatalf("ListPending() error metadata = %+v", err)
	}
}

func TestTopologyApprovalSQLiteApproveRejectSingleWinner(t *testing.T) {
	t.Parallel()

	store, db := newTopologyApprovalSQLiteStore(t)
	input := TopologyApproval{
		ID:                   "appr-race",
		RequestedBy:          "alice",
		Reason:               "change",
		CreatedAt:            time.Now().UTC(),
		ExpireAt:             time.Now().UTC().Add(time.Hour),
		ArchHash:             "hash-race",
		ProposedArchitecture: json.RawMessage(`{"nodes":[{"id":"a"}]}`),
	}
	if _, err := store.Create(context.Background(), input); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	approved, err := store.Approve(context.Background(), "reviewer-a", input.ID)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	rejected, err := store.Reject(context.Background(), "reviewer-b", input.ID)
	if err != nil {
		t.Fatalf("Reject() stale error = %v", err)
	}
	if approved != 1 || rejected != 0 {
		t.Fatalf("approve/reject rows = %d/%d, want 1/0", approved, rejected)
	}
	assertTopologyApprovalStatus(t, db, input.ID, "approved", "reviewer-a")
}

func TestTopologyApprovalSQLiteRejectApproveSingleWinner(t *testing.T) {
	t.Parallel()

	store, db := newTopologyApprovalSQLiteStore(t)
	input := TopologyApproval{
		ID:                   "appr-reject",
		RequestedBy:          "alice",
		Reason:               "change",
		CreatedAt:            time.Now().UTC(),
		ExpireAt:             time.Now().UTC().Add(time.Hour),
		ArchHash:             "hash-reject",
		ProposedArchitecture: json.RawMessage(`{"nodes":[{"id":"a"}]}`),
	}
	if _, err := store.Create(context.Background(), input); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rejected, err := store.Reject(context.Background(), "reviewer-b", input.ID)
	if err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	approved, err := store.Approve(context.Background(), "reviewer-a", input.ID)
	if err != nil {
		t.Fatalf("Approve() stale error = %v", err)
	}
	if rejected != 1 || approved != 0 {
		t.Fatalf("reject/approve rows = %d/%d, want 1/0", rejected, approved)
	}
	assertTopologyApprovalStatus(t, db, input.ID, "rejected", "reviewer-b")
}

func TestTopologyApprovalSQLiteRejectsInvalidArchitectureJSON(t *testing.T) {
	t.Parallel()

	store, _ := newTopologyApprovalSQLiteStore(t)
	_, err := store.Create(context.Background(), TopologyApproval{
		ID:                   "appr-invalid-json",
		RequestedBy:          "alice",
		Reason:               "bad",
		CreatedAt:            time.Now().UTC(),
		ExpireAt:             time.Now().UTC().Add(time.Hour),
		ArchHash:             "hash-invalid",
		ProposedArchitecture: json.RawMessage(`{`),
	})
	if err == nil {
		t.Fatal("Create() invalid proposed_architecture error = nil, want CHECK failure")
	}
}

func newTopologyApprovalSQLiteStore(t *testing.T) (*store, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "topologyapproval.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if _, err := db.Exec(`
CREATE TABLE topology_approvals (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	requested_by TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	expire_at INTEGER NOT NULL,
	reviewed_at INTEGER,
	reviewer TEXT NOT NULL DEFAULT '',
	review_note TEXT NOT NULL DEFAULT '',
	arch_hash TEXT NOT NULL,
	proposed_architecture TEXT NOT NULL CHECK(json_valid(proposed_architecture))
);
`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return &store{q: sqlc.New(db)}, db
}

func assertTopologyApprovalStatus(t *testing.T, db *sql.DB, id, wantStatus, wantReviewer string) {
	t.Helper()

	var status, reviewer string
	if err := db.QueryRow("SELECT status, reviewer FROM topology_approvals WHERE id = ?", id).Scan(&status, &reviewer); err != nil {
		t.Fatalf("query topology approval status: %v", err)
	}
	if status != wantStatus || reviewer != wantReviewer {
		t.Fatalf("topology approval status/reviewer = %q/%q, want %q/%q", status, reviewer, wantStatus, wantReviewer)
	}
}
