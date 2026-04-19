package codexapp

import (
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// TestBuildSkillPromptInput_FullMode Full skill 必须产出带成对 header/footer 的新格式块。
func TestBuildSkillPromptInput_FullMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "go-testing", Mode: dto.SkillModeFull, Prompt: "run go test -race"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("full mode should inject")
	}
	want := "[skill:go-testing::full@v1]\nrun go test -race\n[/skill:go-testing::full@v1]"
	if item.Text != want {
		t.Fatalf("full mode output:\ngot  %q\nwant %q", item.Text, want)
	}
}

// TestBuildSkillPromptInput_SummaryMode Summary 模式产出带 skill_expand_body 指针的块。
func TestBuildSkillPromptInput_SummaryMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "rpc-tracing", Mode: dto.SkillModeSummary, Summary: "trace bus/router flow"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("summary mode should inject")
	}
	if !strings.Contains(item.Text, "[skill:rpc-tracing::summary@v1]") {
		t.Fatalf("missing summary header: %q", item.Text)
	}
	if !strings.Contains(item.Text, `skill_expand_body("rpc-tracing")`) {
		t.Fatalf("summary block must include skill_expand_body pointer: %q", item.Text)
	}
	if !strings.Contains(item.Text, "[/skill:rpc-tracing::summary@v1]") {
		t.Fatalf("missing summary footer: %q", item.Text)
	}
}

// TestBuildSkillPromptInput_NoneModeSkipped P20.1 §3.3 加固：None-mode skill 不注入。
func TestBuildSkillPromptInput_NoneModeSkipped(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "hidden", Mode: dto.SkillModeNone, Prompt: "secret body"},
	}
	_, ok := buildSkillPromptInput(skills)
	if ok {
		t.Fatalf("None-mode skill MUST NOT be injected")
	}
}

// TestBuildSkillPromptInput_InvalidModeSkipped P20.1 §3.5 加固：非法 mode 保守降级为 None。
func TestBuildSkillPromptInput_InvalidModeSkipped(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "bad", Mode: "banana", Prompt: "body", Summary: "sum"},
	}
	_, ok := buildSkillPromptInput(skills)
	if ok {
		t.Fatalf("invalid mode MUST NOT be injected (P20.1 §3.5 conservative downgrade)")
	}
}

// TestBuildSkillPromptInput_MixedModes 多 skill 混合模式：Full/Summary 注入，None/invalid 跳过。
func TestBuildSkillPromptInput_MixedModes(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "alpha", Mode: dto.SkillModeFull, Prompt: "A"},
		{Name: "beta", Mode: dto.SkillModeNone, Prompt: "B"},
		{Name: "gamma", Mode: "invalid", Prompt: "C", Summary: "cs"},
		{Name: "delta", Mode: dto.SkillModeSummary, Summary: "D"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("at least 2 skills should inject")
	}
	for _, must := range []string{"[skill:alpha::full@v1]", "[skill:delta::summary@v1]"} {
		if !strings.Contains(item.Text, must) {
			t.Fatalf("missing %q in output: %q", must, item.Text)
		}
	}
	for _, mustNot := range []string{"[skill:beta", "[skill:gamma"} {
		if strings.Contains(item.Text, mustNot) {
			t.Fatalf("unexpected %q in output: %q", mustNot, item.Text)
		}
	}
}

// TestBuildSkillPromptInput_LegacyPayloadEmptyModeUsesFull 向后兼容：
// 旧 payload {name, prompt}（Mode="" 空值）→ 按 Full 注入。
func TestBuildSkillPromptInput_LegacyPayloadEmptyModeUsesFull(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "legacy", Prompt: "old body"}, // Mode 未设置
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("legacy payload should inject as Full")
	}
	want := "[skill:legacy::full@v1]\nold body\n[/skill:legacy::full@v1]"
	if item.Text != want {
		t.Fatalf("legacy payload output:\ngot  %q\nwant %q", item.Text, want)
	}
}

// TestBuildSkillPromptInput_AllSkippedReturnsFalse 全部被跳过时返回 ok=false。
func TestBuildSkillPromptInput_AllSkippedReturnsFalse(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "a", Mode: dto.SkillModeNone},
		{Name: "b", Mode: "banana"},
		{Name: "c", Mode: dto.SkillModeFull, Prompt: ""}, // 空 body
	}
	_, ok := buildSkillPromptInput(skills)
	if ok {
		t.Fatalf("all-skipped should return ok=false")
	}
}
