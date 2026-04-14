package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterDynamicProviderMakesSlotRenderable(t *testing.T) {
	svc := NewService(&Config{}, nil)
	if len(svc.Sections()) != 12 {
		t.Fatalf("len(Sections()) = %d, want 12", len(svc.Sections()))
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
