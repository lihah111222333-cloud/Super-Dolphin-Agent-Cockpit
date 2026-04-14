package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
		content, err := section.Compute(context.Background(), SectionContext{BuildCtx: BuildCtx{}})
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
			"must NEVER generate or guess URLs",
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

func TestStaticSectionsIdentityUsesOutputStyleFraming(t *testing.T) {
	for _, section := range StaticSections() {
		if section.Name != SectionIdentity {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{
				OutputStyleConfig: &contract.OutputStyleConfig{Name: "Explanatory"},
			},
		})
		if err != nil {
			t.Fatalf("identity Compute() error = %v", err)
		}
		if content == nil || !strings.Contains(*content, `according to your "Output Style" below`) {
			t.Fatalf("identity content = %v, want output-style framing", content)
		}
		return
	}
	t.Fatal("identity section not found")
}

func TestStaticSectionsIdentitySkipsOutputStyleFramingForNonRenderableConfig(t *testing.T) {
	keepCodingInstructions := false
	for _, section := range StaticSections() {
		if section.Name != SectionIdentity {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{
				OutputStyleConfig: &contract.OutputStyleConfig{
					Source:                  "user-config",
					KeepCodingInstructions: &keepCodingInstructions,
				},
			},
		})
		if err != nil {
			t.Fatalf("identity Compute() error = %v", err)
		}
		if content == nil {
			t.Fatal("identity content = nil, want base identity instructions")
		}
		if strings.Contains(*content, `according to your "Output Style" below`) {
			t.Fatalf("identity content = %q, want default framing when output style is not renderable", *content)
		}
		return
	}
	t.Fatal("identity section not found")
}

func TestStaticSectionsEngineeringSkipsWhenKeepCodingDisabled(t *testing.T) {
	keepCodingInstructions := false
	for _, section := range StaticSections() {
		if section.Name != SectionEngineering {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{KeepCodingInstructions: &keepCodingInstructions},
		})
		if err != nil {
			t.Fatalf("engineering Compute() error = %v", err)
		}
		if content != nil {
			t.Fatalf("engineering content = %q, want nil when keepCodingInstructions=false", *content)
		}
		return
	}
	t.Fatal("engineering section not found")
}
