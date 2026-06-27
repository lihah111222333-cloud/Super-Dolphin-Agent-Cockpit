package threadprompt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func TestRuntimeCatalogGetTemplateWithEmptyCWDSkipsStore(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{getErr: errors.New("store get should not be called")}

	catalog := newRuntimeCatalogForStore(store, nil)
	_, err := catalog.GetTemplate(context.Background(), "main/db-global", "")
	if err == nil {
		t.Fatal("GetTemplate() error = nil, want not found without trusted cwd")
	}
	if strings.Contains(err.Error(), "store get should not be called") {
		t.Fatalf("GetTemplate() error = %v, want no DB read without trusted cwd", err)
	}
}

func TestRuntimeCatalogGetTemplateWithEmptyCWDCanReadBuiltin(t *testing.T) {
	t.Parallel()

	builtin := &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		{
			ID:        -9,
			PromptKey: "main/default",
			Title:     "Builtin Default",
			AgentKey:  "main",
			Enabled:   true,
			Scope:     "global",
		},
	}}
	store := &fakePromptStore{getTemplates: map[string]promptstore.PromptTemplate{
		"main/default": {ID: 1, PromptKey: "main/default", Title: "DB Default", Tags: mustJSONTags("scope.global"), Enabled: true},
	}, getErr: errors.New("store get should not be called")}

	catalog := newRuntimeCatalogForStore(store, builtin)
	got, err := catalog.GetTemplate(context.Background(), "main/default", "")
	if err != nil {
		t.Fatalf("GetTemplate() error = %v", err)
	}
	if got.ID != -9 || got.Title != "Builtin Default" {
		t.Fatalf("GetTemplate() = %#v, want builtin template", *got)
	}
}

func TestRuntimeCatalogListTemplatesInfersRecallIntentFromRecallOnlySections(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			{
				ID:        41,
				PromptKey: "main/knowledge/sqlc",
				Title:     "SQLC Knowledge",
				AgentKey:  "main",
				WhenToUse: "Use when SQLC workflow details are needed.",
				Tags:      mustJSONTags("scope.cwd:/repo/a"),
				Enabled:   true,
			},
		},
		sectionsByTemplateID: map[int64][]promptstore.PromptTemplateSection{
			41: {
				{
					TemplateID:  41,
					SectionKey:  "recall_sqlc",
					TriggerType: "recall",
					RecallTopic: "sqlc-workflow",
					Enabled:     true,
				},
			},
		},
	}

	catalog := newRuntimeCatalogForStore(store, nil)
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{CWD: "/repo/a"})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	got := runtimeCatalogTemplateByKey(templates, "main/knowledge/sqlc")
	if got == nil {
		t.Fatalf("ListTemplates() = %#v, want section-only recall template", templates)
	}
	if !runtimeCatalogStringSliceContains(promptstore.TemplateTags(got.Tags), "intent:recall") {
		t.Fatalf("template tags = %#v, want inferred intent:recall", promptstore.TemplateTags(got.Tags))
	}
}

func TestRuntimeCatalogListTemplatesDoesNotInferRecallWhenInjectableSectionExists(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			{
				ID:        42,
				PromptKey: "main/expert/sqlc",
				Title:     "SQLC Expert",
				AgentKey:  "main",
				WhenToUse: "Use for SQLC implementation tasks.",
				Tags:      mustJSONTags("scope.cwd:/repo/a"),
				Enabled:   true,
			},
		},
		sectionsByTemplateID: map[int64][]promptstore.PromptTemplateSection{
			42: {
				{TemplateID: 42, SectionKey: "workflow", TriggerType: "always", Body: "Implement SQLC changes.", Enabled: true},
				{TemplateID: 42, SectionKey: "recall_sqlc", TriggerType: "recall", RecallTopic: "sqlc-workflow", Enabled: true},
			},
		},
	}

	catalog := newRuntimeCatalogForStore(store, nil)
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{CWD: "/repo/a"})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	got := runtimeCatalogTemplateByKey(templates, "main/expert/sqlc")
	if got == nil {
		t.Fatalf("ListTemplates() = %#v, want expert template", templates)
	}
	if runtimeCatalogStringSliceContains(promptstore.TemplateTags(got.Tags), "intent:recall") {
		t.Fatalf("template tags = %#v, want expert with injectable section to stay launchable", promptstore.TemplateTags(got.Tags))
	}
}

func TestRuntimeCatalogListTemplatesIgnoresDisabledInjectableSectionWhenInferringRecall(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			{
				ID:        43,
				PromptKey: "main/knowledge/sqlc-disabled-workflow",
				Title:     "SQLC Knowledge",
				AgentKey:  "main",
				WhenToUse: "Use when SQLC workflow details are needed.",
				Tags:      mustJSONTags("scope.cwd:/repo/a"),
				Enabled:   true,
			},
		},
		sectionsByTemplateID: map[int64][]promptstore.PromptTemplateSection{
			43: {
				{TemplateID: 43, SectionKey: "workflow", TriggerType: "always", Body: "Disabled workflow.", Enabled: false},
				{TemplateID: 43, SectionKey: "recall_sqlc", TriggerType: "recall", RecallTopic: "sqlc-workflow", Enabled: true},
			},
		},
	}

	catalog := newRuntimeCatalogForStore(store, nil)
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{CWD: "/repo/a"})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	got := runtimeCatalogTemplateByKey(templates, "main/knowledge/sqlc-disabled-workflow")
	if got == nil || !runtimeCatalogStringSliceContains(promptstore.TemplateTags(got.Tags), "intent:recall") {
		t.Fatalf("template = %#v, want disabled injectable section ignored and intent:recall inferred", got)
	}
}

func TestAvailableExpertsExcludesSectionOnlyRecallAssets(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			{
				ID:        71,
				PromptKey: "main/knowledge/pricing",
				Title:     "Pricing Knowledge",
				AgentKey:  "main",
				WhenToUse: "Use when pricing details are needed.",
				Tags:      mustJSONTags("scope.cwd:/repo/a"),
				Enabled:   true,
			},
		},
		sectionsByTemplateID: map[int64][]promptstore.PromptTemplateSection{
			71: {
				{
					TemplateID:  71,
					SectionKey:  "recall_pricing",
					TriggerType: "recall",
					RecallTopic: "pricing-table",
					Enabled:     true,
				},
			},
		},
	}
	provider := AvailableExpertsProvider{catalog: newRuntimeCatalogForStore(store, nil)}

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{Prompt: "查询价格"},
		BuildCtx: contract.BuildCtx{CWD: "/repo/a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want section-only recall asset hidden from available experts", *text)
	}
}
