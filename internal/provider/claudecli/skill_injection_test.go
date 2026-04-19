package claudecli

import (
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// TestBuildSkillList_FiltersNoneMode P20.1 §3.3 加固：
// Mode=None 的 skill 必须不出现在 "skills: - xxx" 列表里，否则模型会看见名字
// 但找不到 body。本测试用最小桩数据锁定该不变量。
func TestBuildSkillList_FiltersNoneMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "visible-full", Mode: dto.SkillModeFull, Prompt: "ignored by list fn"},
		{Name: "hidden-none", Mode: dto.SkillModeNone, Prompt: "will not be listed"},
		{Name: "visible-summary", Mode: dto.SkillModeSummary, Summary: "s"},
		{Name: "unspecified-defaults-full", Prompt: "no mode field"},
	}
	list := buildSkillList(skills)
	if !strings.Contains(list, "visible-full") {
		t.Fatalf("Full skill must appear in list: %q", list)
	}
	if !strings.Contains(list, "visible-summary") {
		t.Fatalf("Summary skill must appear in list: %q", list)
	}
	if !strings.Contains(list, "unspecified-defaults-full") {
		t.Fatalf("Unspecified (legacy) should default to full and appear: %q", list)
	}
	if strings.Contains(list, "hidden-none") {
		t.Fatalf("None-mode skill MUST NOT appear in list (P20.1 §3.3): %q", list)
	}
}

// TestBuildSkillList_FiltersInvalidMode 非法 mode（经 Effective() 降级为 None）
// 同样不得暴露名字。
func TestBuildSkillList_FiltersInvalidMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "good", Mode: dto.SkillModeFull, Prompt: "x"},
		{Name: "bad-mode", Mode: "banana", Prompt: "x"}, // Effective → None
	}
	list := buildSkillList(skills)
	if !strings.Contains(list, "good") {
		t.Fatalf("good skill should appear: %q", list)
	}
	if strings.Contains(list, "bad-mode") {
		t.Fatalf("invalid-mode skill MUST NOT appear: %q", list)
	}
}

// TestBuildSkillList_EmptyWhenAllFiltered 若所有 skill 都被过滤掉（None / 无名），
// buildSkillList 应返回空串（不输出孤零零的 "skills:" header）。
func TestBuildSkillList_EmptyWhenAllFiltered(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "all-none", Mode: dto.SkillModeNone},
		{Name: "", Mode: dto.SkillModeFull},
		{Name: "another-none", Mode: dto.SkillModeNone},
	}
	if list := buildSkillList(skills); list != "" {
		t.Fatalf("all-filtered case should yield empty list, got %q", list)
	}
}
