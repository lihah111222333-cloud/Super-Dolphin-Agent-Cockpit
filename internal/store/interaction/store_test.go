package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

type interactionQuerierStub struct {
	createFn func(context.Context, sqlc.CreateInteractionParams) (sqlc.AgentInteraction, error)
	getFn    func(context.Context, sqlc.GetInteractionParams) (sqlc.AgentInteraction, error)
	listFn   func(context.Context, sqlc.ListInteractionsParams) ([]sqlc.AgentInteraction, error)
	reviewFn func(context.Context, sqlc.ReviewInteractionParams) (sqlc.AgentInteraction, error)
}

func (s *interactionQuerierStub) CreateInteraction(ctx context.Context, arg sqlc.CreateInteractionParams) (sqlc.AgentInteraction, error) {
	if s.createFn != nil {
		return s.createFn(ctx, arg)
	}
	return sqlc.AgentInteraction{}, nil
}

func (s *interactionQuerierStub) GetInteraction(ctx context.Context, arg sqlc.GetInteractionParams) (sqlc.AgentInteraction, error) {
	if s.getFn != nil {
		return s.getFn(ctx, arg)
	}
	return sqlc.AgentInteraction{}, nil
}

func (s *interactionQuerierStub) ListInteractions(ctx context.Context, arg sqlc.ListInteractionsParams) ([]sqlc.AgentInteraction, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func (s *interactionQuerierStub) ReviewInteraction(ctx context.Context, arg sqlc.ReviewInteractionParams) (sqlc.AgentInteraction, error) {
	if s.reviewFn != nil {
		return s.reviewFn(ctx, arg)
	}
	return sqlc.AgentInteraction{}, nil
}

func fullAgentInteractionFixture() sqlc.AgentInteraction {
	parent := int64(42)
	reviewed := time.Unix(1700000000, 0).UTC().UnixMilli()
	created := time.Unix(1699000000, 0).UTC().UnixMilli()
	updated := time.Unix(1699500000, 0).UTC().UnixMilli()
	return sqlc.AgentInteraction{
		ID:             1,
		ThreadID:       "thread-1",
		ParentID:       &parent,
		Sender:         "orchestrator",
		Receiver:       "agent-A",
		MsgType:        "request",
		Status:         "pending",
		RequiresReview: int64(1),
		ReviewedBy:     "reviewer-1",
		ReviewNote:     "ok",
		ReviewedAt:     &reviewed,
		Payload:        []byte(`{"a":1}`),
		CreatedAt:      created,
		UpdatedAt:      updated,
	}
}

func TestCreateForwardsParamsAndMapsResult(t *testing.T) {
	t.Parallel()

	fixture := fullAgentInteractionFixture()
	var captured sqlc.CreateInteractionParams
	s := &store{q: &interactionQuerierStub{
		createFn: func(_ context.Context, arg sqlc.CreateInteractionParams) (sqlc.AgentInteraction, error) {
			captured = arg
			return fixture, nil
		},
	}}

	input := Interaction{
		ThreadID:       "thread-1",
		ParentID:       fixture.ParentID,
		Sender:         "orchestrator",
		Receiver:       "agent-A",
		MsgType:        "request",
		Status:         "pending",
		RequiresReview: true,
		Payload:        json.RawMessage(`{"a":1}`),
	}

	got, err := s.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got == nil {
		t.Fatal("Create() returned nil *Interaction")
	}
	assertCreateParams(t, captured)
	assertCreatedInteraction(t, got, fixture)
}

type createParamsView struct {
	ThreadID       string
	ParentID       int64
	HasParentID    bool
	Sender         string
	Receiver       string
	MsgType        string
	Status         string
	RequiresReview int64
	Payload        string
}

type interactionView struct {
	ID             int64
	ThreadID       string
	ParentID       int64
	HasParentID    bool
	Sender         string
	Receiver       string
	MsgType        string
	Status         string
	RequiresReview bool
	ReviewedBy     string
	ReviewNote     string
	ReviewedAt     time.Time
	HasReviewedAt  bool
	Payload        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func assertCreateParams(t *testing.T, got sqlc.CreateInteractionParams) {
	t.Helper()
	want := createParamsView{
		ThreadID:       "thread-1",
		ParentID:       42,
		HasParentID:    true,
		Sender:         "orchestrator",
		Receiver:       "agent-A",
		MsgType:        "request",
		Status:         "pending",
		RequiresReview: int64(1),
		Payload:        `{"a":1}`,
	}
	if createParamsViewOf(got) != want {
		t.Fatalf("Create() forwarded wrong params: %+v", got)
	}
}

func assertCreatedInteraction(t *testing.T, got *Interaction, fixture sqlc.AgentInteraction) {
	t.Helper()
	want := interactionViewOfFixture(fixture)
	if interactionViewOf(got) != want {
		t.Fatalf("Create() mapped result = %+v", got)
	}
}

func createParamsViewOf(arg sqlc.CreateInteractionParams) createParamsView {
	parentID, hasParentID := int64PointerView(arg.ParentID)
	return createParamsView{
		ThreadID:       arg.ThreadID,
		ParentID:       parentID,
		HasParentID:    hasParentID,
		Sender:         arg.Sender,
		Receiver:       arg.Receiver,
		MsgType:        arg.MsgType,
		Status:         arg.Status,
		RequiresReview: arg.RequiresReview,
		Payload:        string(arg.Payload),
	}
}

func interactionViewOf(got *Interaction) interactionView {
	parentID, hasParentID := int64PointerView(got.ParentID)
	reviewedAt, hasReviewedAt := timePointerView(got.ReviewedAt)
	return interactionView{
		ID:             got.ID,
		ThreadID:       got.ThreadID,
		ParentID:       parentID,
		HasParentID:    hasParentID,
		Sender:         got.Sender,
		Receiver:       got.Receiver,
		MsgType:        got.MsgType,
		Status:         got.Status,
		RequiresReview: got.RequiresReview,
		ReviewedBy:     got.ReviewedBy,
		ReviewNote:     got.ReviewNote,
		ReviewedAt:     reviewedAt,
		HasReviewedAt:  hasReviewedAt,
		Payload:        string(got.Payload),
		CreatedAt:      got.CreatedAt,
		UpdatedAt:      got.UpdatedAt,
	}
}

func int64MilliPointerView(ms *int64) (time.Time, bool) {
	if ms == nil {
		return time.Time{}, false
	}
	return time.UnixMilli(*ms).UTC(), true
}

func interactionViewOfFixture(fixture sqlc.AgentInteraction) interactionView {
	parentID, hasParentID := int64PointerView(fixture.ParentID)
	reviewedAt, hasReviewedAt := int64MilliPointerView(fixture.ReviewedAt)
	return interactionView{
		ID:             fixture.ID,
		ThreadID:       fixture.ThreadID,
		ParentID:       parentID,
		HasParentID:    hasParentID,
		Sender:         fixture.Sender,
		Receiver:       fixture.Receiver,
		MsgType:        fixture.MsgType,
		Status:         fixture.Status,
		RequiresReview: fixture.RequiresReview != 0,
		ReviewedBy:     fixture.ReviewedBy,
		ReviewNote:     fixture.ReviewNote,
		ReviewedAt:     reviewedAt,
		HasReviewedAt:  hasReviewedAt,
		Payload:        string(fixture.Payload),
		CreatedAt:      time.UnixMilli(fixture.CreatedAt).UTC(),
		UpdatedAt:      time.UnixMilli(fixture.UpdatedAt).UTC(),
	}
}

func int64PointerView(value *int64) (int64, bool) {
	if value == nil {
		return 0, false
	}
	return *value, true
}

func timePointerView(value *time.Time) (time.Time, bool) {
	if value == nil {
		return time.Time{}, false
	}
	return *value, true
}

func TestCreateWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("insert failed")
	s := &store{q: &interactionQuerierStub{
		createFn: func(context.Context, sqlc.CreateInteractionParams) (sqlc.AgentInteraction, error) {
			return sqlc.AgentInteraction{}, sentinel
		},
	}}

	got, err := s.Create(context.Background(), Interaction{})
	if got != nil {
		t.Fatalf("Create() got = %+v, want nil on error", got)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Create() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "create" || storeErr.Entity != "interaction" {
		t.Fatalf("Create() error metadata = %+v", err)
	}
}

func TestGetForwardsIDAndMapsResult(t *testing.T) {
	t.Parallel()

	fixture := fullAgentInteractionFixture()
	var capturedID int64
	s := &store{q: &interactionQuerierStub{
		getFn: func(_ context.Context, arg sqlc.GetInteractionParams) (sqlc.AgentInteraction, error) {
			id := arg.ID
			capturedID = id
			return fixture, nil
		},
	}}

	got, err := s.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if capturedID != 1 {
		t.Fatalf("Get() forwarded id = %d, want 1", capturedID)
	}
	if got == nil || got.ID != fixture.ID || got.ThreadID != fixture.ThreadID {
		t.Fatalf("Get() = %+v", got)
	}
}

func TestGetWrapsSQLErrNoRowsAsNotFound(t *testing.T) {
	t.Parallel()

	s := &store{q: &interactionQuerierStub{
		getFn: func(context.Context, sqlc.GetInteractionParams) (sqlc.AgentInteraction, error) {
			return sqlc.AgentInteraction{}, sql.ErrNoRows
		},
	}}

	got, err := s.Get(context.Background(), 99)
	if got != nil {
		t.Fatalf("Get() got = %+v, want nil on error", got)
	}
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("Get() error = %v, want wrap of ErrNotFound", err)
	}
}

func TestListForwardsFilterAndMapsRows(t *testing.T) {
	t.Parallel()

	fixture := fullAgentInteractionFixture()
	var captured sqlc.ListInteractionsParams
	s := &store{q: &interactionQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListInteractionsParams) ([]sqlc.AgentInteraction, error) {
			captured = arg
			return []sqlc.AgentInteraction{fixture}, nil
		},
	}}

	got, err := s.List(context.Background(), ListFilter{ThreadID: "thread-1", Keyword: "agent", Limit: 20})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	assertListInteractionsParams(t, captured)
	assertListedInteractions(t, got, fixture)
}

func assertListInteractionsParams(t *testing.T, captured sqlc.ListInteractionsParams) {
	t.Helper()

	if captured.Column1 != "thread-1" || captured.Column3 != "agent" || captured.Limit != 20 {
		t.Fatalf("List() forwarded wrong params: %+v", captured)
	}
	if captured.Column4 == nil || *captured.Column4 != "agent" || captured.Column5 == nil || *captured.Column5 != "agent" || captured.Column6 == nil || *captured.Column6 != "agent" {
		t.Fatalf("List() forwarded wrong keyword LIKE params: %+v", captured)
	}
}

func assertListedInteractions(t *testing.T, got []Interaction, fixture sqlc.AgentInteraction) {
	t.Helper()

	if len(got) != 1 || got[0].ID != fixture.ID || got[0].ThreadID != fixture.ThreadID {
		t.Fatalf("List() = %+v", got)
	}
	if string(got[0].Payload) != `{"a":1}` {
		t.Fatalf("List() payload = %s", got[0].Payload)
	}
}

func TestListReturnsEmptySliceWhenNoRows(t *testing.T) {
	t.Parallel()

	s := &store{q: &interactionQuerierStub{
		listFn: func(context.Context, sqlc.ListInteractionsParams) ([]sqlc.AgentInteraction, error) {
			return nil, nil
		},
	}}
	got, err := s.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List() = %+v, want non-nil empty slice", got)
	}
}

func TestListWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("list fail")
	s := &store{q: &interactionQuerierStub{
		listFn: func(context.Context, sqlc.ListInteractionsParams) ([]sqlc.AgentInteraction, error) {
			return nil, sentinel
		},
	}}
	_, err := s.List(context.Background(), ListFilter{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("List() error = %v, want wrap of sentinel", err)
	}
}

func TestReviewForwardsInputAndMapsResult(t *testing.T) {
	t.Parallel()

	fixture := fullAgentInteractionFixture()
	fixture.Status = "approved"
	fixture.ReviewedBy = "reviewer-2"
	fixture.ReviewNote = "looks good"
	var captured sqlc.ReviewInteractionParams
	s := &store{q: &interactionQuerierStub{
		reviewFn: func(_ context.Context, arg sqlc.ReviewInteractionParams) (sqlc.AgentInteraction, error) {
			captured = arg
			return fixture, nil
		},
	}}

	got, err := s.Review(context.Background(), ReviewInput{ID: 1, Status: "approved", ReviewedBy: "reviewer-2", ReviewNote: "looks good"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if captured.ID != 1 || captured.Status != "approved" || captured.ReviewedBy != "reviewer-2" || captured.ReviewNote != "looks good" {
		t.Fatalf("Review() forwarded wrong params: %+v", captured)
	}
	if got == nil || got.Status != "approved" || got.ReviewedBy != "reviewer-2" || got.ReviewNote != "looks good" {
		t.Fatalf("Review() = %+v", got)
	}
}

func TestReviewWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("review fail")
	s := &store{q: &interactionQuerierStub{
		reviewFn: func(context.Context, sqlc.ReviewInteractionParams) (sqlc.AgentInteraction, error) {
			return sqlc.AgentInteraction{}, sentinel
		},
	}}
	got, err := s.Review(context.Background(), ReviewInput{ID: 1})
	if got != nil {
		t.Fatalf("Review() got = %+v, want nil on error", got)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Review() error = %v, want wrap of sentinel", err)
	}
}

func TestInteractionSQLiteCreateSetsTimestampsAndRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	store, _ := newInteractionSQLiteStore(t)
	got, err := store.Create(context.Background(), Interaction{
		ThreadID:       "thread-1",
		Sender:         "orchestrator",
		Receiver:       "agent-A",
		MsgType:        "request",
		Status:         "pending",
		RequiresReview: true,
		Payload:        json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("Create() timestamps = created %v updated %v, want non-zero", got.CreatedAt, got.UpdatedAt)
	}

	_, err = store.Create(context.Background(), Interaction{
		ThreadID: "thread-1",
		Sender:   "orchestrator",
		Receiver: "agent-A",
		MsgType:  "request",
		Status:   "pending",
		Payload:  json.RawMessage(`{`),
	})
	if err == nil {
		t.Fatal("Create() invalid JSON error = nil, want CHECK failure")
	}
}

func TestReviewInteractionSQLiteRequiresPendingReview(t *testing.T) {
	t.Parallel()

	store, db := newInteractionSQLiteStore(t)
	created, err := store.Create(context.Background(), Interaction{
		ThreadID:       "thread-1",
		Sender:         "orchestrator",
		Receiver:       "agent-A",
		MsgType:        "request",
		Status:         "pending",
		RequiresReview: true,
		Payload:        json.RawMessage(`{"needs":"review"}`),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	approved, err := store.Review(context.Background(), ReviewInput{ID: created.ID, Status: "approved", ReviewedBy: "reviewer-1", ReviewNote: "ok"})
	if err != nil {
		t.Fatalf("Review() first error = %v", err)
	}
	if approved.Status != "approved" || approved.ReviewedBy != "reviewer-1" {
		t.Fatalf("Review() first = %+v", approved)
	}

	_, err = store.Review(context.Background(), ReviewInput{ID: created.ID, Status: "rejected", ReviewedBy: "reviewer-2", ReviewNote: "stale"})
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("Review() repeated error = %v, want ErrNotFound", err)
	}
	assertInteractionStatus(t, db, created.ID, "approved", "reviewer-1")
}

func TestReviewInteractionSQLiteRequiresReviewFlag(t *testing.T) {
	t.Parallel()

	store, _ := newInteractionSQLiteStore(t)
	created, err := store.Create(context.Background(), Interaction{
		ThreadID:       "thread-1",
		Sender:         "orchestrator",
		Receiver:       "agent-A",
		MsgType:        "message",
		Status:         "pending",
		RequiresReview: false,
		Payload:        json.RawMessage(`{"needs":"none"}`),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = store.Review(context.Background(), ReviewInput{ID: created.ID, Status: "approved", ReviewedBy: "reviewer-1"})
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("Review() non-reviewable error = %v, want ErrNotFound", err)
	}
}

func newInteractionSQLiteStore(t *testing.T) (*store, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "interaction.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if _, err := db.Exec(`
CREATE TABLE agent_interactions (
	id INTEGER PRIMARY KEY,
	thread_id TEXT NOT NULL DEFAULT '',
	parent_id INTEGER,
	sender TEXT NOT NULL,
	receiver TEXT NOT NULL DEFAULT '',
	msg_type TEXT NOT NULL DEFAULT 'task',
	status TEXT NOT NULL DEFAULT 'pending',
	requires_review INTEGER NOT NULL DEFAULT 0 CHECK(requires_review IN (0, 1)),
	reviewed_by TEXT NOT NULL DEFAULT '',
	review_note TEXT NOT NULL DEFAULT '',
	reviewed_at INTEGER,
	payload TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload)),
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return &store{q: sqlc.New(db)}, db
}

func assertInteractionStatus(t *testing.T, db *sql.DB, id int64, wantStatus, wantReviewer string) {
	t.Helper()

	var status, reviewer string
	if err := db.QueryRow("SELECT status, reviewed_by FROM agent_interactions WHERE id = ?", id).Scan(&status, &reviewer); err != nil {
		t.Fatalf("query interaction status: %v", err)
	}
	if status != wantStatus || reviewer != wantReviewer {
		t.Fatalf("interaction status/reviewer = %q/%q, want %q/%q", status, reviewer, wantStatus, wantReviewer)
	}
}
