package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestStaticSectionsExposeRequestedSlots(t *testing.T) {
	sections := StaticSections()
	want := []string{
		SectionIdentity,
		SectionSystemConstraints,
		SectionEngineering,
		SectionActions,
		SectionToolPreferences,
		SectionStyle,
		SectionOutputEfficiency,
	}
	if len(sections) != len(want) {
		t.Fatalf("len(StaticSections()) = %d, want %d", len(sections), len(want))
	}
	for i, name := range want {
		if sections[i].Name != name {
			t.Fatalf("StaticSections()[%d].Name = %q, want %q", i, sections[i].Name, name)
		}
		if sections[i].Region != PromptRegionStatic {
			t.Fatalf("StaticSections()[%d].Region = %v, want %v", i, sections[i].Region, PromptRegionStatic)
		}
	}
}

func TestStaticSectionsRenderNonEmptyContent(t *testing.T) {
	for _, section := range StaticSections() {
		content, err := section.Compute(context.Background(), SectionContext{})
		if err != nil {
			t.Fatalf("section %q Compute() error = %v", section.Name, err)
		}
		if content == nil || *content == "" {
			t.Fatalf("section %q returned empty content", section.Name)
		}
	}
}

func TestStaticSectionsCoverCriticalClaudeSemantics(t *testing.T) {
	sectionsByName := make(map[string]string, len(StaticSections()))
	for _, section := range StaticSections() {
		content, err := section.Compute(context.Background(), SectionContext{})
		if err != nil {
			t.Fatalf("section %q Compute() error = %v", section.Name, err)
		}
		if content == nil {
			t.Fatalf("section %q returned nil content", section.Name)
		}
		sectionsByName[section.Name] = *content
	}

	checks := map[string][]string{
		SectionIdentity: {
			"authorized security testing",
			"Never invent or guess URLs",
		},
		SectionSystemConstraints: {
			"<user-prompt-submit-hook>",
			"prompt injection",
		},
		SectionEngineering: {
			"instruction is unclear or generic",
			"impossible-case validation",
			"truthfully if checks fail or were not run",
		},
		SectionActions: {
			"Local, reversible actions",
			"durable instructions pre-authorize",
			"locks, or conflicts",
		},
		SectionToolPreferences: {
			"head, tail",
			"Batch independent tool calls in parallel",
		},
		SectionStyle: {
			"file_path:line_number",
			"owner/repo#123",
		},
		SectionOutputEfficiency: {
			"rehashing the user's request",
			"not code or tool calls",
		},
	}

	for name, snippets := range checks {
		content := sectionsByName[name]
		for _, snippet := range snippets {
			if !strings.Contains(content, snippet) {
				t.Fatalf("section %q missing snippet %q\ncontent:\n%s", name, snippet, content)
			}
		}
	}
}
