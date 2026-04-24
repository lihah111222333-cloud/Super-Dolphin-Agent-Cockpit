package prompt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Regression guard: DisplayName must NEVER be derived from Prompt.
//
// Root cause (2026-04-24): assembler.go used
//   shared.FirstNonEmpty(in.Name, in.Prompt)
// for DisplayName in all 3 paths (AssembleStart, simpleStartAssembly,
// fallbackStartAssembly). This caused the user's first message to become
// the thread name (e.g. "嗨" or a pasted JSON blob).
//
// If you are reading this because a test broke: the naming policy is
// intentional. DO NOT reintroduce Prompt → DisplayName derivation.
// ---------------------------------------------------------------------------

func TestPromptNeverBecomesDisplayName_AssembleStart(t *testing.T) {
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Prompt:   "嗨",
		Provider: "codex",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if assembly.DisplayName != "" {
		t.Fatalf("REGRESSION: DisplayName = %q, want empty (prompt must not become display name)", assembly.DisplayName)
	}
}

func TestPromptNeverBecomesDisplayName_SimpleStart(t *testing.T) {
	t.Setenv(envClaudeSimple, "1")
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Prompt:   "请负责定位登录回调 500 根因",
		CWD:      "/repo",
		Provider: "codex",
	})
	if err != nil {
		t.Fatalf("AssembleStart(simple) error = %v", err)
	}
	if assembly.DisplayName != "" {
		t.Fatalf("REGRESSION: DisplayName = %q, want empty (prompt must not become display name in simple mode)", assembly.DisplayName)
	}
}

func TestExplicitNamePreserved_AssembleStart(t *testing.T) {
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Name:     "用户命名",
		Prompt:   "嗨",
		Provider: "codex",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if assembly.DisplayName != "用户命名" {
		t.Fatalf("DisplayName = %q, want %q", assembly.DisplayName, "用户命名")
	}
}

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
		Name:                  " legacy display ",
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
	if assembly.Boundary == nil {
		t.Fatalf("Boundary = nil, want cached/uncached split metadata")
	}
	if !strings.Contains(assembly.Boundary.CachedPrefix, identityContent) {
		t.Fatalf("CachedPrefix = %q, want identity section", assembly.Boundary.CachedPrefix)
	}
	if strings.Contains(assembly.Boundary.CachedPrefix, "CWD: /repo") {
		t.Fatalf("CachedPrefix unexpectedly contains dynamic section: %q", assembly.Boundary.CachedPrefix)
	}
	if !strings.Contains(assembly.Boundary.UncachedTail, "CWD: /repo") || !strings.Contains(assembly.Boundary.UncachedTail, "legacy base") {
		t.Fatalf("UncachedTail = %q, want dynamic section and legacy base", assembly.Boundary.UncachedTail)
	}
	// The boundary blocks compose the boundary portion of BaseInstructions;
	// the full BaseInstructions also includes the appended system prompt.
	boundaryComposed := joinBlocks(assembly.Boundary.CachedPrefix, assembly.Boundary.UncachedTail)
	if !strings.HasPrefix(assembly.BaseInstructions, boundaryComposed) {
		t.Fatalf("BaseInstructions does not start with boundary blocks: boundary=%#v base=%q", assembly.Boundary, assembly.BaseInstructions)
	}
	if assembly.Snapshot.Boundary == nil || *assembly.Snapshot.Boundary != *assembly.Boundary {
		t.Fatalf("Snapshot.Boundary = %#v, want %#v", assembly.Snapshot.Boundary, assembly.Boundary)
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
	if !strings.Contains(assembly.BaseInstructions, "base") || assembly.DeveloperInstructions != "dev" || assembly.DisplayName != "" {
		t.Fatalf("fallback assembly = %#v (DisplayName must be empty when only Prompt is set)", assembly)
	}
	// Phase 3 invariant: system-reminder content (currentDate, runtimeExtras,
	// gitStatus) must never leak into BaseInstructions — it flows through the
	// structured UserContext/UserContextText/SystemContext fields instead so
	// provider bridges can route it into the synthetic user meta message.
	if strings.Contains(assembly.BaseInstructions, "<system-reminder>") {
		t.Fatalf("fallback BaseInstructions must not embed system-reminder: %q", assembly.BaseInstructions)
	}
	if _, ok := assembly.UserContext["currentDate"]; !ok {
		t.Fatalf("fallback UserContext missing currentDate: %#v", assembly.UserContext)
	}
	if len(assembly.ResolvedSections) != 0 {
		t.Fatalf("ResolvedSections = %d, want 0 on fallback", len(assembly.ResolvedSections))
	}
}

func TestAssembleStartKeepsDeveloperInstructionsSeparateFromSystemContext(t *testing.T) {
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		CWD:                   t.TempDir(),
		DeveloperInstructions: "developer tail",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if assembly.DeveloperInstructions != "developer tail" {
		t.Fatalf("DeveloperInstructions = %q, want developer tail", assembly.DeveloperInstructions)
	}
}

func TestAssembleTurnIncludesSystemContext(t *testing.T) {
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.TrimSpace(assembly.SystemContext["gitStatus"]) == "" {
		t.Fatalf("SystemContext = %#v, want gitStatus entry", assembly.SystemContext)
	}
}

func TestAssembleTurnRefreshesSystemContextEachTurn(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	svc := NewService(&Config{}, nil)
	first, err := svc.AssembleTurn(context.Background(), TurnInput{CWD: dir})
	if err != nil {
		t.Fatalf("first AssembleTurn() error = %v", err)
	}
	if strings.Contains(first.SystemContext["gitStatus"], "note.txt") {
		t.Fatalf("first gitStatus = %q, want clean initial status", first.SystemContext["gitStatus"])
	}
	if err := os.WriteFile(dir+"/note.txt", []byte("draft"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	second, err := svc.AssembleTurn(context.Background(), TurnInput{CWD: dir})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	if first.SystemContext["gitStatus"] == second.SystemContext["gitStatus"] {
		t.Fatalf("gitStatus did not refresh: first=%q second=%q", first.SystemContext["gitStatus"], second.SystemContext["gitStatus"])
	}
	if !strings.Contains(second.SystemContext["gitStatus"], "note.txt") {
		t.Fatalf("second gitStatus = %q, want note.txt entry", second.SystemContext["gitStatus"])
	}
}

func TestSimpleAssembleStartHardEarlyReturn(t *testing.T) {
	t.Setenv(envClaudeSimple, "1")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")
	svc := NewService(&Config{}, nil)
	called := false
	text := "should never render"
	if err := svc.RegisterSection(PromptSection{
		Name:   "simple_static_guard",
		Order:  999,
		Region: PromptRegionStatic,
		Compute: func(context.Context, SectionContext) (*string, error) {
			called = true
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterSection() error = %v", err)
	}
	assembly, err := svc.AssembleStart(context.Background(), StartInput{CWD: "/repo", DeveloperInstructions: "developer tail", BaseInstructions: "legacy base"})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if called {
		t.Fatal("simple mode still evaluated registered sections")
	}
	if len(assembly.ResolvedSections) != 0 {
		t.Fatalf("ResolvedSections = %#v, want nil/empty in simple mode", assembly.ResolvedSections)
	}
	want := simpleStartIdentityLine + "\nCWD: /repo\nDate: 2026-04-22"
	if assembly.BaseInstructions != want {
		t.Fatalf("BaseInstructions = %q, want strict three-line form %q", assembly.BaseInstructions, want)
	}
	if strings.Contains(assembly.BaseInstructions, "<system-reminder>") {
		t.Fatalf("CLAUDE_CODE_SIMPLE ultraSimple must not inject system-reminder: %q", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "legacy base") || strings.Contains(assembly.BaseInstructions, text) {
		t.Fatalf("BaseInstructions unexpectedly kept normal-path content: %q", assembly.BaseInstructions)
	}
	if assembly.DeveloperInstructions != "developer tail" {
		t.Fatalf("DeveloperInstructions = %q, want developer tail", assembly.DeveloperInstructions)
	}
	if assembly.UserContext != nil || assembly.UserContextText != "" || assembly.SystemContext != nil {
		t.Fatalf("ultraSimple must leave UserContext/SystemContext empty: %#v / %q / %#v", assembly.UserContext, assembly.UserContextText, assembly.SystemContext)
	}
}

func TestSimpleAssembleStartUsesSessionFlag(t *testing.T) {
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		CWD:          "/flagged",
		SessionFlags: map[string]bool{"simple_mode": true},
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if len(assembly.ResolvedSections) != 0 {
		t.Fatalf("ResolvedSections = %#v, want nil/empty under simple session flag", assembly.ResolvedSections)
	}
	want := simpleStartIdentityLine + "\nCWD: /flagged\nDate: 2026-04-22"
	if assembly.BaseInstructions != want {
		t.Fatalf("BaseInstructions = %q, want strict three-line form %q", assembly.BaseInstructions, want)
	}
	if strings.Contains(assembly.BaseInstructions, "<system-reminder>") {
		t.Fatalf("simple session flag must not inject system-reminder: %q", assembly.BaseInstructions)
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

func TestUncachedDynamicSectionRetainsStableLastValue(t *testing.T) {
	svc := NewService(&Config{}, nil)
	calls := 0
	if err := svc.RegisterDynamicProvider(DynamicTextProvider{
		Name: DynamicSectionMCPInstructions,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			calls++
			text := "stable mcp instructions"
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	first, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("first AssembleStart() error = %v", err)
	}
	second, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("second AssembleStart() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("stable uncached provider calls = %d, want 2", calls)
	}
	if first.Snapshot.Hash != second.Snapshot.Hash {
		t.Fatalf("stable uncached section changed snapshot hash: first=%q second=%q", first.Snapshot.Hash, second.Snapshot.Hash)
	}
	internal := svc.(*service)
	generation := internal.cache.Generation()
	cached, ok := internal.cache.Lookup(DynamicSectionMCPInstructions, generation)
	if !ok || cached == nil || *cached != "stable mcp instructions" {
		t.Fatalf("volatile cache = %#v, %v, want stable retained value", cached, ok)
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v error = %v\n%s", args, err, string(output))
	}
}

func resolvedSectionContent(sections []ResolvedPromptSection, name string) (string, bool) {
	for _, section := range sections {
		if section.Name == name {
			return section.Content, true
		}
	}
	return "", false
}
func resolvedSectionIndex(sections []ResolvedPromptSection, name string) int {
	for idx, section := range sections {
		if section.Name == name {
			return idx
		}
	}
	return -1
}
