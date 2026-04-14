package memory

import (
	"context"
	"strings"
	"testing"

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
	text := NewMemoryRuleEngine().BuildMemoryLines(MemoryRuleOptions{})
	orderedHeadings := []string{
		"### 1. memory system",
		"### 2. taxonomy",
		"### 3. exclusions",
		"### 4. save rules",
		"### 5. access rules",
		"### 6. trust rules",
		"### 7. memory vs plan/tasks",
		"### 8. searching past context",
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
		"runtime retrieval work instead of prompt-level directory or transcript grep",
	} {
		if !strings.Contains(text, snippet) {
			t.Fatalf("prompt missing required snippet %q", snippet)
		}
	}
}

func TestBuildMemoryLinesSkipIndexAndExtraGuidelines(t *testing.T) {
	text := NewMemoryRuleEngine().BuildMemoryLines(MemoryRuleOptions{
		SkipIndex:       true,
		ExtraGuidelines: []string{"Keep explanations short.", "Prefer absolute dates in summaries."},
	})
	if !strings.Contains(text, "When `skipIndex` is enabled, write or update the topic file only") {
		t.Fatalf("skipIndex rule missing from prompt:\n%s", text)
	}
	extraIndex := strings.Index(text, "### 8. extra guidelines")
	if extraIndex == -1 {
		t.Fatalf("extra guidelines section missing from prompt:\n%s", text)
	}
	extra := text[extraIndex:]
	for _, snippet := range []string{"Keep explanations short.", "Prefer absolute dates in summaries."} {
		if !strings.Contains(extra, snippet) {
			t.Fatalf("extra guidelines section missing %q", snippet)
		}
	}
	searchIndex := strings.Index(text, "### 9. searching past context")
	if searchIndex == -1 {
		t.Fatalf("searching past context section missing from prompt:\n%s", text)
	}
	if searchIndex <= extraIndex {
		t.Fatalf("searching past context section should appear after extra guidelines")
	}
}

func TestMemoryRulesProviderRegistersStartOnlyDynamicSection(t *testing.T) {
	provider := NewRulesProvider(&Config{
		Enabled:         true,
		SkipIndex:       true,
		ExtraGuidelines: []string{"Keep explanations short."},
	}, NewMemoryRuleEngine())
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
		"### 8. extra guidelines",
		"Keep explanations short.",
	} {
		if !strings.Contains(start.BaseInstructions, snippet) {
			t.Fatalf("BaseInstructions missing %q:\n%s", snippet, start.BaseInstructions)
		}
	}

	turn, err := svc.AssembleTurn(context.Background(), prompt.TurnInput{})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.Contains(turn.UserContextText, "### 1. memory system") {
		t.Fatalf("UserContextText unexpectedly contains memory dynamic section:\n%s", turn.UserContextText)
	}
}
