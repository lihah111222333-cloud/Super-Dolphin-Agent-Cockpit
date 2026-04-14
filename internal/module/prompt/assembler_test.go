package prompt

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAssembleStartIncludesBuiltinsAndDynamicSections(t *testing.T) {
	svc := NewService(&Config{}, nil)
	if err := svc.RegisterDynamicProvider(DynamicTextProvider{
		Name: DynamicSectionEnvInfoSimple,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			text := "CWD: /repo"
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Prompt:                " legacy display ",
		BaseInstructions:      "legacy base",
		DeveloperInstructions: "developer tail",
		Provider:              "codex",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if assembly.DisplayName != "legacy display" {
		t.Fatalf("DisplayName = %q, want %q", assembly.DisplayName, "legacy display")
	}
	if !strings.Contains(assembly.BaseInstructions, "## "+SectionIdentity) {
		t.Fatalf("BaseInstructions missing built-in section: %q", assembly.BaseInstructions)
	}
	if !strings.Contains(assembly.BaseInstructions, "CWD: /repo") {
		t.Fatalf("BaseInstructions missing dynamic section text: %q", assembly.BaseInstructions)
	}
	if !strings.Contains(assembly.BaseInstructions, "legacy base") {
		t.Fatalf("BaseInstructions missing legacy base payload: %q", assembly.BaseInstructions)
	}
	if assembly.DeveloperInstructions != "developer tail" {
		t.Fatalf("DeveloperInstructions = %q, want %q", assembly.DeveloperInstructions, "developer tail")
	}
	if assembly.Snapshot.Version != SnapshotVersion || assembly.Snapshot.Hash == "" {
		t.Fatalf("unexpected snapshot = %#v", assembly.Snapshot)
	}
}

func TestAssembleStartFallsBackOnBuildError(t *testing.T) {
	svc := NewService(&Config{}, nil)
	if err := svc.RegisterSection(PromptSection{
		Name:   "broken",
		Order:  999,
		Region: PromptRegionStatic,
		Compute: func(context.Context, SectionContext) (*string, error) {
			return nil, errors.New("boom")
		},
	}); err != nil {
		t.Fatalf("RegisterSection() error = %v", err)
	}

	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Prompt:                "fallback name",
		BaseInstructions:      "base",
		DeveloperInstructions: "dev",
		Provider:              "codex",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if assembly.BaseInstructions != "base" || assembly.DeveloperInstructions != "dev" {
		t.Fatalf("fallback assembly = %#v", assembly)
	}
	if len(assembly.ResolvedSections) != 0 {
		t.Fatalf("ResolvedSections = %d, want 0 on fallback", len(assembly.ResolvedSections))
	}
}

func TestVolatileDynamicSectionRecomputesEveryTurn(t *testing.T) {
	svc := NewService(&Config{}, nil)
	counter := 0
	if err := svc.RegisterDynamicProvider(DynamicTextProvider{
		Name: DynamicSectionMCPInstructions,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			counter++
			text := "mcp call #" + string(rune('0'+counter))
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	first, err := svc.AssembleTurn(context.Background(), TurnInput{})
	if err != nil {
		t.Fatalf("first AssembleTurn() error = %v", err)
	}
	second, err := svc.AssembleTurn(context.Background(), TurnInput{})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	if first.UserContextText == second.UserContextText {
		t.Fatalf("volatile section did not change: first=%q second=%q", first.UserContextText, second.UserContextText)
	}
}
