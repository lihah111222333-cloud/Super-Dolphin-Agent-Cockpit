package thread

import (
	"context"
	"strings"
	"testing"
)

func filterFakePromptTemplatesByCWD(templates []PromptTemplate, cwd string) []PromptTemplate {
	cwd = strings.TrimSpace(cwd)
	out := make([]PromptTemplate, 0, len(templates))
	for _, template := range templates {
		tags := runtimePromptTemplateTags(template.Tags)
		if cwd == "" || promptTemplateVisibleInCWD(tags, cwd) {
			out = append(out, template)
		}
	}
	return out
}

func promptTemplateVisibleInCWD(tags []string, cwd string) bool {
	hasScopedTag := false
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		switch {
		case tag == "scope.global":
			return true
		case strings.HasPrefix(tag, "scope.cwd:"):
			hasScopedTag = true
			if strings.TrimPrefix(tag, "scope.cwd:") == cwd {
				return true
			}
		}
	}
	return !hasScopedTag
}

func TestResolveRouterPromptListPassesTrustedCWD(t *testing.T) {
	t.Parallel()
	store := &fakePromptCatalog{
		templates: []PromptTemplate{
			sqlTemplate("repo-a/launch", "main", "repo body", []string{"scope.cwd:/repo/a"}),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "repo-a/launch"}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}

	if len(store.listFilters) != 1 {
		t.Fatalf("List called %d times, want 1", len(store.listFilters))
	}
	filter := store.listFilters[0]
	if filter.CWD != "/repo/a" || filter.Limit != 200 {
		t.Fatalf("List filter = %+v, want CWD=/repo/a Limit=200", filter)
	}
	if req.PromptKey != "repo-a/launch" || req.BaseInstructions != "repo body" {
		t.Fatalf("prompt not injected from scoped list: %+v", req)
	}
}

func TestResolveRoutedPromptRequiresTrustedCWDForCatalogRouting(t *testing.T) {
	t.Parallel()
	store := &fakePromptCatalog{
		templates: []PromptTemplate{
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{Prompt: "hello"}
	err := s.resolveRoutedPrompt(context.Background(), req)
	if err == nil {
		t.Fatal("resolveRoutedPrompt() error = nil, want trusted cwd error")
	}
	if !strings.Contains(err.Error(), "invalid params") || !strings.Contains(err.Error(), "cwd") {
		t.Fatalf("error = %v, want invalid params trusted cwd error", err)
	}
	if len(store.listFilters) != 0 {
		t.Fatalf("List called before cwd validation: %+v", store.listFilters)
	}
}

func TestResolveRoutedPrompt_PromptKeyRequiresTrustedCWD(t *testing.T) {
	t.Parallel()
	store := &fakePromptCatalog{
		templates: []PromptTemplate{
			sqlTemplate("main/launch-fav", "main", "fav body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{PromptKey: "main/launch-fav"}
	err := s.resolveRoutedPrompt(context.Background(), req)
	if err == nil {
		t.Fatal("resolveRoutedPrompt() error = nil, want invalid params")
	}
	if !strings.Contains(err.Error(), "invalid params") || !strings.Contains(err.Error(), "cwd") {
		t.Fatalf("error = %v, want invalid params trusted cwd error", err)
	}
	if len(store.listFilters) != 0 {
		t.Fatalf("List called before cwd validation: %+v", store.listFilters)
	}
	if req.BaseInstructions != "" || req.AgentKey != "" || req.PromptVersionID != nil {
		t.Fatalf("invalid params path must not inject prompt: %+v", req)
	}
}

func TestResolveRoutedPrompt_PromptKeyCrossCWDMissMarksStale(t *testing.T) {
	t.Parallel()
	store := &fakePromptCatalog{
		templates: []PromptTemplate{
			sqlTemplate("repo-b/launch", "main", "repo b body", []string{"scope.cwd:/repo/b"}),
			sqlTemplate(defaultPromptKey, "main", "default body", []string{"scope.global"}),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "repo-b/launch"}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}
	if !req.PromptKeyStale {
		t.Fatalf("cross-CWD prompt_key miss must mark stale: %+v", req)
	}
	if req.BaseInstructions != "" || req.AgentKey != "" || req.PromptVersionID != nil {
		t.Fatalf("cross-CWD miss must not fall back or inject: %+v", req)
	}
}

func TestResolveRoutedPrompt_PromptKeyRuntimeAssetMarksStale(t *testing.T) {
	t.Parallel()
	store := &fakePromptCatalog{
		templates: []PromptTemplate{
			sqlTemplate("repo-a/recall", "main", "recall body", []string{"scope.cwd:/repo/a", "intent:recall"}),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "repo-a/recall"}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}
	if !req.PromptKeyStale {
		t.Fatalf("runtime asset prompt_key must mark stale for UI cleanup: %+v", req)
	}
	if req.BaseInstructions != "" || req.AgentKey != "" || req.PromptVersionID != nil {
		t.Fatalf("runtime asset pin must not inject prompt: %+v", req)
	}
}

func TestResolveRoutedPrompt_PromptKeyRecallAssetMarksStale(t *testing.T) {
	t.Parallel()
	tpl := sqlTemplate("repo-a/recall", "main", "recall prompt text must not launch", []string{"scope.cwd:/repo/a", "intent:recall"})
	tpl.ID = 81
	store := &fakePromptCatalog{
		templates: []PromptTemplate{tpl},
		sectionsByTemplateID: map[int64][]PromptTemplateSection{
			81: {
				{
					TemplateID:  81,
					SectionKey:  "recall_pricing",
					TriggerType: "recall",
					RecallTopic: "pricing-table",
					Enabled:     true,
				},
			},
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{CWD: "/repo/a", PromptKey: "repo-a/recall"}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}
	if !req.PromptKeyStale {
		t.Fatalf("recall asset prompt_key must mark stale for UI cleanup: %+v", req)
	}
	if req.BaseInstructions != "" || req.AgentKey != "" || req.PromptVersionID != nil {
		t.Fatalf("recall asset pin must not inject prompt: %+v", req)
	}
}
