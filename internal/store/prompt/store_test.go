package prompt

import (
	"context"
	"errors"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type promptQuerierStub struct {
	listFn          func(context.Context, sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error)
	getFn           func(context.Context, string) (sqlc.GetPromptTemplateRow, error)
	deleteFn        func(context.Context, string) (int64, error)
	insertVersionFn func(context.Context, sqlc.InsertPromptVersionParams) (int64, error)
	upsertFn        func(context.Context, sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error)
}

func (s *promptQuerierStub) ListPromptTemplates(ctx context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func (s *promptQuerierStub) GetPromptTemplate(ctx context.Context, promptKey string) (sqlc.GetPromptTemplateRow, error) {
	if s.getFn != nil {
		return s.getFn(ctx, promptKey)
	}
	return sqlc.GetPromptTemplateRow{}, nil
}

func (s *promptQuerierStub) DeletePromptTemplate(ctx context.Context, promptKey string) (int64, error) {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, promptKey)
	}
	return 0, nil
}

func (s *promptQuerierStub) InsertPromptVersion(ctx context.Context, arg sqlc.InsertPromptVersionParams) (int64, error) {
	if s.insertVersionFn != nil {
		return s.insertVersionFn(ctx, arg)
	}
	return 0, nil
}

func (s *promptQuerierStub) UpsertPromptTemplate(ctx context.Context, arg sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error) {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, arg)
	}
	return sqlc.UpsertPromptTemplateRow{}, nil
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

func TestStoreGetUpsertDeleteAndInsertVersion(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()

	t.Run("get maps row", func(t *testing.T) {
		var capturedKey string
		s := &store{q: &promptQuerierStub{
			getFn: func(_ context.Context, promptKey string) (sqlc.GetPromptTemplateRow, error) {
				capturedKey = promptKey
				return sqlc.GetPromptTemplateRow{
					ID:          7,
					PromptKey:   promptKey,
					Title:       "Scoped Prompt",
					AgentKey:    "main",
					ToolName:    "tool",
					PromptText:  "body",
					Variables:   []byte(`{"lang":"go"}`),
					Tags:        []byte(`{"unexpected":true}`),
					Description: "desc",
					Enabled:     true,
					CreatedBy:   "creator",
					UpdatedBy:   "editor",
					CreatedAt:   now,
					UpdatedAt:   now,
				}, nil
			},
		}}

		got, err := s.Get(context.Background(), "main/scoped")
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if capturedKey != "main/scoped" {
			t.Fatalf("Get() prompt key = %q, want main/scoped", capturedKey)
		}
		if got == nil || got.ID != 7 || got.Title != "Scoped Prompt" || got.AgentKey != "main" {
			t.Fatalf("Get() mapped row incorrectly: %+v", got)
		}
	})

	t.Run("upsert forwards params and maps row", func(t *testing.T) {
		var captured sqlc.UpsertPromptTemplateParams
		s := &store{q: &promptQuerierStub{
			upsertFn: func(_ context.Context, arg sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error) {
				captured = arg
				return sqlc.UpsertPromptTemplateRow{
					ID:          8,
					PromptKey:   arg.PromptKey,
					Title:       arg.Title,
					AgentKey:    arg.AgentKey,
					ToolName:    arg.ToolName,
					PromptText:  arg.PromptText,
					Variables:   arg.Column6,
					Tags:        arg.Column7,
					Description: arg.Description,
					Enabled:     arg.Enabled,
					CreatedBy:   arg.CreatedBy,
					UpdatedBy:   arg.UpdatedBy,
					CreatedAt:   now,
					UpdatedAt:   now,
				}, nil
			},
		}}

		got, err := s.Upsert(context.Background(), PromptTemplate{
			PromptKey:   "main/scoped",
			Title:       "Scoped Prompt",
			AgentKey:    "main",
			ToolName:    "tool",
			PromptText:  "body",
			Variables:   []byte(`{"lang":"go"}`),
			Tags:        []byte(`["scope.cwd:/repo"]`),
			Description: "desc",
			Enabled:     true,
			CreatedBy:   "creator",
			UpdatedBy:   "editor",
		})
		if err != nil {
			t.Fatalf("Upsert() unexpected error: %v", err)
		}
		if captured.PromptKey != "main/scoped" || captured.Title != "Scoped Prompt" || captured.UpdatedBy != "editor" {
			t.Fatalf("Upsert() forwarded wrong params: %+v", captured)
		}
		if got == nil || got.ID != 8 || string(got.Tags) != `["scope.cwd:/repo"]` {
			t.Fatalf("Upsert() mapped row incorrectly: %+v", got)
		}
	})

	t.Run("delete treats rows affected as success", func(t *testing.T) {
		var capturedKey string
		s := &store{q: &promptQuerierStub{
			deleteFn: func(_ context.Context, promptKey string) (int64, error) {
				capturedKey = promptKey
				return 1, nil
			},
		}}

		if err := s.Delete(context.Background(), "main/scoped"); err != nil {
			t.Fatalf("Delete() unexpected error: %v", err)
		}
		if capturedKey != "main/scoped" {
			t.Fatalf("Delete() prompt key = %q, want main/scoped", capturedKey)
		}
	})

	t.Run("insert version forwards params", func(t *testing.T) {
		var captured sqlc.InsertPromptVersionParams
		s := &store{q: &promptQuerierStub{
			insertVersionFn: func(_ context.Context, arg sqlc.InsertPromptVersionParams) (int64, error) {
				captured = arg
				return 42, nil
			},
		}}

		sourceUpdatedAt := now.Add(2 * time.Minute)
		id, err := s.InsertVersion(context.Background(), PromptTemplateVersion{
			PromptKey:       "main/scoped",
			Title:           "Scoped Prompt",
			AgentKey:        "main",
			ToolName:        "tool",
			PromptText:      "body",
			Variables:       []byte(`{"lang":"go"}`),
			Tags:            []byte(`["scope.cwd:/repo"]`),
			Description:     "desc",
			Enabled:         true,
			CreatedBy:       "creator",
			UpdatedBy:       "editor",
			SourceUpdatedAt: &sourceUpdatedAt,
		})
		if err != nil {
			t.Fatalf("InsertVersion() unexpected error: %v", err)
		}
		if id != 42 {
			t.Fatalf("InsertVersion() returned wrong id: got %d want 42", id)
		}
		if captured.PromptKey != "main/scoped" || captured.ToolName != "tool" || captured.SourceUpdatedAt == nil || !captured.SourceUpdatedAt.Equal(sourceUpdatedAt) {
			t.Fatalf("InsertVersion() forwarded wrong params: %+v", captured)
		}
	})

	t.Run("with tx executes callback", func(t *testing.T) {
		s := &store{q: &promptQuerierStub{}}
		callbackCalls := 0

		err := s.WithTx(context.Background(), func(txStore Store) error {
			callbackCalls++
			if txStore == nil {
				t.Fatal("WithTx() txStore = nil")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("WithTx() unexpected error: %v", err)
		}
		if callbackCalls != 1 {
			t.Fatalf("WithTx() callbackCalls = %d, want 1", callbackCalls)
		}
	})

	t.Run("delete wraps not found", func(t *testing.T) {
		s := &store{q: &promptQuerierStub{
			deleteFn: func(context.Context, string) (int64, error) {
				return 0, nil
			},
		}}
		err := s.Delete(context.Background(), "missing")
		if err == nil || !platformdb.IsNotFound(err) {
			t.Fatalf("Delete() error = %v, want not found", err)
		}
	})
}
