package thread

import (
	"context"
	"strings"
	"testing"

	promptpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt"
)

func TestResolveRoutedPromptUsesBuiltinDefaultWhenDBSeedMissing(t *testing.T) {
	t.Parallel()

	catalog := &fakePromptCatalog{
		readOnly: true,
		templates: []PromptTemplate{{
			ID: -100001, PromptKey: defaultPromptKey, Title: "Builtin Default", AgentKey: "main", Enabled: true,
		}},
		sectionsByTemplateID: map[int64][]PromptTemplateSection{
			-100001: {{ID: -200001, TemplateID: -100001, SectionKey: "identity", Region: "static", Ordinal: 0, Body: "Builtin default body", Enabled: true, TriggerType: "always"}},
		},
	}
	svc := &service{promptCatalog: catalog, enableWhenEval: promptpkg.EvaluateEnableWhen}
	req := &StartRequest{CWD: "/repo/a", Prompt: "hello"}

	err := svc.resolveRoutedPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("PromptKey = %q, want %q", req.PromptKey, defaultPromptKey)
	}
	if req.BaseInstructions != "" {
		t.Fatalf("BaseInstructions = %q, want structured builtin blocks", req.BaseInstructions)
	}
	if len(req.BaseInstructionBlocks) != 1 || req.BaseInstructionBlocks[0].Body != "Builtin default body" {
		t.Fatalf("BaseInstructionBlocks = %#v, want builtin default body", req.BaseInstructionBlocks)
	}
	if req.PromptVersionID != nil {
		t.Fatalf("PromptVersionID = %v, want nil when DB store is absent", req.PromptVersionID)
	}
}

func TestResolveRoutedPromptFailsWhenDefaultTemplateMissing(t *testing.T) {
	t.Parallel()

	store := &fakePromptCatalog{}
	svc := newServiceWithRouter(store)
	req := &StartRequest{CWD: "/repo/a", Prompt: "hello"}

	err := svc.resolveRoutedPrompt(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), defaultPromptKey) {
		t.Fatalf("resolveRoutedPrompt() error = %v, want missing %s error", err, defaultPromptKey)
	}
	if req.BaseInstructions != "" || len(req.BaseInstructionBlocks) != 0 {
		t.Fatalf("request mutated despite missing default: base=%q blocks=%#v", req.BaseInstructions, req.BaseInstructionBlocks)
	}
}

func TestResolveRoutedPromptPersistsSelectedTemplateVersion(t *testing.T) {
	t.Parallel()

	template := sqlTemplate(defaultPromptKey, "main", "Builtin prompt text", []string{"scope.global"})
	template.ID = -100001
	template.Title = "Builtin Default"
	catalog := &fakePromptCatalog{templates: []PromptTemplate{template}}
	svc := &service{promptCatalog: catalog, enableWhenEval: promptpkg.EvaluateEnableWhen}
	req := &StartRequest{CWD: "/repo/a", Prompt: "hello"}

	err := svc.resolveRoutedPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("PromptKey = %q, want %q", req.PromptKey, defaultPromptKey)
	}
	if req.AgentTitle != "Builtin Default" {
		t.Fatalf("AgentTitle = %q, want builtin title", req.AgentTitle)
	}
	if req.BaseInstructions != "Builtin prompt text" {
		t.Fatalf("BaseInstructions = %q, want builtin prompt text", req.BaseInstructions)
	}
	if catalog.lastInsertVersion.PromptText != "Builtin prompt text" {
		t.Fatalf("version prompt_text = %q, want builtin prompt text", catalog.lastInsertVersion.PromptText)
	}
}
