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
		"### 3. save rules",
		"### 4. access rules",
		"### 5. trust rules",
		"### 6. exclusions",
		"### 7. memory vs plan/tasks",
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
		"local_unavailable",
		"Verify referenced functions, flags, and types still exist",
		"`Searching past context` is deferred",
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
	if !strings.Contains(text, "When `skipIndex` is enabled") {
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
}

func TestMemoryRulesProviderRegistersStartOnlyDynamicSection(t *testing.T) {
	provider := NewRulesProvider(&Config{Enabled: true}, NewMemoryRuleEngine())
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
	if !strings.Contains(start.BaseInstructions, "## "+prompt.DynamicSectionMemory) {
		t.Fatalf("BaseInstructions missing memory dynamic section:\n%s", start.BaseInstructions)
	}
	if !strings.Contains(start.BaseInstructions, "### 2. taxonomy") {
		t.Fatalf("BaseInstructions missing rendered memory rules:\n%s", start.BaseInstructions)
	}

	turn, err := svc.AssembleTurn(context.Background(), prompt.TurnInput{})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.Contains(turn.UserContextText, "## "+prompt.DynamicSectionMemory) {
		t.Fatalf("UserContextText unexpectedly contains memory dynamic section:\n%s", turn.UserContextText)
	}
}
