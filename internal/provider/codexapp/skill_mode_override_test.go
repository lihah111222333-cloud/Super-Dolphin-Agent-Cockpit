package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	moduleskill "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	moduleturn "github.com/anthropic-ai/super-agent-v3/internal/module/turn"
)

// TestOverrideSkillsToSummary_FlipsUnspecifiedToSummary 验证 codexapp Phase 1.5 v2 (B 路)
// 的核心行为：**仅 Mode=Unspecified**（隐式默认）被翻 Summary；显式 Full 不动。
func TestOverrideSkillsToSummary_FlipsUnspecifiedToSummary(t *testing.T) {
	in := []dto.SkillRef{
		{Name: "implicit"}, // Mode=Unspecified → 默认被翻 Summary
	}
	out := overrideSkillsToSummary(in)
	if len(out) != 1 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0].Mode != dto.SkillModeSummary {
		t.Fatalf("out[0].Mode = %q, want Summary", out[0].Mode)
	}
}

// TestOverrideSkillsToSummary_PreservesExplicitFull 验证显式 Mode=Full 不再被默默翻成
// Summary——P20.2 §4 与其它依赖明确 mode 语义的调用方保留意图。
func TestOverrideSkillsToSummary_PreservesExplicitFull(t *testing.T) {
	in := []dto.SkillRef{
		{Name: "explicit-full", Mode: dto.SkillModeFull},
	}
	out := overrideSkillsToSummary(in)
	if out[0].Mode != dto.SkillModeFull {
		t.Fatalf("explicit Full mutated to %q, want kept Full", out[0].Mode)
	}
}

// TestOverrideSkillsToSummary_PreservesNone 验证 native skill (Mode=None) 不被
// 翻成 Summary——否则会把原本应跳过的 native skill 误注入摘要文本。
// B 路下同时隐含验证 explicit Full 也不被覆盖。
func TestOverrideSkillsToSummary_PreservesNone(t *testing.T) {
	in := []dto.SkillRef{
		{Name: "native", Mode: dto.SkillModeNone},
		{Name: "explicit-summary", Mode: dto.SkillModeSummary, Summary: "keep"},
	}
	out := overrideSkillsToSummary(in)
	if out[0].Mode != dto.SkillModeNone {
		t.Fatalf("native Mode mutated: %q", out[0].Mode)
	}
	if out[1].Mode != dto.SkillModeSummary || out[1].Summary != "keep" {
		t.Fatalf("explicit Summary mutated: %+v", out[1])
	}
}

// TestOverrideSkillsToSummary_PreservesAlreadySummary 已经是 Summary 的 ref 保持
// 不变（幂等）。
func TestOverrideSkillsToSummary_PreservesAlreadySummary(t *testing.T) {
	in := []dto.SkillRef{{Name: "x", Mode: dto.SkillModeSummary, Summary: "kept"}}
	out := overrideSkillsToSummary(in)
	if out[0].Mode != dto.SkillModeSummary {
		t.Fatalf("Mode = %q", out[0].Mode)
	}
	if out[0].Summary != "kept" {
		t.Fatalf("Summary lost: %q", out[0].Summary)
	}
}

// TestOverrideSkillsToSummary_DoesNotMutateInput 锁定不修改入参——上游 turn 模块
// 复用同一个 SkillRef 切片，被覆盖会污染 claudecli 等其他 provider。
func TestOverrideSkillsToSummary_DoesNotMutateInput(t *testing.T) {
	in := []dto.SkillRef{{Name: "foo", Mode: dto.SkillModeFull}}
	_ = overrideSkillsToSummary(in)
	if in[0].Mode != dto.SkillModeFull {
		t.Fatalf("input mutated: %+v", in[0])
	}
}

// TestOverrideSkillsToSummary_EmptyInput 空切片 / nil 安全。
func TestOverrideSkillsToSummary_EmptyInput(t *testing.T) {
	if got := overrideSkillsToSummary(nil); got != nil {
		t.Fatalf("nil -> want nil, got %v", got)
	}
	if got := overrideSkillsToSummary([]dto.SkillRef{}); len(got) != 0 {
		t.Fatalf("empty -> want empty, got %v", got)
	}
}

// TestBuildTurnStartParams_AppliesSummaryOverride 集成层断言：buildTurnStartParams
// 走完后，turnInputsFromRequest 看到的是 Summary 模式渲染——确认 override 真的接到入口。
// B 路下只默认翻 Unspecified，本用例不设 Mode 让 override 生效。
func TestBuildTurnStartParams_AppliesSummaryOverride(t *testing.T) {
	req := dto.TurnRequest{
		Skills: []dto.SkillRef{
			{Name: "demo", Prompt: "FULL_BODY_NEVER_HERE", Summary: "summary line"},
		},
	}
	params := buildTurnStartParams("thread-x", req)
	// SelectedSkills 仅看名字，不应受影响。
	if len(params.SelectedSkills) != 1 || params.SelectedSkills[0] != "demo" {
		t.Fatalf("SelectedSkills = %v", params.SelectedSkills)
	}
	// Input 中的 skill 文本块：Summary 模式必须保留摘要 + tool pointer，且不含原始 Prompt body。
	var skillText string
	for _, item := range params.Input {
		if item.Type != "text" {
			continue
		}
		if strings.Contains(item.Text, "[skill:demo]") {
			skillText = item.Text
		}
		if strings.Contains(item.Text, "FULL_BODY_NEVER_HERE") {
			t.Fatalf("Summary mode leaked Full body: %q", item.Text)
		}
	}
	if skillText == "" {
		t.Fatalf("missing summary skill text block: %#v", params.Input)
	}
	if !strings.Contains(skillText, "摘要: summary line") {
		t.Fatalf("summary block missing summary line: %q", skillText)
	}
	if !strings.Contains(skillText, `使用方式: Call skill_expand_body("demo") for full body`) {
		t.Fatalf("summary block missing skill_expand_body pointer: %q", skillText)
	}
}

// TestPrepareTurnToBuildTurnStartParams_NameOnlySkillUsesSummary 走真实
// name-only skill 选择 + skill.Service hydrate + turn.Service.PrepareTurn + codexapp
// buildTurnStartParams，锁定普通业务链路：上游保持 Mode=Unspecified，codexapp
// adapter 才能把默认 skill 注入切到 Summary。
func TestPrepareTurnToBuildTurnStartParams_NameOnlySkillUsesSummary(t *testing.T) {
	cwd := t.TempDir()
	skillDir := filepath.Join(cwd, ".agent", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\nsummary: summary line\n---\nFULL_BODY_NEVER_HERE\n"), 0o644); err != nil {
		t.Fatalf("WriteFile SKILL.md: %v", err)
	}

	svc := moduleturn.NewServiceWithPromptAssemblyAndTurnContext(nil, nil, nil, moduleskill.NewService(cwd), nil)
	session := &summaryOverrideSession{threadID: "thread-real"}

	req, err := svc.PrepareTurn(context.Background(), session, moduleturn.PrepareInput{
		Prompt:               "please use demo",
		CWD:                  cwd,
		Skills:               []dto.SkillRef{{Name: "demo", Source: dto.SkillSourceManual}},
		ManualSkillSelection: true,
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if len(req.Skills) != 1 {
		t.Fatalf("PrepareTurn() skills = %#v", req.Skills)
	}
	if req.Skills[0].Mode != dto.SkillModeUnspecified {
		t.Fatalf("PrepareTurn() materialized Mode = %q, want Unspecified marker", req.Skills[0].Mode)
	}
	if req.Skills[0].Prompt != "" {
		t.Fatalf("PrepareTurn() must not hydrate untrusted full body without approval: %+v", req.Skills[0])
	}
	if req.Skills[0].Summary != "summary line" {
		t.Fatalf("PrepareTurn() Summary = %q, want summary line", req.Skills[0].Summary)
	}

	params := buildTurnStartParams(session.threadID, req)
	assertSummarySkillPrompt(t, params.Input, "demo", "summary line", "FULL_BODY_NEVER_HERE")
}

func assertSummarySkillPrompt(t *testing.T, inputs []turnInputItem, name, summary, forbiddenBody string) {
	t.Helper()
	var skillText string
	marker := "[skill:" + name + "]"
	for _, item := range inputs {
		if item.Type != "text" {
			continue
		}
		if strings.Contains(item.Text, forbiddenBody) {
			t.Fatalf("Summary mode leaked Full body: %q", item.Text)
		}
		if strings.Contains(item.Text, marker) {
			skillText = item.Text
		}
	}
	if skillText == "" {
		t.Fatalf("missing summary skill text block: %#v", inputs)
	}
	if !strings.Contains(skillText, "摘要: "+summary) {
		t.Fatalf("summary block missing summary line: %q", skillText)
	}
	if !strings.Contains(skillText, `使用方式: Call skill_expand_body("`+name+`") for full body`) {
		t.Fatalf("summary block missing skill_expand_body pointer: %q", skillText)
	}
}

type summaryOverrideSession struct {
	threadID string
	caps     dto.CapabilitySet
}

func (s *summaryOverrideSession) ThreadID() string { return s.threadID }

func (s *summaryOverrideSession) RolloutPath() string { return "" }

func (s *summaryOverrideSession) Capabilities() dto.CapabilitySet { return s.caps }

func (s *summaryOverrideSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}

func (s *summaryOverrideSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (s *summaryOverrideSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error { return nil }

func (s *summaryOverrideSession) ListThreads(context.Context) ([]dto.ThreadRef, error) { return nil, nil }

func (s *summaryOverrideSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}

func (s *summaryOverrideSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}

func (s *summaryOverrideSession) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

func (s *summaryOverrideSession) Close(context.Context) error { return nil }

func (s *summaryOverrideSession) ForceStop() error { return nil }
