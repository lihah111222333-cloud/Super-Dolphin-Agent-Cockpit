package prompt

import (
	"context"
	"errors"
	"fmt"
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

func TestStartOnlyDynamicSectionCachesStartWithoutLeakingToTurn(t *testing.T) {
	svc := NewService(&Config{}, nil)
	calls := 0
	if err := svc.RegisterDynamicProvider(DynamicTextProvider{
		Name: DynamicSectionMemory,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			calls++
			text := fmt.Sprintf("memory build #%d", calls)
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	firstTurn, err := svc.AssembleTurn(context.Background(), TurnInput{})
	if err != nil {
		t.Fatalf("first AssembleTurn() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("memory provider calls after turn = %d, want 0", calls)
	}
	if strings.Contains(firstTurn.UserContextText, "memory build #") {
		t.Fatalf("turn unexpectedly rendered memory content: %q", firstTurn.UserContextText)
	}

	firstStart, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("first AssembleStart() error = %v", err)
	}
	secondStart, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("second AssembleStart() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("memory provider calls after repeated start = %d, want 1", calls)
	}
	if !strings.Contains(firstStart.BaseInstructions, "memory build #1") {
		t.Fatalf("start missing cached memory content: %q", firstStart.BaseInstructions)
	}
	if firstStart.BaseInstructions != secondStart.BaseInstructions {
		t.Fatalf("cached start mismatch: first=%q second=%q", firstStart.BaseInstructions, secondStart.BaseInstructions)
	}

	secondTurn, err := svc.AssembleTurn(context.Background(), TurnInput{})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("memory provider calls after second turn = %d, want 1", calls)
	}
	if strings.Contains(secondTurn.UserContextText, "memory build #") {
		t.Fatalf("turn unexpectedly reused cached memory content: %q", secondTurn.UserContextText)
	}

	if err := svc.Invalidate(context.Background(), InvalidateClear); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	thirdStart, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("third AssembleStart() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("memory provider calls after invalidate = %d, want 2", calls)
	}
	if !strings.Contains(thirdStart.BaseInstructions, "memory build #2") {
		t.Fatalf("start missing rebuilt memory content: %q", thirdStart.BaseInstructions)
	}
}
