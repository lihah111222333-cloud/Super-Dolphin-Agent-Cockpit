package thread

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"go.uber.org/fx"
)

func TestRegisterThreadPromptProvidersRegistersAvailableExpertsAndRecallCatalog(t *testing.T) {
	t.Parallel()

	registrar := &capturingDynamicRegistrar{}
	if err := registerThreadPromptProviders(threadPromptProviderParams{
		Registrar:   registrar,
		PromptStore: &fakePromptStore{},
	}); err != nil {
		t.Fatalf("registerThreadPromptProviders() error = %v", err)
	}
	want := []string{
		contract.DynamicSectionProjectDefaultRules,
		contract.DynamicSectionAvailableExperts,
		contract.DynamicSectionRecallCatalog,
	}
	if !slicesEqual(registrar.names, want) {
		t.Fatalf("registered names = %#v, want %#v", registrar.names, want)
	}
}

func TestRegisterThreadPromptProvidersUsesBuiltinRegistry(t *testing.T) {
	t.Parallel()

	registrar := &capturingDynamicRegistrar{}
	if err := registerThreadPromptProviders(threadPromptProviderParams{
		Registrar: registrar,
		Builtin: &threadBuiltinPromptRegistry{
			templates: []contract.BuiltinPromptTemplate{{
				ID:        -700,
				PromptKey: "main/expert/builtin",
				Title:     "Builtin Expert",
				AgentKey:  "main",
				WhenToUse: "Use builtin expert.",
				Tags:      []string{"intent:expert"},
				Enabled:   true,
				Scope:     "global",
			}},
		},
	}); err != nil {
		t.Fatalf("registerThreadPromptProviders() error = %v", err)
	}
	provider := registrar.providers[contract.DynamicSectionAvailableExperts]
	if provider == nil {
		t.Fatalf("registered providers = %#v, want available experts provider", registrar.providers)
	}
	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{Prompt: "需要专家", CWD: "/repo/a"},
	})
	if err != nil || text == nil || !strings.Contains(*text, "main/expert/builtin") {
		t.Fatalf("Resolve() = (%v, %v), want builtin expert rendered", text, err)
	}
}

func TestThreadModuleRegistersPromptProvidersViaFx(t *testing.T) {
	t.Parallel()

	registrar := &capturingDynamicRegistrar{}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() *slog.Logger {
				return slog.New(slog.NewTextHandler(io.Discard, nil))
			},
			func() promptstore.Store {
				return &fakePromptStore{}
			},
			func() contract.DynamicSectionRegistrar {
				return registrar
			},
		),
		Module,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	want := []string{
		contract.DynamicSectionProjectDefaultRules,
		contract.DynamicSectionAvailableExperts,
		contract.DynamicSectionRecallCatalog,
	}
	if !slicesEqual(registrar.names, want) {
		t.Fatalf("registered names = %#v, want %#v", registrar.names, want)
	}
}

func TestBuildStartAssemblyInputCarriesPromptKey(t *testing.T) {
	t.Parallel()

	input := buildStartAssemblyInput(StartRequest{
		PromptKey: "main/launch-fav",
		Prompt:    "hello",
	}, "thread-1", contract.BuildCtx{})

	if input.PromptKey != "main/launch-fav" {
		t.Fatalf("PromptKey = %q, want main/launch-fav", input.PromptKey)
	}
}

func TestFoldRouterOutputIntoAssemblyInputPreservesPromptKey(t *testing.T) {
	t.Parallel()

	assemblyInput := contract.StartInput{PromptKey: "pre-router"}
	req := &StartRequest{PromptKey: "main/routed"}

	foldRouterOutputIntoAssemblyInput(&assemblyInput, req)

	if assemblyInput.PromptKey != "main/routed" {
		t.Fatalf("PromptKey = %q, want main/routed", assemblyInput.PromptKey)
	}
}

type capturingDynamicRegistrar struct {
	names     []string
	providers map[string]contract.DynamicSectionProvider
}

func (r *capturingDynamicRegistrar) RegisterDynamicProvider(provider contract.DynamicSectionProvider) error {
	r.names = append(r.names, provider.SectionName())
	if r.providers == nil {
		r.providers = map[string]contract.DynamicSectionProvider{}
	}
	r.providers[provider.SectionName()] = provider
	return nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *capturingDynamicRegistrar) UnregisterDynamicProvider(name string) bool {
	return false
}

type threadBuiltinPromptRegistry struct {
	templates []contract.BuiltinPromptTemplate
	sections  map[int64][]contract.BuiltinPromptSection
}

func (r *threadBuiltinPromptRegistry) ListTemplates() []contract.BuiltinPromptTemplate {
	out := make([]contract.BuiltinPromptTemplate, len(r.templates))
	copy(out, r.templates)
	return out
}

func (r *threadBuiltinPromptRegistry) GetTemplate(promptKey string) (contract.BuiltinPromptTemplate, bool) {
	for _, template := range r.templates {
		if template.PromptKey == promptKey {
			return template, true
		}
	}
	return contract.BuiltinPromptTemplate{}, false
}

func (r *threadBuiltinPromptRegistry) SectionsByTemplateID(templateID int64) []contract.BuiltinPromptSection {
	sections := r.sections[templateID]
	out := make([]contract.BuiltinPromptSection, len(sections))
	copy(out, sections)
	return out
}
