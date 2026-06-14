package prompt

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestCreatePromptTemplateConflictMapsToConflict(t *testing.T) {
	t.Parallel()

	s := &store{q: &promptQuerierStub{
		createFn: func(context.Context, sqlc.CreatePromptTemplateParams) (sqlc.CreatePromptTemplateRow, error) {
			return sqlc.CreatePromptTemplateRow{}, sql.ErrNoRows
		},
	}}
	_, err := s.CreatePromptTemplate(context.Background(), promptUpsertInput())
	if err == nil || !platformdb.IsConflict(err) {
		t.Fatalf("CreatePromptTemplate() error = %v, want conflict", err)
	}
}

func TestCreatePromptTemplateForwardsParamsAndMapsRow(t *testing.T) {
	t.Parallel()
	now := promptStoreTestTime()
	var captured sqlc.CreatePromptTemplateParams
	s := &store{q: &promptQuerierStub{
		createFn: func(_ context.Context, arg sqlc.CreatePromptTemplateParams) (sqlc.CreatePromptTemplateRow, error) {
			captured = arg
			return promptCreateRow(arg, now), nil
		},
	}}

	got, err := s.CreatePromptTemplate(context.Background(), promptUpsertInput())
	if err != nil {
		t.Fatalf("CreatePromptTemplate() unexpected error: %v", err)
	}
	if captured.PromptKey != "main/scoped" {
		t.Fatalf("CreatePromptTemplate() forwarded wrong params: %+v", captured)
	}
	if got == nil || got.PromptKey != "main/scoped" || got.WhenToUse != "Use when editing scoped prompts." {
		t.Fatalf("CreatePromptTemplate() mapped row incorrectly: %+v", got)
	}
}

func promptCreateRow(arg sqlc.CreatePromptTemplateParams, now time.Time) sqlc.CreatePromptTemplateRow {
	return sqlc.CreatePromptTemplateRow{
		ID:             8,
		PromptKey:      arg.PromptKey,
		Title:          arg.Title,
		AgentKey:       arg.AgentKey,
		ToolName:       arg.ToolName,
		PromptText:     arg.PromptText,
		Variables:      arg.Variables,
		Tags:           arg.Tags,
		Description:    arg.Description,
		Enabled:        arg.Enabled,
		ManuallyEdited: arg.ManuallyEdited,
		CreatedBy:      arg.CreatedBy,
		UpdatedBy:      arg.UpdatedBy,
		CreatedAt:      platformdb.Millis(now),
		UpdatedAt:      platformdb.Millis(now),
		WhenToUse:      arg.WhenToUse,
	}
}

func TestStoreUpsertSectionForwardsTriggerTypeAndRecallTopic(t *testing.T) {
	t.Parallel()
	var captured sqlc.UpsertPromptTemplateSectionParams
	s := &store{q: &promptQuerierStub{
		upsertSectionFn: func(_ context.Context, arg sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error) {
			captured = arg
			return promptSectionRowFromUpsert(arg), nil
		},
	}}

	got, err := s.UpsertSection(context.Background(), PromptTemplateSection{
		TemplateID:  7,
		SectionKey:  "project_memory",
		Region:      "dynamic",
		Ordinal:     30,
		Body:        "body",
		EnableWhen:  []byte(`{"language":"zh"}`),
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "project-memory",
	})
	if err != nil {
		t.Fatalf("UpsertSection() unexpected error: %v", err)
	}
	if captured.TriggerType != "recall" || captured.RecallTopic != "project-memory" {
		t.Fatalf("UpsertSection() recall params = trigger:%q topic:%q, want recall/project-memory", captured.TriggerType, captured.RecallTopic)
	}
	if got.TriggerType != "recall" || got.RecallTopic != "project-memory" {
		t.Fatalf("UpsertSection() mapped recall fields = %+v", got)
	}
}

func TestStoreUpsertSectionDefaultsEmptyTriggerTypeToAlways(t *testing.T) {
	t.Parallel()
	var captured sqlc.UpsertPromptTemplateSectionParams
	s := &store{q: &promptQuerierStub{
		upsertSectionFn: func(_ context.Context, arg sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error) {
			captured = arg
			return promptSectionRowFromUpsert(arg), nil
		},
	}}

	got, err := s.UpsertSection(context.Background(), PromptTemplateSection{
		TemplateID: 7,
		SectionKey: "identity",
		Region:     "static",
		Body:       "body",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("UpsertSection() unexpected error: %v", err)
	}
	if captured.TriggerType != "always" || got.TriggerType != "always" {
		t.Fatalf("UpsertSection() trigger_type = captured %q got %q, want always", captured.TriggerType, got.TriggerType)
	}
}

func TestStoreUpsertSectionDefaultsEmptyEnableWhenToJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		enableWhen json.RawMessage
	}{
		{name: "nil", enableWhen: nil},
		{name: "empty", enableWhen: json.RawMessage("")},
		{name: "whitespace", enableWhen: json.RawMessage(" \t\r\n ")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured sqlc.UpsertPromptTemplateSectionParams
			s := &store{q: &promptQuerierStub{
				upsertSectionFn: func(_ context.Context, arg sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error) {
					captured = arg
					return promptSectionRowFromUpsert(arg), nil
				},
			}}

			got, err := s.UpsertSection(context.Background(), PromptTemplateSection{
				TemplateID:  7,
				SectionKey:  "identity",
				Region:      "static",
				Body:        "body",
				EnableWhen:  tt.enableWhen,
				Enabled:     true,
				TriggerType: "always",
			})
			if err != nil {
				t.Fatalf("UpsertSection() unexpected error: %v", err)
			}
			if captured.EnableWhen != "{}" {
				t.Fatalf("UpsertSection() enable_when = %q, want {}", captured.EnableWhen)
			}
			if got == nil || string(got.EnableWhen) != "{}" || !json.Valid(got.EnableWhen) {
				t.Fatalf("UpsertSection() mapped enable_when = %s, want valid empty object", got.EnableWhen)
			}
		})
	}
}

func TestStoreUpsertSectionRejectsInvalidEnableWhen(t *testing.T) {
	t.Parallel()
	called := false
	s := &store{q: &promptQuerierStub{
		upsertSectionFn: func(context.Context, sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error) {
			called = true
			return sqlc.PromptTemplateSection{}, nil
		},
	}}

	_, err := s.UpsertSection(context.Background(), PromptTemplateSection{
		TemplateID:  7,
		SectionKey:  "identity",
		Region:      "static",
		Body:        "body",
		EnableWhen:  json.RawMessage(`{"language":`),
		Enabled:     true,
		TriggerType: "always",
	})
	if err == nil {
		t.Fatal("UpsertSection() expected invalid enable_when error, got nil")
	}
	if !strings.Contains(err.Error(), "enable_when") || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("UpsertSection() error = %v, want clear enable_when JSON validation error", err)
	}
	if called {
		t.Fatal("UpsertSection() called sqlc despite invalid enable_when")
	}
}

func TestStoreUpsertSectionRejectsInvalidTriggerType(t *testing.T) {
	t.Parallel()
	called := false
	s := &store{q: &promptQuerierStub{
		upsertSectionFn: func(context.Context, sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error) {
			called = true
			return sqlc.PromptTemplateSection{}, nil
		},
	}}

	_, err := s.UpsertSection(context.Background(), PromptTemplateSection{
		TemplateID:  7,
		SectionKey:  "bad",
		Region:      "static",
		Body:        "body",
		Enabled:     true,
		TriggerType: "sometimes",
	})
	if err == nil {
		t.Fatal("UpsertSection() expected invalid trigger_type error, got nil")
	}
	if called {
		t.Fatal("UpsertSection() called sqlc despite invalid trigger_type")
	}
}

func TestStoreUpsertSectionRejectsInvalidRecallTopic(t *testing.T) {
	t.Parallel()
	called := false
	s := &store{q: &promptQuerierStub{
		upsertSectionFn: func(context.Context, sqlc.UpsertPromptTemplateSectionParams) (sqlc.PromptTemplateSection, error) {
			called = true
			return sqlc.PromptTemplateSection{}, nil
		},
	}}

	_, err := s.UpsertSection(context.Background(), PromptTemplateSection{
		TemplateID:  7,
		SectionKey:  "bad_recall",
		Region:      "dynamic",
		Body:        "body",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "project.memory",
	})
	if err == nil {
		t.Fatal("UpsertSection() expected invalid recall_topic error, got nil")
	}
	if called {
		t.Fatal("UpsertSection() called sqlc despite invalid recall_topic")
	}
}

func TestStoreListSectionsMapsTriggerTypeAndRecallTopic(t *testing.T) {
	t.Parallel()
	s := &store{q: &promptQuerierStub{
		listSectionsFn: func(_ context.Context, arg sqlc.ListPromptTemplateSectionsByTemplateParams) ([]sqlc.PromptTemplateSection, error) {
			if arg.TemplateID != 7 {
				t.Fatalf("ListPromptTemplateSectionsByTemplate template_id = %d, want 7", arg.TemplateID)
			}
			return []sqlc.PromptTemplateSection{{
				ID:          11,
				TemplateID:  7,
				SectionKey:  "project_memory",
				Region:      "dynamic",
				Body:        "body",
				Enabled:     int64(1),
				TriggerType: "recall",
				RecallTopic: "project-memory",
			}}, nil
		},
	}}

	got, err := s.ListSectionsByTemplateID(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListSectionsByTemplateID() unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].TriggerType != "recall" || got[0].RecallTopic != "project-memory" {
		t.Fatalf("ListSectionsByTemplateID() recall fields = %+v", got)
	}
}

func TestStoreListSectionsByTemplateIDsForwardsBatchIDsAndMapsRows(t *testing.T) {
	t.Parallel()

	var captured sqlc.ListPromptTemplateSectionsByTemplatesParams
	s := &store{q: &promptQuerierStub{
		listSectionsBatchFn: func(_ context.Context, arg sqlc.ListPromptTemplateSectionsByTemplatesParams) ([]sqlc.PromptTemplateSection, error) {
			captured = arg
			return []sqlc.PromptTemplateSection{
				{ID: 11, TemplateID: 7, SectionKey: "identity", Region: "static", Body: "identity", Enabled: int64(1), TriggerType: "always"},
				{ID: 12, TemplateID: 8, SectionKey: "workflow", Region: "dynamic", Body: "workflow", Enabled: int64(1), TriggerType: "keyword"},
			}, nil
		},
	}}

	got, err := s.ListSectionsByTemplateIDs(context.Background(), []int64{7, 8})
	if err != nil {
		t.Fatalf("ListSectionsByTemplateIDs() unexpected error: %v", err)
	}
	if len(captured.TemplateIds) != 2 || captured.TemplateIds[0] != 7 || captured.TemplateIds[1] != 8 {
		t.Fatalf("ListPromptTemplateSectionsByTemplates ids = %#v, want [7 8]", captured.TemplateIds)
	}
	if len(got) != 2 || got[0].TemplateID != 7 || got[1].TemplateID != 8 {
		t.Fatalf("ListSectionsByTemplateIDs() mapped rows = %+v", got)
	}
}

func TestStoreListSectionsByTemplateIDsSkipsEmptyInput(t *testing.T) {
	t.Parallel()

	called := false
	s := &store{q: &promptQuerierStub{
		listSectionsBatchFn: func(context.Context, sqlc.ListPromptTemplateSectionsByTemplatesParams) ([]sqlc.PromptTemplateSection, error) {
			called = true
			return nil, nil
		},
	}}

	got, err := s.ListSectionsByTemplateIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListSectionsByTemplateIDs() unexpected error: %v", err)
	}
	if called {
		t.Fatal("ListSectionsByTemplateIDs() called sqlc for empty input")
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ListSectionsByTemplateIDs() = %#v, want empty non-nil slice", got)
	}
}

func promptSectionRowFromUpsert(arg sqlc.UpsertPromptTemplateSectionParams) sqlc.PromptTemplateSection {
	return sqlc.PromptTemplateSection{
		ID:          11,
		TemplateID:  arg.TemplateID,
		SectionKey:  arg.SectionKey,
		Region:      arg.Region,
		Ordinal:     arg.Ordinal,
		Body:        arg.Body,
		EnableWhen:  arg.EnableWhen,
		Enabled:     arg.Enabled,
		TriggerType: arg.TriggerType,
		RecallTopic: arg.RecallTopic,
	}
}
