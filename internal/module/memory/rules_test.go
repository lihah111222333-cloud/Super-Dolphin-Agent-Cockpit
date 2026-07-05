package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

func TestMemoryRuleEngineRulesForKnownTypes(t *testing.T) {
	engine := NewMemoryRuleEngine()
	for _, memoryType := range diskMemoryTypes {
		behavior, ok := engine.RulesForType(memoryType)
		if !ok {
			t.Fatalf("RulesForType(%q) missing", memoryType)
		}
		if strings.TrimSpace(behavior.Summary) == "" {
			t.Fatalf("RulesForType(%q) summary is empty", memoryType)
		}
		if len(behavior.Save) == 0 || len(behavior.Access) == 0 || len(behavior.Trust) == 0 {
			t.Fatalf("RulesForType(%q) has incomplete layers: %#v", memoryType, behavior)
		}
	}
}

func TestBuildMemoryLinesIncludesDeterministicCompleteSections(t *testing.T) {
	text := NewMemoryRuleEngine().BuildMemoryLines(MemoryRuleOptions{SearchPastContextEnabled: true})
	orderedHeadings := []string{
		"### 1. memory system",
		"### 2. taxonomy",
		"### 3. exclusions",
		"### 4. save rules",
		"### 5. auto-detect signals",
		"### 6. access rules",
		"### 7. trust rules",
		"### 8. memory vs plan/tasks",
		"### 9. searching past context",
	}
	last := -1
	for _, heading := range orderedHeadings {
		idx := strings.Index(text, heading)
		if idx == -1 {
			t.Fatalf("missing heading %q in prompt:\n%s", heading, text)
		}
		if idx <= last {
			t.Fatalf("heading %q is out of order in prompt", heading)
		}
		last = idx
	}
	for _, memoryType := range diskMemoryTypes {
		if got := strings.Count(text, "#### "+string(memoryType)); got != 3 {
			t.Fatalf("type heading %q count = %d, want 3 in save/access/trust sections", memoryType, got)
		}
	}
	for _, snippet := range []string{
		"sanitize + resolve + authorize",
		"ignore or not use memory",
		"`scope` is an ACL boundary",
		"local_unavailable",
		"Verify referenced functions and flags still exist",
		"V3 also re-checks referenced type names",
		"both what to avoid and what to keep doing",
		"who is doing what, why, or by when",
		"`git log` / `git blame` are authoritative",
		"what was surprising or non-obvious about it",
		"Standard mode is a two-step save",
		"Each durable fact belongs in its own topic file",
		"durable memory is searched first, and budgeted transcript snippets may be surfaced",
	} {
		if !strings.Contains(text, snippet) {
			t.Fatalf("prompt missing required snippet %q", snippet)
		}
	}
}

func TestBuildMemoryLinesSkipIndexAndExtraGuidelines(t *testing.T) {
	text := NewMemoryRuleEngine().BuildMemoryLines(MemoryRuleOptions{
		SkipIndex:                true,
		SearchPastContextEnabled: true,
		ExtraGuidelines:          []string{"Keep explanations short.", "Prefer absolute dates in summaries."},
	})
	if !strings.Contains(text, "When `skipIndex` is enabled, write or update the topic file only") {
		t.Fatalf("skipIndex rule missing from prompt:\n%s", text)
	}
	extraIndex := strings.Index(text, "### 9. extra guidelines")
	if extraIndex == -1 {
		t.Fatalf("extra guidelines section missing from prompt:\n%s", text)
	}
	extra := text[extraIndex:]
	for _, snippet := range []string{"Keep explanations short.", "Prefer absolute dates in summaries."} {
		if !strings.Contains(extra, snippet) {
			t.Fatalf("extra guidelines section missing %q", snippet)
		}
	}
	searchIndex := strings.Index(text, "### 10. searching past context")
	if searchIndex == -1 {
		t.Fatalf("searching past context section missing from prompt:\n%s", text)
	}
	if searchIndex <= extraIndex {
		t.Fatalf("searching past context section should appear after extra guidelines")
	}
}

func TestRulesLoadMemoryPromptSearchPastContextGateAcrossModes(t *testing.T) {
	engine := NewMemoryRuleEngine()
	cases := []struct {
		name string
		mode MemoryMode
		opts MemoryRuleOptions
	}{
		{name: "standard", mode: MemoryModeStandard},
		{name: "combined", mode: MemoryModeCombined, opts: MemoryRuleOptions{AutoMemPath: "/tmp/auto", TeamMemPath: "/tmp/team"}},
		{name: "kairos", mode: MemoryModeKairos},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			disabled := engine.LoadMemoryPrompt(tc.mode, true, tc.opts)
			if disabled == nil {
				t.Fatalf("LoadMemoryPrompt(%s, gate off) returned nil", tc.mode)
			}
			if strings.Contains(*disabled, "searching past context") {
				t.Fatalf("LoadMemoryPrompt(%s, gate off) unexpectedly included searching section:\n%s", tc.mode, *disabled)
			}
			enabledOpts := tc.opts
			enabledOpts.SearchPastContextEnabled = true
			enabled := engine.LoadMemoryPrompt(tc.mode, true, enabledOpts)
			if enabled == nil {
				t.Fatalf("LoadMemoryPrompt(%s, gate on) returned nil", tc.mode)
			}
			if !strings.Contains(*enabled, "searching past context") {
				t.Fatalf("LoadMemoryPrompt(%s, gate on) missing searching section:\n%s", tc.mode, *enabled)
			}
		})
	}
}

func TestRulesProviderOmitsSearchingPastContextWhenFeatureDisabled(t *testing.T) {
	provider := NewRulesProvider(&Config{
		Enabled:         true,
		RootDir:         t.TempDir(),
		SkipIndex:       true,
		ExtraGuidelines: []string{"Keep explanations short."},
	}, NewMemoryRuleEngine(), nil)
	svc := prompt.NewService(&prompt.Config{}, nil)
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}
	start, err := svc.AssembleStart(context.Background(), prompt.StartInput{})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !strings.Contains(start.BaseInstructions, "### 1. memory system") {
		t.Fatalf("BaseInstructions missing rendered memory rules:\n%s", start.BaseInstructions)
	}
	if strings.Contains(start.BaseInstructions, "searching past context") {
		t.Fatalf("BaseInstructions unexpectedly contains searching past context with feature disabled:\n%s", start.BaseInstructions)
	}
}

func TestMemoryRulesProviderRegistersStartOnlyDynamicSection(t *testing.T) {
	provider := NewRulesProvider(&Config{
		Enabled:         true,
		RootDir:         t.TempDir(),
		SkipIndex:       true,
		ExtraGuidelines: []string{"Keep explanations short."},
		Features:        MemoryFeatureFlags{SearchPastContext: true},
	}, NewMemoryRuleEngine(), nil)
	var dynamic prompt.DynamicSectionProvider = provider
	if dynamic.SectionName() != prompt.DynamicSectionMemory {
		t.Fatalf("SectionName() = %q, want %q", dynamic.SectionName(), prompt.DynamicSectionMemory)
	}

	svc := prompt.NewService(&prompt.Config{}, nil)
	if err := svc.RegisterDynamicProvider(dynamic); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	start, err := svc.AssembleStart(context.Background(), prompt.StartInput{})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !strings.Contains(start.BaseInstructions, "### 1. memory system") {
		t.Fatalf("BaseInstructions missing rendered memory rules:\n%s", start.BaseInstructions)
	}
	for _, snippet := range []string{
		"### 2. taxonomy",
		"When `skipIndex` is enabled",
		"### 9. extra guidelines",
		"Keep explanations short.",
		"### 10. searching past context",
	} {
		if !strings.Contains(start.BaseInstructions, snippet) {
			t.Fatalf("BaseInstructions missing %q:\n%s", snippet, start.BaseInstructions)
		}
	}
	if strings.Contains(start.BaseInstructions, "Feature flag `") {
		t.Fatalf("BaseInstructions still contains feature-placeholder guidance:\n%s", start.BaseInstructions)
	}

	turn, err := svc.AssembleTurn(context.Background(), prompt.TurnInput{})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.Contains(turn.UserContextText, "### 1. memory system") {
		t.Fatalf("UserContextText unexpectedly contains memory dynamic section:\n%s", turn.UserContextText)
	}
}

// TestMemoryRulesProviderDoesNotLeakAbsoluteDisplayPath 覆盖 root memory 到 retrieval wrapper 的 prompt 可见路径。
// 记忆附件可以显示文件名或相对路径，但不能把本机绝对 memory root 写进 provider prompt。
func TestMemoryRulesProviderDoesNotLeakAbsoluteDisplayPath(t *testing.T) {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	absolutePath := filepath.Join(root, "project", "commit-style.md")
	attachments := freezeRelevantMemoryAttachments([]MemoryEntry{{
		FilePath:  absolutePath,
		Content:   "Use concise imperative commit messages.",
		UpdatedAt: now,
	}}, now)
	if len(attachments) != 1 {
		t.Fatalf("len(freezeRelevantMemoryAttachments()) = %d, want 1", len(attachments))
	}
	attachment := attachments[0]
	for _, leaked := range []string{root, filepath.ToSlash(root), absolutePath} {
		if strings.Contains(attachment.Path, leaked) ||
			strings.Contains(attachment.Header, leaked) ||
			strings.Contains(attachment.Content, leaked) {
			t.Fatalf("attachment leaked absolute memory path %q: %#v", leaked, attachment)
		}
	}
	if filepath.IsAbs(attachment.Path) || !strings.Contains(filepath.ToSlash(attachment.Path), "commit-style.md") {
		t.Fatalf("attachment path = %q, want non-absolute display path", attachment.Path)
	}
}

// 当前 MEMORY.md 只在会话启动时由 MemoryEntrypointProvider 注入。
// turn-time MemoryContextProvider.Resolve 保持 no-op；旧的 turn-time fallback
// 注入断言已转到 entrypoint_provider_test.go 的 Resolve 行为测试。

func TestCombinedRulesProviderUsesDynamicSectionMemory(t *testing.T) {
	withTeamMemoryRuntimeReady(t, true)
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	autoRoot := filepath.Join(base, "automem")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", repoRoot, err)
	}
	cfg := &Config{
		Enabled:             true,
		RootDir:             base,
		ProjectRoot:         repoRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	svc := prompt.NewService(&prompt.Config{}, nil)
	if err := svc.RegisterDynamicProvider(NewRulesProvider(cfg, NewMemoryRuleEngine(), NewTeamMemoryManager(cfg))); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	start, err := svc.AssembleStart(context.Background(), prompt.StartInput{
		GitRoot: repoRoot,
		CWD:     repoRoot,
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	section, ok := resolvedPromptSectionByName(start.ResolvedSections, prompt.DynamicSectionMemory)
	if !ok {
		t.Fatalf("ResolvedSections missing %q: %#v", prompt.DynamicSectionMemory, start.ResolvedSections)
	}
	for _, snippet := range []string{
		"shared team directory",
		memoryIndexPath(autoRoot),
		memoryIndexPath(filepath.Join(autoRoot, teamMemoryRootDirName)),
	} {
		if !strings.Contains(section.Content, snippet) {
			t.Fatalf("combined memory section missing %q:\n%s", snippet, section.Content)
		}
	}

	turn, err := svc.AssembleTurn(context.Background(), prompt.TurnInput{
		ThreadID: "thread-1",
		GitRoot:  repoRoot,
		CWD:      repoRoot,
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.Contains(turn.UserContextText, "shared team directory") {
		t.Fatalf("turn user context unexpectedly contains combined start-only prompt:\n%s", turn.UserContextText)
	}
}

func TestCombinedRulesProviderFallsBackToStandardWithoutTeamRuntime(t *testing.T) {
	withTeamMemoryRuntimeReady(t, false)
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	autoRoot := filepath.Join(base, "automem")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", repoRoot, err)
	}
	cfg := &Config{
		Enabled:             true,
		RootDir:             base,
		ProjectRoot:         repoRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	text, err := NewRulesProvider(cfg, NewMemoryRuleEngine(), NewTeamMemoryManager(cfg)).Resolve(context.Background(), prompt.SectionContext{
		BuildCtx: contract.BuildCtx{GitRoot: repoRoot, CWD: repoRoot},
		Start:    &prompt.StartInput{},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want standard memory rules")
	}
	if strings.Contains(*text, "shared team directory") {
		t.Fatalf("standard fallback unexpectedly exposed combined team guidance:\n%s", *text)
	}
	if !strings.Contains(*text, "### 1. memory system") {
		t.Fatalf("standard fallback missing baseline rules:\n%s", *text)
	}
}

func TestCombinedRulesProviderSuppressesCombinedWhenKairosActive(t *testing.T) {
	withTeamMemoryRuntimeReady(t, true)
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	autoRoot := filepath.Join(base, "automem")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", repoRoot, err)
	}
	cfg := &Config{
		Enabled:             true,
		RootDir:             base,
		ProjectRoot:         repoRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true, Kairos: true},
	}
	text, err := NewRulesProvider(cfg, NewMemoryRuleEngine(), NewTeamMemoryManager(cfg)).Resolve(context.Background(), prompt.SectionContext{
		BuildCtx: contract.BuildCtx{GitRoot: repoRoot, CWD: repoRoot},
		Start:    &prompt.StartInput{},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want Kairos prompt")
	}
	if strings.Contains(*text, "shared team directory") {
		t.Fatalf("Kairos prompt unexpectedly exposed combined team guidance:\n%s", *text)
	}
	if !strings.Contains(*text, "### 1. KAIROS daily log mode") {
		t.Fatalf("Kairos prompt missing daily-log guidance:\n%s", *text)
	}
}

// resolvedPromptSectionByName 返回指定名称的 resolved prompt section。
func resolvedPromptSectionByName(sections []prompt.ResolvedPromptSection, name string) (prompt.ResolvedPromptSection, bool) {
	for _, section := range sections {
		if section.Name == name {
			return section, true
		}
	}
	return prompt.ResolvedPromptSection{}, false
}
