package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterDynamicProviderMakesSlotRenderable(t *testing.T) {
	svc := NewService(&Config{}, nil)
	want := len(StaticSections()) + len(DynamicSlotNames())
	if len(svc.Sections()) != want {
		t.Fatalf("len(Sections()) = %d, want %d", len(svc.Sections()), want)
	}
	provider := DynamicTextProvider{
		Name: DynamicSectionLanguage,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			text := "Always respond in Chinese."
			return &text, nil
		},
	}
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{Language: "Chinese"})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if !strings.Contains(assembly.UserContextText, "Always respond in Chinese.") {
		t.Fatalf("UserContextText = %q, want registered dynamic text", assembly.UserContextText)
	}
}

func TestUnregisterDynamicProviderRemovesRenderedContent(t *testing.T) {
	svc := NewService(&Config{}, nil)
	provider := DynamicTextProvider{
		Name: DynamicSectionSessionGuidance,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			text := "Use the current skill card."
			return &text, nil
		},
	}
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}
	if !svc.UnregisterDynamicProvider(DynamicSectionSessionGuidance) {
		t.Fatal("UnregisterDynamicProvider() = false, want true")
	}

	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.Contains(assembly.UserContextText, "Use the current skill card.") {
		t.Fatalf("UserContextText = %q, want provider content removed", assembly.UserContextText)
	}
}

func TestSessionGuidanceProviderResolveUsesEnabledTools(t *testing.T) {
	provider := SessionGuidanceProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{
		EnabledTools: []string{"spawn_agent", "request_user_input", "request_user_input"},
		SessionFlags: map[string]bool{"verification_required": true},
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want content")
	}
	checks := []string{
		"# Session-specific guidance",
		"request_user_input",
		"spawn_agent",
		"independent verification pass",
	}
	for _, check := range checks {
		if !strings.Contains(*text, check) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, check)
		}
	}
}

func TestSessionGuidanceProviderResolveSkipsWithoutRelevantTools(t *testing.T) {
	provider := SessionGuidanceProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{EnabledTools: []string{"lsp_file"}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want nil", *text)
	}
}
