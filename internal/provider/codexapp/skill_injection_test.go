package codexapp

import (
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// TestBuildSkillPromptInput_FullMode Full skill 必须产出带成对 header/footer 的新格式块，
// 并包括 P20.2 §4 的 name-list 前缀。
func TestBuildSkillPromptInput_FullMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "go-testing", Mode: dto.SkillModeFull, Prompt: "run go test -race"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("full mode should inject")
	}
	want := "skills:\n- go-testing\n\n[skill:go-testing::full@v1]\nrun go test -race\n[/skill:go-testing::full@v1]"
	if item.Text != want {
		t.Fatalf("full mode output:\ngot  %q\nwant %q", item.Text, want)
	}
}

// TestBuildSkillPromptInput_SummaryMode Summary 模式仍保留 legacy marker，直到 p20.9 读端先发。
func TestBuildSkillPromptInput_SummaryMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "rpc-tracing", Mode: dto.SkillModeSummary, Summary: "trace bus/router flow"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("summary mode should inject")
	}
	want := "skills:\n- rpc-tracing\n\n[skill:rpc-tracing]\n摘要: trace bus/router flow\n使用方式: Call skill_expand_body(\"rpc-tracing\") for full body"
	if item.Text != want {
		t.Fatalf("summary mode output:\ngot  %q\nwant %q", item.Text, want)
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
	for _, must := range []string{"[skill:alpha::full@v1]", "[skill:delta]", `skill_expand_body("delta")`} {
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
// 旧 payload {name, prompt}（Mode="" 空值）→ 按 Full 注入，name-list 同时出现。
func TestBuildSkillPromptInput_LegacyPayloadEmptyModeUsesFull(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "legacy", Prompt: "old body"}, // Mode 未设置
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("legacy payload should inject as Full")
	}
	want := "skills:\n- legacy\n\n[skill:legacy::full@v1]\nold body\n[/skill:legacy::full@v1]"
	if item.Text != want {
		t.Fatalf("legacy payload output:\ngot  %q\nwant %q", item.Text, want)
	}
}

// TestBuildSkillPromptInput_NameListFallbackWhenBodyMissing P20.2 §4：
// 所有 skill 正文缺失（仅 Name）时不再 silent drop，而是以 `skills:\n- name` 名单单独注入。
func TestBuildSkillPromptInput_NameListFallbackWhenBodyMissing(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "planner"},
		{Name: "reviewer"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("name-only skill must produce a name-list fallback (no silent drop)")
	}
	want := "skills:\n- planner\n- reviewer"
	if item.Text != want {
		t.Fatalf("fallback output:\ngot  %q\nwant %q", item.Text, want)
	}
}

// TestBuildSkillPromptInput_NameListFallbackSkipsNoneAndInvalid P20.2 §4 × P20.1 §3.5：
// None 与非法 mode 的 skill 既不进 name-list也不进 block；仅 Full/Summary skill 出现在名单里。
func TestBuildSkillPromptInput_NameListFallbackSkipsNoneAndInvalid(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "visible"},                         // legacy Full → name-list ok
		{Name: "hidden", Mode: dto.SkillModeNone}, // 剩 name-list 也要跳
		{Name: "bogus", Mode: "banana"},           // invalid 同理
		{Name: "sumonly", Mode: dto.SkillModeSummary, Summary: "s"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("expected injection with mixed modes")
	}
	if !strings.HasPrefix(item.Text, "skills:\n- visible\n- sumonly\n\n") {
		t.Fatalf("name-list must only include visible+sumonly: %q", item.Text)
	}
	for _, unwanted := range []string{"- hidden", "- bogus", "[skill:hidden", "[skill:bogus"} {
		if strings.Contains(item.Text, unwanted) {
			t.Fatalf("output must not contain %q: %q", unwanted, item.Text)
		}
	}
}

// TestBuildSkillPromptInput_AllSkippedReturnsFalse 全部被跳过时返回 ok=false。
// P20.2 §4 后：“全部跳过”指所有 skill 的 Mode.Effective()==None；Full+空 body 仍会产出 name-list。
func TestBuildSkillPromptInput_AllSkippedReturnsFalse(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "a", Mode: dto.SkillModeNone},
		{Name: "b", Mode: "banana"},
		{Name: ""},    // 空 name
		{Name: "   "}, // trim 后空
	}
	_, ok := buildSkillPromptInput(skills)
	if ok {
		t.Fatalf("all-skipped should return ok=false")
	}
}

// ============================================================================
// P20.1 Phase 7 SkillInjectionPort (codexapp)
// ============================================================================

func TestCodexSkillInjectionPort_DetectNativeSkills_AlwaysEmpty(t *testing.T) {
	port := NewSkillInjectionPort()
	// 即使在真实目录下也应返回 nil——Codex CLI 无原生 skill 机制
	if got := port.DetectNativeSkills(""); got != nil {
		t.Fatalf("empty cwd should return nil, got %v", got)
	}
	if got := port.DetectNativeSkills("/nonexistent"); got != nil {
		t.Fatalf("non-existent cwd should return nil, got %v", got)
	}
	if got := port.DetectNativeSkills(t.TempDir()); got != nil {
		t.Fatalf("real tmp dir should also return nil, got %v", got)
	}
}

func TestCodexSkillInjectionPort_BuildTurnSectionUsesLegacySummaryMarkerAndNameListFallback(t *testing.T) {
	port := NewSkillInjectionPort()
	got, ok := port.BuildTurnSection([]dto.SkillRef{
		{Name: "planner", Prompt: "plan before coding"},
		{Name: "reviewer", Mode: dto.SkillModeSummary, Summary: "review the diff"},
		{Name: "silent", Mode: dto.SkillModeNone, Prompt: "hidden"},
	})
	if !ok {
		t.Fatal("BuildTurnSection() ok = false, want true")
	}
	for _, want := range []string{
		"skills:\n- planner\n- reviewer",
		"[skill:planner::full@v1]",
		"[skill:reviewer]\n摘要: review the diff",
		`skill_expand_body("reviewer")`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildTurnSection() missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "silent") {
		t.Fatalf("BuildTurnSection() should skip none-mode skill, got %q", got)
	}
}

func TestCodexSkillInjectionPort_InjectL1ManifestAppendsManifest(t *testing.T) {
	port := NewSkillInjectionPort()
	if got := port.InjectL1Manifest("base instructions", "skills manifest"); got != "base instructions\n\nskills manifest" {
		t.Fatalf("InjectL1Manifest() = %q", got)
	}
}

func TestCodexSkillInjectionPort_ReservedTokens(t *testing.T) {
	port := NewSkillInjectionPort()
	if got := port.ReservedTokens(); got != 3000 {
		t.Fatalf("default token budget = %d, want 3000 (P20.1 §3.7)", got)
	}
}
