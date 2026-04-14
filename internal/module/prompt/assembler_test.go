package prompt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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
	identityContent := ""
	for _, section := range assembly.ResolvedSections {
		if section.Name == SectionIdentity {
			identityContent = section.Content
			break
		}
	}
	if identityContent == "" {
		t.Fatalf("ResolvedSections missing %q: %#v", SectionIdentity, assembly.ResolvedSections)
	}
	if !strings.Contains(assembly.BaseInstructions, identityContent) {
		t.Fatalf("BaseInstructions missing built-in section content: %q", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "## "+SectionIdentity) {
		t.Fatalf("BaseInstructions unexpectedly injected section heading: %q", assembly.BaseInstructions)
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

func TestAssembleStartKeepsStaticSectionsAheadOfDynamicSections(t *testing.T) {
	svc := NewService(&Config{}, nil)
	lateStatic := "late static sentinel"
	earlyDynamic := "early dynamic sentinel"
	if err := svc.RegisterSection(PromptSection{
		Name:   "late_static_custom",
		Order:  999,
		Region: PromptRegionStatic,
		Compute: func(context.Context, SectionContext) (*string, error) {
			return &lateStatic, nil
		},
	}); err != nil {
		t.Fatalf("RegisterSection(static) error = %v", err)
	}
	if err := svc.RegisterSection(PromptSection{
		Name:   "early_dynamic_custom",
		Order:  1,
		Region: PromptRegionDynamic,
		Compute: func(context.Context, SectionContext) (*string, error) {
			return &earlyDynamic, nil
		},
	}); err != nil {
		t.Fatalf("RegisterSection(dynamic) error = %v", err)
	}

	assembly, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	staticIdx := strings.Index(assembly.BaseInstructions, lateStatic)
	dynamicIdx := strings.Index(assembly.BaseInstructions, earlyDynamic)
	if staticIdx == -1 || dynamicIdx == -1 {
		t.Fatalf("BaseInstructions missing custom sections: %q", assembly.BaseInstructions)
	}
	if staticIdx > dynamicIdx {
		t.Fatalf("static section rendered after dynamic section: static=%d dynamic=%d\n%s", staticIdx, dynamicIdx, assembly.BaseInstructions)
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

func TestAssembleStartAppendsSystemContextToDeveloperInstructions(t *testing.T) {
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		CWD:                   t.TempDir(),
		DeveloperInstructions: "developer tail",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !strings.Contains(assembly.DeveloperInstructions, "# System Context") {
		t.Fatalf("DeveloperInstructions = %q, want system context header", assembly.DeveloperInstructions)
	}
	if !strings.Contains(assembly.DeveloperInstructions, "Git status:") {
		t.Fatalf("DeveloperInstructions = %q, want git status block", assembly.DeveloperInstructions)
	}
	if !strings.Contains(assembly.DeveloperInstructions, "developer tail") {
		t.Fatalf("DeveloperInstructions = %q, want developer tail", assembly.DeveloperInstructions)
	}
}

func TestUncachedDynamicSectionRecomputesEveryTurn(t *testing.T) {
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
	firstContent, _ := resolvedSectionContent(first.ResolvedSections, DynamicSectionMCPInstructions)
	secondContent, _ := resolvedSectionContent(second.ResolvedSections, DynamicSectionMCPInstructions)
	if firstContent == secondContent {
		t.Fatalf("uncached section did not change: first=%q second=%q", firstContent, secondContent)
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

func TestInputScopedSectionCachesOnlyDependencyFields(t *testing.T) {
	svc := NewService(&Config{}, nil)
	calls := 0
	if err := svc.RegisterDynamicProvider(DynamicTextProvider{
		Name: DynamicSectionLanguage,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			calls++
			text := fmt.Sprintf("language call #%d", calls)
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	first, err := svc.AssembleTurn(context.Background(), TurnInput{Language: "zh-CN", UserText: "first"})
	if err != nil {
		t.Fatalf("first AssembleTurn() error = %v", err)
	}
	second, err := svc.AssembleTurn(context.Background(), TurnInput{Language: "zh-CN", UserText: "second", Attachments: []string{"note.txt"}})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	third, err := svc.AssembleTurn(context.Background(), TurnInput{Language: "en", UserText: "third"})
	if err != nil {
		t.Fatalf("third AssembleTurn() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("language provider calls = %d, want 2", calls)
	}
	firstLanguage, ok := resolvedSectionContent(first.ResolvedSections, DynamicSectionLanguage)
	if !ok {
		t.Fatalf("first turn missing %q section", DynamicSectionLanguage)
	}
	secondLanguage, ok := resolvedSectionContent(second.ResolvedSections, DynamicSectionLanguage)
	if !ok {
		t.Fatalf("second turn missing %q section", DynamicSectionLanguage)
	}
	thirdLanguage, ok := resolvedSectionContent(third.ResolvedSections, DynamicSectionLanguage)
	if !ok {
		t.Fatalf("third turn missing %q section", DynamicSectionLanguage)
	}
	if firstLanguage != secondLanguage {
		t.Fatalf("input-scoped cache missed on unrelated input changes: first=%q second=%q", firstLanguage, secondLanguage)
	}
	if thirdLanguage == firstLanguage {
		t.Fatalf("language section did not rebuild after dependency change: first=%q third=%q", firstLanguage, thirdLanguage)
	}
}

func TestCacheByNameSectionIgnoresInputNoise(t *testing.T) {
	svc := NewService(&Config{}, nil)
	calls := 0
	if err := svc.RegisterDynamicProvider(DynamicTextProvider{
		Name: DynamicSectionOutputStyle,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			calls++
			text := fmt.Sprintf("output style call #%d", calls)
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	first, err := svc.AssembleTurn(context.Background(), TurnInput{Language: "zh-CN", UserText: "first"})
	if err != nil {
		t.Fatalf("first AssembleTurn() error = %v", err)
	}
	second, err := svc.AssembleTurn(context.Background(), TurnInput{
		Language:     "en",
		UserText:     "second",
		EnabledTools: []string{"spawn_agent", "lsp_grep"},
		SessionFlags: map[string]bool{"verification_required": true},
	})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("cache-by-name provider calls = %d, want 1", calls)
	}
	firstContent, ok := resolvedSectionContent(first.ResolvedSections, DynamicSectionOutputStyle)
	if !ok {
		t.Fatalf("first turn missing %q section", DynamicSectionOutputStyle)
	}
	secondContent, ok := resolvedSectionContent(second.ResolvedSections, DynamicSectionOutputStyle)
	if !ok {
		t.Fatalf("second turn missing %q section", DynamicSectionOutputStyle)
	}
	if firstContent != secondContent {
		t.Fatalf("cache-by-name section changed across unrelated inputs: first=%q second=%q", firstContent, secondContent)
	}
}

func TestResolveSectionsRunsIndependentSectionsInParallel(t *testing.T) {
	svc := NewService(&Config{}, nil)
	ready := make(chan string, 2)
	release := make(chan struct{})
	for _, name := range []string{DynamicSectionOutputStyle, DynamicSectionScratchpad} {
		if err := svc.RegisterDynamicProvider(DynamicTextProvider{
			Name: name,
			ResolveFunc: func(ctx context.Context, _ SectionContext) (*string, error) {
				ready <- name
				select {
				case <-release:
					text := name
					return &text, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		}); err != nil {
			t.Fatalf("RegisterDynamicProvider(%q) error = %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resultCh := make(chan TurnAssembly, 1)
	errCh := make(chan error, 1)
	go func() {
		assembly, err := svc.AssembleTurn(ctx, TurnInput{})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- assembly
	}()

	for range 2 {
		select {
		case <-ready:
		case err := <-errCh:
			t.Fatalf("AssembleTurn() error before both sections started = %v", err)
		case <-ctx.Done():
			t.Fatal("independent sections did not start in parallel")
		}
	}
	close(release)

	select {
	case err := <-errCh:
		t.Fatalf("AssembleTurn() error = %v", err)
	case assembly := <-resultCh:
		outputStyleIndex := resolvedSectionIndex(assembly.ResolvedSections, DynamicSectionOutputStyle)
		scratchpadIndex := resolvedSectionIndex(assembly.ResolvedSections, DynamicSectionScratchpad)
		if outputStyleIndex == -1 || scratchpadIndex == -1 {
			t.Fatalf("resolved sections missing parallel test sections: %#v", assembly.ResolvedSections)
		}
		if outputStyleIndex > scratchpadIndex {
			t.Fatalf("resolved section order changed: output_style=%d scratchpad=%d", outputStyleIndex, scratchpadIndex)
		}
	case <-ctx.Done():
		t.Fatal("AssembleTurn() timed out")
	}
}

func resolvedSectionContent(sections []ResolvedPromptSection, name string) (string, bool) {
	for _, section := range sections {
		if section.Name == name { return section.Content, true }
	}
	return "", false
}
func resolvedSectionIndex(sections []ResolvedPromptSection, name string) int {
	for idx, section := range sections {
		if section.Name == name { return idx }
	}
	return -1
}
