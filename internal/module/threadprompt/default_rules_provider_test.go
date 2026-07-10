package threadprompt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestProjectDefaultRulesProviderRendersScopedRules(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		defaultRuleSectionsByCWD: map[string][]PromptTemplateSection{
			"/repo/a": {
				{Body: "涉及 sqlc drift 时先查源 SQL。", Enabled: true},
				{Body: "提交前跑 focused guard。", Enabled: true},
			},
			"/repo/b": {
				{Body: "Repo B only.", Enabled: true},
			},
		},
	}
	provider := ProjectDefaultRulesProvider{catalog: newRuntimeCatalog(store, nil)}
	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want default rules")
	}
	for _, want := range []string{
		"项目和全局默认规则：",
		"- 涉及 sqlc drift 时先查源 SQL。",
		"- 提交前跑 focused guard。",
		"这些规则只适用于当前项目",
	} {
		if !strings.Contains(*text, want) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, want)
		}
	}
	if strings.Contains(*text, "Repo B only.") {
		t.Fatalf("Resolve() = %q, want other cwd rules hidden", *text)
	}
	if len(store.defaultRuleCWDs) != 1 || store.defaultRuleCWDs[0] != "/repo/a" {
		t.Fatalf("ListDefaultRuleSections() cwd calls = %#v, want /repo/a", store.defaultRuleCWDs)
	}
}

func TestProjectDefaultRulesProviderRendersCurrentCWDAndGlobalOnly(t *testing.T) {
	t.Parallel()

	provider := ProjectDefaultRulesProvider{catalog: newRuntimeCatalog(&fakePromptStore{
		defaultRuleSectionsByCWD: map[string][]PromptTemplateSection{
			"/repo/a": {
				{SectionKey: "project", Body: "Project A rule.", TemplateTags: mustJSONTags("scope.cwd:/repo/a"), Enabled: true},
				{SectionKey: "global", Body: "Global rule.", TemplateTags: mustJSONTags("scope.global"), Enabled: true},
			},
			"/repo/b": {
				{SectionKey: "other", Body: "Project B rule.", TemplateTags: mustJSONTags("scope.cwd:/repo/b"), Enabled: true},
				{SectionKey: "disabled", Body: "Disabled project rule.", TemplateTags: mustJSONTags("scope.cwd:/repo/b"), Enabled: false},
			},
		},
	}, nil)}

	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want default rules")
	}
	for _, want := range []string{"Project A rule.", "Global rule."} {
		if !strings.Contains(*text, want) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, want)
		}
	}
	for _, absent := range []string{"Project B rule.", "Disabled project rule."} {
		if strings.Contains(*text, absent) {
			t.Fatalf("Resolve() = %q, want %q absent", *text, absent)
		}
	}
}

func TestProjectDefaultRulesProviderPrefersProjectRuleOverGlobalRule(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		defaultRuleSections: []PromptTemplateSection{
			{
				SectionKey:    "project_rule",
				TemplateTitle: "Focused Tests",
				Body:          "Global rule body.",
				TemplateTags:  mustJSONTags("scope.global"),
				Enabled:       true,
			},
			{
				SectionKey:    "project_rule",
				TemplateTitle: "Focused Tests",
				Body:          "Project rule body.",
				TemplateTags:  mustJSONTags("scope.cwd:/repo-a"),
				Enabled:       true,
			},
			{
				SectionKey:    "project_rule",
				TemplateTitle: "Security Review",
				Body:          "Global security rule.",
				TemplateTags:  mustJSONTags("scope.global"),
				Enabled:       true,
			},
		},
	}
	provider := ProjectDefaultRulesProvider{catalog: newRuntimeCatalog(store, nil)}

	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo-a"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want default rules")
	}
	for _, want := range []string{"Project rule body.", "Global security rule."} {
		if !strings.Contains(*text, want) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, want)
		}
	}
	if strings.Contains(*text, "Global rule body.") {
		t.Fatalf("Resolve() = %q, want overridden global rule hidden", *text)
	}
}

func TestProjectDefaultRulesProviderKeepsEffectiveRuleOrderStable(t *testing.T) {
	t.Parallel()

	sections := []PromptTemplateSection{
		{SectionKey: "alpha", Body: "Alpha rule.", TemplatePromptKey: "main/default-rule/alpha", TemplateTags: mustJSONTags("scope.global"), Enabled: true},
		{SectionKey: "bravo", Body: "Global bravo.", TemplatePromptKey: "main/default-rule/bravo-global", TemplateTags: mustJSONTags("scope.global"), Enabled: true},
		{SectionKey: "charlie", Body: "Charlie rule.", TemplatePromptKey: "main/default-rule/charlie", TemplateTags: mustJSONTags("scope.global"), Enabled: true},
		{SectionKey: "bravo", Body: "Project bravo.", TemplatePromptKey: "main/default-rule/bravo-project", TemplateTags: mustJSONTags("scope.cwd:/repo-a"), Enabled: true},
	}

	for range 20 {
		text := renderProjectDefaultRules(sections)
		requireContainsInOrder(t, text, "Alpha rule.", "Project bravo.", "Charlie rule.")
		if strings.Contains(text, "Global bravo.") {
			t.Fatalf("renderProjectDefaultRules() = %q, want overridden global rule hidden", text)
		}
	}
}

func TestProjectDefaultRulesProviderKeepsMultipleSectionsFromSameTemplate(t *testing.T) {
	t.Parallel()

	sections := []PromptTemplateSection{
		{
			SectionKey:    "sqlc_guard",
			TemplateTitle: "Project Defaults",
			Body:          "SQLC changes must verify generated drift.",
			TemplateTags:  mustJSONTags("scope.cwd:/repo-a"),
			Enabled:       true,
		},
		{
			SectionKey:    "migration_guard",
			TemplateTitle: "Project Defaults",
			Body:          "Database migrations must describe blast radius.",
			TemplateTags:  mustJSONTags("scope.cwd:/repo-a"),
			Enabled:       true,
		},
	}

	text := renderProjectDefaultRules(sections)

	requireContainsInOrder(t, text, "SQLC changes must verify generated drift.", "Database migrations must describe blast radius.")
}

func TestProjectDefaultRulesProviderEmptyReturnsNil(t *testing.T) {
	t.Parallel()

	provider := ProjectDefaultRulesProvider{catalog: newRuntimeCatalog(&fakePromptStore{}, nil)}
	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}})
	if err != nil || text != nil {
		t.Fatalf("Resolve() empty default rules = (%v, %v), want nil, nil", text, err)
	}
}

func TestProjectDefaultRulesProviderStoreErrorIsCritical(t *testing.T) {
	t.Parallel()

	provider := ProjectDefaultRulesProvider{catalog: newRuntimeCatalog(&fakePromptStore{defaultRuleErr: errors.New("db down")}, nil)}
	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}})
	if err == nil || text != nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() store error = (%v, %v), want critical error", text, err)
	}
}

func TestProjectDefaultRulesProviderMissingStoreFailsCritical(t *testing.T) {
	t.Parallel()

	provider := ProjectDefaultRulesProvider{}
	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}})
	if err == nil || text != nil || !contract.IsCriticalPromptSectionError(err) ||
		!strings.Contains(err.Error(), "runtime prompt catalog is required") {
		t.Fatalf("Resolve() missing store = (%v, %v), want critical error", text, err)
	}
}

func TestProjectDefaultRulesProviderMissingCWDFailsCritical(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{}
	provider := ProjectDefaultRulesProvider{catalog: newRuntimeCatalog(store, nil)}
	text, err := provider.Resolve(context.Background(), contract.SectionContext{})
	if err == nil || text != nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() missing cwd = (%v, %v), want critical error", text, err)
	}
	if len(store.defaultRuleCWDs) != 0 {
		t.Fatalf("ListDefaultRuleSections() cwd calls = %#v, want none", store.defaultRuleCWDs)
	}
}
