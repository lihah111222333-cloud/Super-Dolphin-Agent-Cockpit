package nativefilter

import (
	"reflect"
	"testing"
)

// ============================================================================
// AggregateClaude
// ============================================================================

func TestAggregateClaude_EmptyBaseAndSkills(t *testing.T) {
	got := AggregateClaude(ClaudeBase{}, nil)
	if len(got.Permissions.Deny) != 0 {
		t.Errorf("Deny should be empty: %+v", got.Permissions.Deny)
	}
	if got.Permissions.Allow != nil {
		t.Errorf("Allow should stay nil for no-allowlist semantic: %+v", got.Permissions.Allow)
	}
}

func TestAggregateClaude_BaseDisabledToolsRawAndSkillsWrapped(t *testing.T) {
	base := ClaudeBase{
		DisabledSkills: []string{"math-olympiad", "playground"},
		DisabledTools:  []string{"Bash", "Read"},
	}
	got := AggregateClaude(base, nil)
	want := []string{"Bash", "Read", "Skill(math-olympiad)", "Skill(playground)"}
	if !reflect.DeepEqual(got.Permissions.Deny, want) {
		t.Errorf("Deny:\nwant %v\ngot  %v", want, got.Permissions.Deny)
	}
}

func TestAggregateClaude_SkillReplacesNativeJoinsBaseDisabledSkills(t *testing.T) {
	base := ClaudeBase{DisabledSkills: []string{"math-olympiad"}}
	skills := []SkillSummary{
		{Name: "alpha", ReplacesNative: map[string][]string{"claude": {"security-guidance/security-review"}}},
		{Name: "beta", ReplacesNative: map[string][]string{"claude": {"playground", "math-olympiad"}}}, // dup with base
		{Name: "gamma", ReplacesNative: map[string][]string{"codex": {"only-codex"}}},                  // codex 不入 claude deny
	}
	got := AggregateClaude(base, skills)
	want := []string{
		"Skill(math-olympiad)",
		"Skill(playground)",
		"Skill(security-guidance/security-review)",
	}
	if !reflect.DeepEqual(got.Permissions.Deny, want) {
		t.Errorf("Deny:\nwant %v\ngot  %v", want, got.Permissions.Deny)
	}
}

func TestAggregateClaude_DisabledSkillIgnored(t *testing.T) {
	skills := []SkillSummary{
		{Name: "alpha", Disabled: true, ReplacesNative: map[string][]string{"claude": {"never-shown"}}, AllowedTools: []string{"Bash"}},
		{Name: "beta", ReplacesNative: map[string][]string{"claude": {"shown"}}},
	}
	got := AggregateClaude(ClaudeBase{}, skills)
	want := []string{"Skill(shown)"}
	if !reflect.DeepEqual(got.Permissions.Deny, want) {
		t.Errorf("Deny:\nwant %v\ngot  %v", want, got.Permissions.Deny)
	}
	if got.Permissions.Allow != nil {
		t.Errorf("disabled skill's AllowedTools must not contribute to Allow: %v", got.Permissions.Allow)
	}
}

func TestAggregateClaude_DenyDedupAndTrim(t *testing.T) {
	base := ClaudeBase{
		DisabledTools:  []string{" Bash ", "Bash", "  ", ""},
		DisabledSkills: []string{"math-olympiad", " math-olympiad "},
	}
	skills := []SkillSummary{
		{Name: "x", ReplacesNative: map[string][]string{"claude": {"math-olympiad"}}},
	}
	got := AggregateClaude(base, skills)
	want := []string{"Bash", "Skill(math-olympiad)"}
	if !reflect.DeepEqual(got.Permissions.Deny, want) {
		t.Errorf("Deny:\nwant %v\ngot  %v", want, got.Permissions.Deny)
	}
}

func TestAggregateClaude_AllowedToolsNilWhenNothingDeclared(t *testing.T) {
	skills := []SkillSummary{{Name: "x"}, {Name: "y"}}
	got := AggregateClaude(ClaudeBase{}, skills)
	if got.Permissions.Allow != nil {
		t.Errorf("Allow should stay nil: %v", got.Permissions.Allow)
	}
}

func TestAggregateClaude_AllowedToolsBaseOnly(t *testing.T) {
	tools := []string{"Read", "Edit"}
	base := ClaudeBase{AllowedTools: &tools}
	got := AggregateClaude(base, nil)
	want := []string{"Edit", "Read"}
	if !reflect.DeepEqual(got.Permissions.Allow, want) {
		t.Errorf("Allow:\nwant %v\ngot  %v", want, got.Permissions.Allow)
	}
}

func TestAggregateClaude_AllowedToolsUnionAcrossSkills(t *testing.T) {
	base := ClaudeBase{AllowedTools: nil} // 仅 skills 触发 allowlist
	skills := []SkillSummary{
		{Name: "alpha", AllowedTools: []string{"Read", "Bash"}},
		{Name: "beta", AllowedTools: []string{"Bash", "Edit"}},
	}
	got := AggregateClaude(base, skills)
	want := []string{"Bash", "Edit", "Read"}
	if !reflect.DeepEqual(got.Permissions.Allow, want) {
		t.Errorf("Allow union:\nwant %v\ngot  %v", want, got.Permissions.Allow)
	}
}

func TestAggregateClaude_AllowedToolsBasePlusSkills(t *testing.T) {
	tools := []string{"Read"}
	base := ClaudeBase{AllowedTools: &tools}
	skills := []SkillSummary{
		{Name: "alpha", AllowedTools: []string{"Bash"}},
	}
	got := AggregateClaude(base, skills)
	want := []string{"Bash", "Read"}
	if !reflect.DeepEqual(got.Permissions.Allow, want) {
		t.Errorf("Allow:\nwant %v\ngot  %v", want, got.Permissions.Allow)
	}
}

func TestAggregateClaude_AllowedToolsExplicitEmptyBase(t *testing.T) {
	// base.AllowedTools = []（显式空 allowlist）→ 不带 skill AllowedTools 时
	// Allow 应当是非 nil 空切片，区分"不施加 allowlist"。
	tools := []string{}
	base := ClaudeBase{AllowedTools: &tools}
	got := AggregateClaude(base, nil)
	if got.Permissions.Allow == nil {
		t.Fatal("explicit empty base allowlist must surface as non-nil empty slice")
	}
	if len(got.Permissions.Allow) != 0 {
		t.Errorf("Allow should be empty: %v", got.Permissions.Allow)
	}
}

func TestAggregateClaude_OutputDeterministicAcrossSkillOrder(t *testing.T) {
	mk := func(order []SkillSummary) ClaudeSettings {
		return AggregateClaude(ClaudeBase{DisabledTools: []string{"Bash"}}, order)
	}
	a := []SkillSummary{
		{Name: "x", ReplacesNative: map[string][]string{"claude": {"foo"}}, AllowedTools: []string{"Read"}},
		{Name: "y", ReplacesNative: map[string][]string{"claude": {"bar"}}, AllowedTools: []string{"Edit"}},
	}
	b := []SkillSummary{a[1], a[0]}
	if !reflect.DeepEqual(mk(a), mk(b)) {
		t.Errorf("output should be order-independent")
	}
}

// ============================================================================
// AggregateCodex
// ============================================================================

func TestAggregateCodex_EmptyBaseAndSkills(t *testing.T) {
	got := AggregateCodex(CodexBase{}, nil)
	if len(got.DisabledTools) != 0 {
		t.Errorf("DisabledTools should be empty: %+v", got.DisabledTools)
	}
}

func TestAggregateCodex_BaseAndSkillsMerged(t *testing.T) {
	base := CodexBase{DisabledTools: []string{"web_search"}}
	skills := []SkillSummary{
		{Name: "alpha", ReplacesNative: map[string][]string{"codex": {"file_ops", "web_search"}}}, // dup
		{Name: "beta", ReplacesNative: map[string][]string{"claude": {"only-claude"}}},            // claude 不入 codex
	}
	got := AggregateCodex(base, skills)
	want := []string{"file_ops", "web_search"}
	if !reflect.DeepEqual(got.DisabledTools, want) {
		t.Errorf("DisabledTools:\nwant %v\ngot  %v", want, got.DisabledTools)
	}
}

func TestAggregateCodex_DisabledSkillIgnored(t *testing.T) {
	skills := []SkillSummary{
		{Name: "alpha", Disabled: true, ReplacesNative: map[string][]string{"codex": {"never-shown"}}},
		{Name: "beta", ReplacesNative: map[string][]string{"codex": {"shown"}}},
	}
	got := AggregateCodex(CodexBase{}, skills)
	want := []string{"shown"}
	if !reflect.DeepEqual(got.DisabledTools, want) {
		t.Errorf("DisabledTools:\nwant %v\ngot  %v", want, got.DisabledTools)
	}
}

func TestAggregateCodex_DedupAndTrim(t *testing.T) {
	base := CodexBase{DisabledTools: []string{" web_search ", "web_search", ""}}
	got := AggregateCodex(base, nil)
	want := []string{"web_search"}
	if !reflect.DeepEqual(got.DisabledTools, want) {
		t.Errorf("DisabledTools:\nwant %v\ngot  %v", want, got.DisabledTools)
	}
}
