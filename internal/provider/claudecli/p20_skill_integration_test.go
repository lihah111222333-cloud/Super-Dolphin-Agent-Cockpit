package claudecli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestP20SkillIntegrationNativeOverrideKeepsNameListAndCustomPrompt(t *testing.T) {
	t.Parallel()

	gitRoot := t.TempDir()
	writeNativeSkill(t, gitRoot, "native-tool")
	port := NewSkillInjectionPort()
	resolved := port.ApplyNativeOverrides([]dto.SkillRef{
		{Name: "native-tool", Mode: dto.SkillModeFull, Prompt: "native body", Summary: "native summary", Source: dto.SkillSourceManual},
		{Name: "planner", Mode: dto.SkillModeSummary, Summary: "do planning", Source: dto.SkillSourceTrigger},
	}, gitRoot, t.TempDir())
	if resolved[0].Mode != dto.SkillModeNone || resolved[0].Source != dto.SkillSourceNative {
		t.Fatalf("native override = %+v, want Mode=None + Source=Native", resolved[0])
	}
	if resolved[0].Prompt != "" || resolved[0].Summary != "" {
		t.Fatalf("native override should strip prompt/summary, got %+v", resolved[0])
	}

	section, ok := port.BuildTurnSection(resolved)
	if !ok {
		t.Fatal("BuildTurnSection() ok = false, want true")
	}
	text := composeTurnText(dto.TurnRequest{
		Inputs:      []dto.InputItem{{Type: "text", Content: "hello"}},
		Skills:      resolved,
		SkillPrompt: section,
	})
	if !strings.Contains(text, "skills:\n- native-tool\n- planner") {
		t.Fatalf("composeTurnText() = %q, want native + custom name list", text)
	}
	if strings.Contains(text, "native body") {
		t.Fatalf("composeTurnText() leaked native body: %q", text)
	}
	if !strings.Contains(text, "[skill:planner]\n摘要: do planning") {
		t.Fatalf("composeTurnText() = %q, want custom skill prompt carrier", text)
	}
}

func TestP20SkillIntegrationHistoryTrimPreservesSkillsPreludeAfterInjectedBlocks(t *testing.T) {
	t.Parallel()

	messages := []Message{{
		Role: "user",
		Content: "skills:\n- native-tool\n- planner\n\n[skill:planner]\n摘要: do planning\n使用方式: Call skill_expand_body(\"planner\") for full body\n\nhello world",
	}}
	got := trimClaudeHistory(messages, 0)
	if len(got) != 1 {
		t.Fatalf("len(trimClaudeHistory()) = %d, want 1", len(got))
	}
	if strings.Contains(got[0].Content, "[skill:") {
		t.Fatalf("trimClaudeHistory() still leaked injected block: %q", got[0].Content)
	}
	if !strings.Contains(got[0].Content, "skills:\n- native-tool\n- planner") {
		t.Fatalf("trimClaudeHistory() = %q, want skills prelude preserved", got[0].Content)
	}
	if strings.Contains(got[0].Content, "hello world") {
		t.Fatalf("trimClaudeHistory() = %q, want only skills prelude to remain", got[0].Content)
	}
}

func writeNativeSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("native body"), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}
}
