package prompt

import (
	"context"
	"encoding/json"
	"testing"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestPromptIntentDraftUpsertForwardsParamsAndMapsRow(t *testing.T) {
	t.Parallel()

	var captured sqlc.UpsertPromptIntentDraftParams
	s := &store{q: &promptQuerierStub{
		upsertDraftFn: func(_ context.Context, arg sqlc.UpsertPromptIntentDraftParams) (sqlc.UpsertPromptIntentDraftRow, error) {
			captured = arg
			row := promptIntentDraftRow(arg.DraftKey, arg.CWD, arg.Kind, arg.Status)
			row.Scope = arg.Scope
			return row, nil
		},
	}}

	got, err := s.UpsertIntentDraft(context.Background(), promptIntentDraftInput())
	if err != nil {
		t.Fatalf("UpsertIntentDraft() unexpected error: %v", err)
	}
	if captured.DraftKey != "draft-1" || captured.CWD != "/repo/a" || captured.Kind != "recall" || captured.Status != "ready_to_save" || captured.Scope != "global" {
		t.Fatalf("UpsertIntentDraft() params = %+v, want scoped ready recall", captured)
	}
	if string(captured.GeneratedCard) != `{"title":"SQLC"}` || string(captured.Issues) != `[]` {
		t.Fatalf("UpsertIntentDraft() json params card=%s issues=%s", captured.GeneratedCard, captured.Issues)
	}
	assertPromptIntentDraft(t, got, "draft-1", "/repo/a", "recall", "ready_to_save", "global")
}

func TestPromptIntentDraftUpsertRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	called := false
	s := &store{q: &promptQuerierStub{
		upsertDraftFn: func(context.Context, sqlc.UpsertPromptIntentDraftParams) (sqlc.UpsertPromptIntentDraftRow, error) {
			called = true
			return sqlc.UpsertPromptIntentDraftRow{}, nil
		},
	}}
	draft := promptIntentDraftInput()
	draft.CWD = " "
	_, err := s.UpsertIntentDraft(context.Background(), draft)
	if err == nil {
		t.Fatal("UpsertIntentDraft() expected validation error, got nil")
	}
	if called {
		t.Fatal("UpsertIntentDraft() called sqlc despite invalid input")
	}
}

func TestPromptIntentDraftGetRequiresCWD(t *testing.T) {
	t.Parallel()

	called := false
	s := &store{q: &promptQuerierStub{
		getDraftFn: func(context.Context, sqlc.GetPromptIntentDraftParams) (sqlc.GetPromptIntentDraftRow, error) {
			called = true
			return sqlc.GetPromptIntentDraftRow{}, nil
		},
	}}
	_, err := s.GetIntentDraft(context.Background(), "", "draft-1")
	if err == nil {
		t.Fatal("GetIntentDraft() expected empty cwd error, got nil")
	}
	if called {
		t.Fatal("GetIntentDraft() called sqlc despite empty cwd")
	}
}

func TestPromptIntentDraftGetForwardsCWDAndDraftKey(t *testing.T) {
	t.Parallel()

	var captured sqlc.GetPromptIntentDraftParams
	s := &store{q: &promptQuerierStub{
		getDraftFn: func(_ context.Context, arg sqlc.GetPromptIntentDraftParams) (sqlc.GetPromptIntentDraftRow, error) {
			captured = arg
			return sqlc.GetPromptIntentDraftRow(promptIntentDraftRow(arg.DraftKey, arg.CWD, "expert", "draft")), nil
		},
	}}
	got, err := s.GetIntentDraft(context.Background(), " /repo/a ", " draft-1 ")
	if err != nil {
		t.Fatalf("GetIntentDraft() unexpected error: %v", err)
	}
	if captured.CWD != "/repo/a" || captured.DraftKey != "draft-1" {
		t.Fatalf("GetIntentDraft() params = %+v, want trimmed scope", captured)
	}
	assertPromptIntentDraft(t, got, "draft-1", "/repo/a", "expert", "draft", "project")
}

func TestPromptIntentDraftListFiltersByCWDAndStatus(t *testing.T) {
	t.Parallel()

	var captured sqlc.ListPromptIntentDraftsParams
	s := &store{q: &promptQuerierStub{
		listDraftsFn: func(_ context.Context, arg sqlc.ListPromptIntentDraftsParams) ([]sqlc.ListPromptIntentDraftsRow, error) {
			captured = arg
			return []sqlc.ListPromptIntentDraftsRow{
				sqlc.ListPromptIntentDraftsRow(promptIntentDraftRow("draft-1", arg.CWD, "recall", arg.Status.(string))),
			}, nil
		},
	}}
	got, err := s.ListIntentDrafts(context.Background(), PromptIntentDraftListFilter{CWD: " /repo/a ", Status: " ready_to_save ", Limit: 20})
	if err != nil {
		t.Fatalf("ListIntentDrafts() unexpected error: %v", err)
	}
	if captured.CWD != "/repo/a" || captured.Status != "ready_to_save" || captured.LimitCount != 20 {
		t.Fatalf("ListIntentDrafts() params = %+v, want scoped status filter", captured)
	}
	if len(got) != 1 {
		t.Fatalf("ListIntentDrafts() len = %d, want 1", len(got))
	}
	assertPromptIntentDraft(t, &got[0], "draft-1", "/repo/a", "recall", "ready_to_save", "project")
}

func TestPromptIntentDraftListRequiresCWD(t *testing.T) {
	t.Parallel()

	s := &store{q: &promptQuerierStub{}}
	_, err := s.ListIntentDrafts(context.Background(), PromptIntentDraftListFilter{CWD: "", Limit: 20})
	if err == nil {
		t.Fatal("ListIntentDrafts() expected empty cwd error, got nil")
	}
}

func TestPromptIntentDraftUpdateStatusRequiresCWD(t *testing.T) {
	t.Parallel()

	called := false
	s := &store{q: &promptQuerierStub{
		updateDraftStatusFn: func(context.Context, sqlc.UpdatePromptIntentDraftStatusParams) (sqlc.UpdatePromptIntentDraftStatusRow, error) {
			called = true
			return sqlc.UpdatePromptIntentDraftStatusRow{}, nil
		},
	}}
	_, err := s.UpdateIntentDraftStatus(context.Background(), "", "draft-1", "enabled")
	if err == nil {
		t.Fatal("UpdateIntentDraftStatus() expected empty cwd error, got nil")
	}
	if called {
		t.Fatal("UpdateIntentDraftStatus() called sqlc despite empty cwd")
	}
}

func TestPromptIntentDraftUpdateStatusForwardsScope(t *testing.T) {
	t.Parallel()

	var captured sqlc.UpdatePromptIntentDraftStatusParams
	s := &store{q: &promptQuerierStub{
		updateDraftStatusFn: func(_ context.Context, arg sqlc.UpdatePromptIntentDraftStatusParams) (sqlc.UpdatePromptIntentDraftStatusRow, error) {
			captured = arg
			return sqlc.UpdatePromptIntentDraftStatusRow(promptIntentDraftRow(arg.DraftKey, arg.CWD, "default_rule", arg.Status)), nil
		},
	}}
	got, err := s.UpdateIntentDraftStatus(context.Background(), " /repo/a ", " draft-1 ", " enabled ")
	if err != nil {
		t.Fatalf("UpdateIntentDraftStatus() unexpected error: %v", err)
	}
	if captured.CWD != "/repo/a" || captured.DraftKey != "draft-1" || captured.Status != "enabled" {
		t.Fatalf("UpdateIntentDraftStatus() params = %+v, want trimmed scope and status", captured)
	}
	assertPromptIntentDraft(t, got, "draft-1", "/repo/a", "default_rule", "enabled", "project")
}

func promptIntentDraftInput() PromptIntentDraft {
	return PromptIntentDraft{
		DraftKey:      " draft-1 ",
		CWD:           " /repo/a ",
		Kind:          "recall",
		RawInput:      " Save SQLC workflow as recall. ",
		SourceType:    "user_input",
		GeneratedCard: json.RawMessage(`{"title":"SQLC"}`),
		Status:        "ready_to_save",
		Scope:         "global",
	}
}

func promptIntentDraftRow(draftKey, cwd, kind, status string) sqlc.UpsertPromptIntentDraftRow {
	now := platformdb.Millis(promptStoreTestTime())
	return sqlc.UpsertPromptIntentDraftRow{
		ID:            42,
		DraftKey:      draftKey,
		CWD:           cwd,
		Kind:          kind,
		RawInput:      "Save SQLC workflow as recall.",
		SourceType:    "user_input",
		GeneratedCard: []byte(`{"title":"SQLC"}`),
		Confidence:    0.8,
		Status:        status,
		Scope:         "project",
		Issues:        []byte(`[]`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func assertPromptIntentDraft(t *testing.T, got *PromptIntentDraft, draftKey, cwd, kind, status, scope string) {
	t.Helper()
	if got == nil {
		t.Fatal("PromptIntentDraft = nil")
	}
	if got.DraftKey != draftKey || got.CWD != cwd || got.Kind != kind || got.Status != status || got.Scope != scope {
		t.Fatalf("PromptIntentDraft = %+v, want key=%s cwd=%s kind=%s status=%s scope=%s", got, draftKey, cwd, kind, status, scope)
	}
	if !json.Valid(got.GeneratedCard) || !json.Valid(got.Issues) {
		t.Fatalf("PromptIntentDraft JSON invalid: card=%s issues=%s", got.GeneratedCard, got.Issues)
	}
}
