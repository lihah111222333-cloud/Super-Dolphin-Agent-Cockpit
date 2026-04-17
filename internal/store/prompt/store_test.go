package prompt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type promptQuerierStub struct {
	listFn func(context.Context, sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error)
}

func (s *promptQuerierStub) ListPromptTemplates(ctx context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func TestListForwardsAgentKeyKeywordAndLimit(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_234_567, 0).UTC()
	var captured sqlc.ListPromptTemplatesParams

	s := &store{q: &promptQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
			captured = arg
			return []sqlc.ListPromptTemplatesRow{{
				ID:          9,
				PromptKey:   "agent.review",
				Title:       "Review flow",
				AgentKey:    "reviewer",
				ToolName:    "review",
				PromptText:  "please review",
				Variables:   []byte(`{"lang":"go"}`),
				Tags:        []byte(`["qa"]`),
				Enabled:     true,
				CreatedBy:   "u1",
				UpdatedBy:   "u2",
				CreatedAt:   now,
				UpdatedAt:   now,
				Description: "code review prompt",
			}}, nil
		},
	}}

	got, err := s.List(context.Background(), ListFilter{AgentKey: "reviewer", Keyword: "review", Limit: 10})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if captured.Column1 != "reviewer" || captured.Column2 != "review" || captured.Limit != 10 {
		t.Fatalf("List() forwarded wrong params: %+v", captured)
	}
	if len(got) != 1 {
		t.Fatalf("List() len = %d, want 1", len(got))
	}
	p := got[0]
	if p.ID != 9 || p.PromptKey != "agent.review" || p.AgentKey != "reviewer" || !p.Enabled {
		t.Fatalf("List() row mapped incorrectly: %+v", p)
	}
	if string(p.Variables) != `{"lang":"go"}` || string(p.Tags) != `["qa"]` {
		t.Fatalf("List() JSON fields mapped incorrectly: vars=%s tags=%s", p.Variables, p.Tags)
	}
}

func TestListReturnsEmptySliceWhenNoRows(t *testing.T) {
	t.Parallel()
	s := &store{q: &promptQuerierStub{}}
	got, err := s.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List() got = %v, want non-nil empty slice", got)
	}
}

func TestListWrapsQuerierError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("pg connection reset")
	s := &store{q: &promptQuerierStub{
		listFn: func(context.Context, sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
			return nil, sentinel
		},
	}}
	_, err := s.List(context.Background(), ListFilter{})
	if err == nil {
		t.Fatal("List() expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("List() error = %v, want wrap of sentinel", err)
	}
}
