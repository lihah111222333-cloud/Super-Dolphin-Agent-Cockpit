package prompt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestStoreListRecallSectionsMapsRows(t *testing.T) {
	t.Parallel()

	s := &store{q: &promptQuerierStub{
		listRecallFn: func(_ context.Context, arg sqlc.ListRecallSectionsParams) ([]sqlc.ListRecallSectionsRow, error) {
			if arg.CWD == nil || *arg.CWD != "/repo/a" {
				t.Fatalf("ListRecallSections() cwd = %v, want /repo/a", arg.CWD)
			}
			return []sqlc.ListRecallSectionsRow{{
				ID:                  11,
				TemplateID:          7,
				SectionKey:          "recall_sqlc",
				Region:              "dynamic",
				Ordinal:             0,
				EnableWhen:          `{"language":"zh"}`,
				Enabled:             int64(1),
				TriggerType:         "recall",
				RecallTopic:         "sqlc-workflow",
				TemplatePromptKey:   "main/general-zh",
				TemplateTitle:       "General zh",
				TemplateDescription: "SQLC workflow metadata summary",
				TemplateWhenToUse:   "Use when editing sqlc queries",
			}}, nil
		},
	}}

	got, err := s.ListRecallSections(context.Background(), " /repo/a ")
	if err != nil {
		t.Fatalf("ListRecallSections() unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListRecallSections() len = %d, want 1", len(got))
	}
	section := got[0]
	assertRecallSectionField(t, "id", section.ID, int64(11), section)
	assertRecallSectionField(t, "template_id", section.TemplateID, int64(7), section)
	assertRecallSectionField(t, "section_key", section.SectionKey, "recall_sqlc", section)
	assertRecallSectionField(t, "trigger_type", section.TriggerType, "recall", section)
	assertRecallSectionField(t, "recall_topic", section.RecallTopic, "sqlc-workflow", section)
	assertRecallSectionField(t, "template_prompt_key", section.TemplatePromptKey, "main/general-zh", section)
	assertRecallSectionField(t, "template_title", section.TemplateTitle, "General zh", section)
	assertRecallSectionField(t, "template_description", section.TemplateDescription, "SQLC workflow metadata summary", section)
	assertRecallSectionField(t, "template_when_to_use", section.TemplateWhenToUse, "Use when editing sqlc queries", section)
	assertRecallSectionField(t, "body", section.Body, "", section)
	if string(section.EnableWhen) != `{"language":"zh"}` || !json.Valid(section.EnableWhen) {
		t.Fatalf("ListRecallSections() enable_when = %s, want valid JSON", section.EnableWhen)
	}
}

func TestStoreListRecallSectionsRequiresCWD(t *testing.T) {
	t.Parallel()

	called := false
	s := &store{q: &promptQuerierStub{
		listRecallFn: func(context.Context, sqlc.ListRecallSectionsParams) ([]sqlc.ListRecallSectionsRow, error) {
			called = true
			return nil, nil
		},
	}}

	_, err := s.ListRecallSections(context.Background(), " ")
	if err == nil {
		t.Fatal("ListRecallSections() expected empty cwd error, got nil")
	}
	if called {
		t.Fatal("ListRecallSections() called sqlc despite empty cwd")
	}
}

func TestStoreListDefaultRuleSectionsMapsRows(t *testing.T) {
	t.Parallel()

	s := &store{q: &promptQuerierStub{
		listDefaultRulesFn: func(_ context.Context, arg sqlc.ListDefaultRuleSectionsParams) ([]sqlc.ListDefaultRuleSectionsRow, error) {
			if arg.CWD == nil || *arg.CWD != "/repo/a" {
				t.Fatalf("ListDefaultRuleSections() cwd = %v, want /repo/a", arg.CWD)
			}
			return []sqlc.ListDefaultRuleSectionsRow{{
				ID:                12,
				TemplateID:        8,
				SectionKey:        "default_rule_body",
				Region:            "dynamic",
				Ordinal:           1,
				Body:              "Default rule body",
				EnableWhen:        `{"language":"zh"}`,
				Enabled:           int64(1),
				TriggerType:       "always",
				TemplatePromptKey: "main/default-rule/sqlc",
			}}, nil
		},
	}}

	got, err := s.ListDefaultRuleSections(context.Background(), "/repo/a")
	if err != nil {
		t.Fatalf("ListDefaultRuleSections() unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListDefaultRuleSections() len = %d, want 1", len(got))
	}
	section := got[0]
	assertRecallSectionField(t, "id", section.ID, int64(12), section)
	assertRecallSectionField(t, "section_key", section.SectionKey, "default_rule_body", section)
	assertRecallSectionField(t, "template_prompt_key", section.TemplatePromptKey, "main/default-rule/sqlc", section)
	if string(section.EnableWhen) != `{"language":"zh"}` || !json.Valid(section.EnableWhen) {
		t.Fatalf("ListDefaultRuleSections() enable_when = %s, want valid JSON", section.EnableWhen)
	}
}

func TestLockRecallTopicInCWDRequiresCWDAndTopic(t *testing.T) {
	t.Parallel()

	called := false
	s := &store{q: &promptQuerierStub{
		lockRecallFn: func(context.Context) error {
			called = true
			return nil
		},
	}}
	if err := s.LockRecallTopicInCWD(context.Background(), "", "topic"); err == nil {
		t.Fatal("LockRecallTopicInCWD() expected empty cwd error, got nil")
	}
	if err := s.LockRecallTopicInCWD(context.Background(), "/repo/a", " "); err == nil {
		t.Fatal("LockRecallTopicInCWD() expected empty topic error, got nil")
	}
	if called {
		t.Fatal("LockRecallTopicInCWD() called sqlc despite invalid input")
	}
}

func TestLockRecallTopicInCWDCallsPlaceholderAfterValidation(t *testing.T) {
	t.Parallel()

	called := false
	s := &store{q: &promptQuerierStub{
		lockRecallFn: func(context.Context) error {
			called = true
			return nil
		},
	}}
	if err := s.LockRecallTopicInCWD(context.Background(), " /repo/a ", " topic "); err != nil {
		t.Fatalf("LockRecallTopicInCWD() unexpected error: %v", err)
	}
	if !called {
		t.Fatal("LockRecallTopicInCWD() did not call placeholder lock")
	}
}

func assertRecallSectionField[T comparable](t *testing.T, field string, got, want T, section PromptTemplateSection) {
	t.Helper()
	if got != want {
		t.Fatalf("ListRecallSections() %s = %v, want %v: %+v", field, got, want, section)
	}
}
