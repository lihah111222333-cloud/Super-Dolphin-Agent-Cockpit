package codexapp

import (
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// TestBuildSkillPromptInput_FullMode 默认 env 未设置时必须保持 legacy writer。
func TestBuildSkillPromptInput_FullMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "go-testing", Mode: dto.SkillModeFull, Prompt: "run go test -race"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("full mode should inject")
	}
	want := "skills:\n- go-testing\n\n[skill:go-testing]\nrun go test -race"
	if item.Text != want {
		t.Fatalf("full mode output:\ngot  %q\nwant %q", item.Text, want)
	}
}

// TestBuildSkillPromptInput_V1FullMode 灰度启用 v1 时产出 paired markers。
func TestBuildSkillPromptInput_V1FullMode(t *testing.T) {
	t.Setenv("SKILL_WRITER_FORMAT", "v1")
	skills := []dto.SkillRef{
		{Name: "go-testing", Mode: dto.SkillModeFull, Prompt: "run go test -race"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("full mode should inject")
	}
	want := "skills:\n- go-testing\n\n[skill:go-testing::full@v1]\nrun go test -race\n[/skill:go-testing::full@v1]"
	if item.Text != want {
		t.Fatalf("v1 full mode output:\ngot  %q\nwant %q", item.Text, want)
	}
}

// TestBuildSkillPromptInput_SummaryMode 默认 legacy summary 保留中文提示文本。
func TestBuildSkillPromptInput_SummaryMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "rpc-tracing", Mode: dto.SkillModeSummary, Summary: "trace bus/router flow"},
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("summary mode should inject")
	}
	if !strings.Contains(item.Text, "[skill:rpc-tracing]") {
		t.Fatalf("missing legacy summary header: %q", item.Text)
	}
	if !strings.Contains(item.Text, "摘要: trace bus/router flow") {
		t.Fatalf("summary block must include legacy 摘要 text: %q", item.Text)
	}
	if !strings.Contains(item.Text, `使用方式: Call skill_expand_body("rpc-tracing") for full body`) {
		t.Fatalf("summary block must include skill_expand_body pointer: %q", item.Text)
	}
}

func TestBuildSkillPromptInput_V1SummaryMode(t *testing.T) {
	t.Setenv("SKILL_WRITER_FORMAT", "v1")
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
	if !strings.Contains(item.Text, "摘要: trace bus/router flow") {
		t.Fatalf("v1 summary block must include legacy 摘要 text: %q", item.Text)
	}
	if !strings.Contains(item.Text, `使用方式: Call skill_expand_body("rpc-tracing") for full body`) {
		t.Fatalf("v1 summary block must include skill_expand_body pointer: %q", item.Text)
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
	t.Setenv("SKILL_WRITER_FORMAT", "v1")
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

// TestBuildSkillPromptInput_UnspecifiedModeUsesSummaryDefault 锁定 codexapp sink-level
// 默认策略：即便新入口直接调用 buildSkillPromptInput，Mode=Unspecified 也只能渲染
// Summary + tool pointer，不得回退为 Full/eager body。
func TestBuildSkillPromptInput_UnspecifiedModeUsesSummaryDefault(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "legacy", Prompt: "FULL_BODY_NEVER_HERE", Summary: "short summary"}, // Mode 未设置
	}
	item, ok := buildSkillPromptInput(skills)
	if !ok {
		t.Fatalf("unspecified mode should inject summary default")
	}
	want := "skills:\n- legacy\n\n[skill:legacy]\n摘要: short summary\n使用方式: Call skill_expand_body(\"legacy\") for full body"
	if item.Text != want {
		t.Fatalf("unspecified mode summary output:\ngot  %q\nwant %q", item.Text, want)
	}
	if strings.Contains(item.Text, "FULL_BODY_NEVER_HERE") {
		t.Fatalf("unspecified mode leaked full body: %q", item.Text)
	}
}

// TestBuildSkillPromptInput_NameListFallbackWhenBodyMissing P20.2 §4：
// 所有 skill 正文 / summary 缺失（仅 Name）时不再 silent drop，而是以 `skills:\n- name` 名单单独注入。
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
		{Name: "visible"},                         // codexapp Summary default；无 summary 时 name-list fallback ok
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
	// P22 P4 契约：codexapp 无原生 skill 机制，不需要 cwd，所以不管 cwd 情况如何
	// 都返回 (nil, nil)——不像 claudecli 那样在空 cwd 时必须 ErrMissingCWD。
	for _, cwd := range []string{"", "/nonexistent", t.TempDir()} {
		names, err := port.DetectNativeSkills(cwd)
		if err != nil {
			t.Errorf("cwd=%q: err = %v, want nil (codexapp has no native skills, no cwd requirement)", cwd, err)
		}
		if names != nil {
			t.Errorf("cwd=%q: names = %v, want nil", cwd, names)
		}
	}
}

func TestCodexSkillInjectionPort_ReservedTokens(t *testing.T) {
	port := NewSkillInjectionPort()
	if got := port.ReservedTokens(); got != 3000 {
		t.Fatalf("default token budget = %d, want 3000 (P20.1 §3.7)", got)
	}
}
