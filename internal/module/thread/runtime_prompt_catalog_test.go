package thread

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func TestResolveRoutedPromptUsesBuiltinDefaultWhenDBSeedMissing(t *testing.T) {
	t.Parallel()

	catalog := threadprompt.NewRuntimeCatalog(nil, &threadBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			{
				ID:        -100001,
				PromptKey: defaultPromptKey,
				Kind:      "base",
				Title:     "Builtin Default",
				AgentKey:  "main",
				Enabled:   true,
				Scope:     "global",
			},
		},
		sections: map[int64][]contract.BuiltinPromptSection{
			-100001: {
				{ID: -200001, TemplateID: -100001, SectionKey: "identity", Region: "static", Ordinal: 0, Body: "Builtin default body", Enabled: true, TriggerType: "always"},
			},
		},
	})
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

	store := &fakePromptStore{}
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

func TestResolveRoutedPromptDoesNotDuplicateRegistryBackedSystemSeed(t *testing.T) {
	t.Parallel()

	seed := sqlTemplate(defaultPromptKey, "main", "DB default body", []string{"scope.global"})
	seed.ID = 42
	seed.CreatedBy = "system.seed"
	seed.UpdatedBy = "system.seed"
	store := &fakePromptStore{templates: []promptstore.PromptTemplate{seed}}
	catalog := threadprompt.NewRuntimeCatalog(store, &threadBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			{
				ID:         -100001,
				PromptKey:  defaultPromptKey,
				Kind:       "base",
				Title:      "Builtin Default",
				AgentKey:   "main",
				PromptText: "Builtin prompt text",
				Enabled:    true,
				Scope:      "global",
			},
		},
	})
	svc := &service{promptStore: store, promptCatalog: catalog, enableWhenEval: promptpkg.EvaluateEnableWhen}
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
	if store.lastInsertVersion.PromptText != "Builtin prompt text" {
		t.Fatalf("version prompt_text = %q, want builtin prompt text", store.lastInsertVersion.PromptText)
	}
}
