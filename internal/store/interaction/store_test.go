package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
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

func TestGetWrapsPgxErrNoRowsAsNotFound(t *testing.T) {
	t.Parallel()

	s := &store{q: &interactionQuerierStub{
		getFn: func(context.Context, sqlc.GetInteractionParams) (sqlc.AgentInteraction, error) {
			return sqlc.AgentInteraction{}, pgx.ErrNoRows
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
	if captured.Column1 != "thread-1" || captured.Column2 != "agent" || captured.Limit != 20 {
		t.Fatalf("List() forwarded wrong params: %+v", captured)
	}
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
