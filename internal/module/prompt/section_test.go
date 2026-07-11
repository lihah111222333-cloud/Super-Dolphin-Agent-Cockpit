package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
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
					Source:                 "user-config",
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

func TestStaticSectionsToolPreferencesUsePlannerAwareHint(t *testing.T) {
	for _, section := range StaticSections() {
		if section.Name != SectionToolPreferences {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{EnabledTools: []string{"file", "update_plan"}},
		})
		if err != nil {
			t.Fatalf("tool_preferences Compute() error = %v", err)
		}
		if content == nil || !strings.Contains(*content, "update_plan or task_create_dag") {
			t.Fatalf("tool_preferences content = %v, want planner-aware hint", content)
		}
		return
	}
	t.Fatal("tool_preferences section not found")
}

func TestStaticSectionsToolPreferencesRouteShellAndCodeWork(t *testing.T) {
	for _, section := range StaticSections() {
		if section.Name != SectionToolPreferences {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{EnabledTools: []string{"exec_command", "file", "grep", "inspect", "xref"}},
		})
		if err != nil {
			t.Fatalf("tool_preferences Compute() error = %v", err)
		}
		if content == nil {
			t.Fatal("tool_preferences content = nil, want tool routing rules")
		}
		for _, must := range []string{"exec_command", "ordinary shell", "code understanding", "diagnostics", "LSP"} {
			if !strings.Contains(*content, must) {
				t.Fatalf("tool_preferences content = %q, want %q", *content, must)
			}
		}
		return
	}
	t.Fatal("tool_preferences section not found")
}

func TestStaticSectionsToolPreferencesSuppressedTools(t *testing.T) {
	for _, section := range StaticSections() {
		if section.Name != SectionToolPreferences {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{
				SuppressedTools: []string{"Bash", "Edit", "Read"},
			},
		})
		if err != nil {
			t.Fatalf("Compute() error = %v", err)
		}
		if content == nil {
			t.Fatal("tool_preferences content is nil, want suppressed tools bullet")
		}
		if !strings.Contains(*content, "Do NOT use") {
			t.Fatalf("content = %q, want 'Do NOT use' bullet", *content)
		}
		if !strings.Contains(*content, "Bash, Edit, Read") {
			t.Fatalf("content = %q, want tool names 'Bash, Edit, Read'", *content)
		}
		return
	}
	t.Fatal("tool_preferences section not found")
}

func TestStaticSectionsToolPreferencesNoSuppressedTools(t *testing.T) {
	for _, section := range StaticSections() {
		if section.Name != SectionToolPreferences {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{},
		})
		if err != nil {
			t.Fatalf("Compute() error = %v", err)
		}
		if content != nil && strings.Contains(*content, "Do NOT use") {
			t.Fatalf("content = %q, should NOT contain suppressed tools bullet when SuppressedTools is empty", *content)
		}
		return
	}
	t.Fatal("tool_preferences section not found")
}

func TestStaticSectionsToolPreferencesUseReplModeBranch(t *testing.T) {
	for _, section := range StaticSections() {
		if section.Name != SectionToolPreferences {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{
				EnabledTools: []string{"update_plan"},
				SessionFlags: map[string]bool{"repl_mode": true},
			},
		})
		if err != nil {
			t.Fatalf("tool_preferences Compute() error = %v", err)
		}
		if content == nil || !strings.Contains(*content, "In REPL mode") {
			t.Fatalf("tool_preferences content = %v, want repl branch", content)
		}
		if strings.Contains(*content, "Do not reach for shell fallbacks") {
			t.Fatalf("tool_preferences content = %q, want repl branch without default shell-fallback bullet", *content)
		}
		return
	}
	t.Fatal("tool_preferences section not found")
}
