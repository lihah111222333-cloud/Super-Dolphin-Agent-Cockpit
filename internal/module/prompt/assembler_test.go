package prompt

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	identityContent := requireResolvedPromptSectionContent(t, assembly.ResolvedSections, SectionIdentity)
	assertStartAssemblyBaseContent(t, assembly, identityContent)
	if assembly.DeveloperInstructions != "developer tail" {
		t.Fatalf("DeveloperInstructions = %q, want %q", assembly.DeveloperInstructions, "developer tail")
	}
	if assembly.Snapshot.Version != SnapshotVersion || assembly.Snapshot.Hash == "" {
		t.Fatalf("unexpected snapshot = %#v", assembly.Snapshot)
	}
	assertStartAssemblyBoundary(t, assembly, identityContent)
}

func TestAssembleStartIncludesDatasourceDynamicSection(t *testing.T) {
	svc := NewService(&Config{}, nil)
	datasourceText := "# Data sources\n- alpha.pdf\n- zeta.txt"
	if err := svc.RegisterDynamicProvider(DynamicTextProvider{
		Name: DynamicSectionDatasource,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			return &datasourceText, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	assembly, err := svc.AssembleStart(context.Background(), StartInput{CWD: "/repo"})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	content := requireResolvedPromptSectionContent(t, assembly.ResolvedSections, DynamicSectionDatasource)
	if content != datasourceText {
		t.Fatalf("datasource section content = %q, want %q", content, datasourceText)
	}
	if !strings.Contains(assembly.BaseInstructions, datasourceText) {
		t.Fatalf("BaseInstructions missing datasource section:\n%s", assembly.BaseInstructions)
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

func TestAssembleStartRendersSuppressedToolsInToolPreferences(t *testing.T) {
	svc := NewService(&Config{}, nil, WithDisabledBuiltinToolsFn(func(context.Context, string, string) []string {
		return []string{"Read", "WebSearch"}
	}))

	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		CWD:      "/repo",
		Provider: "codex",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	for _, want := range []string{
		"Do NOT use these native tools",
		"Read",
		"WebSearch",
	} {
		if !strings.Contains(assembly.BaseInstructions, want) {
			t.Fatalf("BaseInstructions missing %q:\n%s", want, assembly.BaseInstructions)
		}
	}
	if strings.Join(assembly.SuppressedTools, ",") != "Read,WebSearch" {
		t.Fatalf("SuppressedTools = %#v, want Read/WebSearch carrier", assembly.SuppressedTools)
	}
}

func TestAssembleStartSimpleCodexCarriesSuppressedTools(t *testing.T) {
	svc := NewService(&Config{}, nil, WithDisabledBuiltinToolsFn(func(context.Context, string, string) []string {
		return []string{"shell"}
	}))

	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		CWD:          "/repo",
		Provider:     "codex",
		SessionFlags: map[string]bool{"simple": true},
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if strings.Join(assembly.SuppressedTools, ",") != "shell" {
		t.Fatalf("SuppressedTools = %#v, want simple carrier", assembly.SuppressedTools)
	}
}

func TestAssembleStartSuppressesUserDisabledToolsForRequestedProvider(t *testing.T) {
	svc := NewService(&Config{}, nil, WithDisabledBuiltinToolsFn(func(_ context.Context, _ string, provider string) []string {
		if provider == "codex" {
			return []string{"codex_only_disabled_tool"}
		}
		return []string{"Bash"}
	}))

	assembly, err := svc.AssembleStart(context.Background(), StartInput{CWD: "/repo", Provider: "claude"})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !strings.Contains(assembly.BaseInstructions, "Bash") {
		t.Fatalf("BaseInstructions missing claude suppressions:\n%s", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "codex_only_disabled_tool") {
		t.Fatalf("BaseInstructions leaked codex suppression:\n%s", assembly.BaseInstructions)
	}
}

func TestAssembleStartReturnsErrorOnBuildError(t *testing.T) {
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
	if err == nil {
		t.Fatalf("AssembleStart() error = nil, want section build error")
	}
	if assembly.BaseInstructions != "" || assembly.DeveloperInstructions != "" || assembly.DisplayName != "" {
		t.Fatalf("assembly = %#v, want zero value on build error", assembly)
	}
	if strings.Contains(assembly.BaseInstructions, "<system-reminder>") {
		t.Fatalf("error BaseInstructions must not embed system-reminder: %q", assembly.BaseInstructions)
	}
	if len(assembly.ResolvedSections) != 0 {
		t.Fatalf("ResolvedSections = %d, want 0 on build error", len(assembly.ResolvedSections))
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
	want := simpleStartIdentityLine + "\nCWD: /repo\nDate: 2026-04-22"
	assertSimpleStartHardEarlyReturn(t, assembly, called, want, text)
	if assembly.DeveloperInstructions != "developer tail" {
		t.Fatalf("DeveloperInstructions = %q, want developer tail", assembly.DeveloperInstructions)
	}
	assertSimpleStartContextEmpty(t, assembly)
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
	assertMemoryProviderSkippedOnTurn(t, firstTurn, calls)

	firstStart, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("first AssembleStart() error = %v", err)
	}
	secondStart, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("second AssembleStart() error = %v", err)
	}
	assertCachedMemoryStart(t, firstStart, secondStart, calls)

	secondTurn, err := svc.AssembleTurn(context.Background(), TurnInput{})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	assertMemoryProviderStillSkippedOnTurn(t, secondTurn, calls)

	if err := svc.Invalidate(context.Background(), InvalidateClear); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	thirdStart, err := svc.AssembleStart(context.Background(), StartInput{})
	if err != nil {
		t.Fatalf("third AssembleStart() error = %v", err)
	}
	assertRebuiltMemoryStart(t, thirdStart, calls)
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
	firstLanguage := requireResolvedPromptSectionContent(t, first.ResolvedSections, DynamicSectionLanguage)
	secondLanguage := requireResolvedPromptSectionContent(t, second.ResolvedSections, DynamicSectionLanguage)
	thirdLanguage := requireResolvedPromptSectionContent(t, third.ResolvedSections, DynamicSectionLanguage)
	assertInputScopedLanguageCache(t, firstLanguage, secondLanguage, thirdLanguage)
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

	waitForParallelSectionsReady(t, ctx, ready, errCh)
	close(release)

	assertParallelSectionAssembly(t, ctx, resultCh, errCh)
}
