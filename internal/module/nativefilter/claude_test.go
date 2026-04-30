package nativefilter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

func parseDeny(t *testing.T, raw []byte) []string {
	t.Helper()
	var got struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, raw)
	}
	return got.Permissions.Deny
}

func TestBuildClaudeSettings_MergesDenyList(t *testing.T) {
	base := Config{
		Claude: ClaudeConfig{
			DisabledSkills: []string{"native-extra"},
			DisabledTools:  []string{"Read"},
		},
	}
	extra := []string{"simplify", "init"}
	out, err := BuildClaudeSettings(base, extra, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := parseDeny(t, out)

	// 顺序：disabled_tools 先 (Read), 然后 disabled_skills (Skill:native-extra),
	// 然后 extra (Skill:simplify, Skill:init)
	want := []string{"Read", "Skill:native-extra", "Skill:simplify", "Skill:init"}
	if len(got) != len(want) {
		t.Fatalf("len(deny) = %d, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("deny[%d] = %q, want %q (full: %v)", i, got[i], w, got)
		}
	}
}

func TestBuildClaudeSettings_EmptyAllRendersEmptyArray(t *testing.T) {
	out, err := BuildClaudeSettings(Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := parseDeny(t, out)
	if len(got) != 0 {
		t.Errorf("empty config should yield empty deny, got %v", got)
	}
	// 确认是 [] 不是 null（JSON 字面）
	if !strings.Contains(string(out), `"deny": []`) {
		t.Errorf("expected literal `\"deny\": []`, got: %s", out)
	}
}

func TestBuildClaudeSettings_DeduplicatesAcrossLists(t *testing.T) {
	// base.disabled_skills 含 "x"；extra 也含 "x"；输出只保留一份 Skill:x
	base := Config{Claude: ClaudeConfig{DisabledSkills: []string{"x"}}}
	out, err := BuildClaudeSettings(base, []string{"x", "y"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := parseDeny(t, out)
	count := 0
	for _, d := range got {
		if d == "Skill:x" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Skill:x should appear once after dedup, count=%d full=%v", count, got)
	}
	if len(got) != 2 {
		t.Errorf("len(deny) = %d, want 2 (Skill:x + Skill:y): %v", len(got), got)
	}
}

func TestBuildClaudeSettings_UsesColonSyntax(t *testing.T) {
	// 实测确认正确语法是 Skill:name 冒号，不是 Skill(name) 圆括号
	out, _ := BuildClaudeSettings(Config{Claude: ClaudeConfig{DisabledSkills: []string{"foo"}}}, nil, nil)
	got := parseDeny(t, out)
	if len(got) != 1 || got[0] != "Skill:foo" {
		t.Errorf("expected [\"Skill:foo\"] (colon syntax), got %v", got)
	}
	// 反向断言：不要出现圆括号格式
	if strings.Contains(string(out), "Skill(foo)") {
		t.Errorf("must NOT use parenthesis syntax, but found in: %s", out)
	}
}

func TestBuildClaudeSettings_EmptyStringsFiltered(t *testing.T) {
	base := Config{Claude: ClaudeConfig{DisabledSkills: []string{""}, DisabledTools: []string{""}}}
	out, _ := BuildClaudeSettings(base, []string{""}, nil)
	got := parseDeny(t, out)
	if len(got) != 0 {
		t.Errorf("empty strings should be filtered, got %v", got)
	}
}

func TestBuildClaudeSettings_RendersAllowlistWhenNonEmpty(t *testing.T) {
	base := Config{Claude: ClaudeConfig{AllowedTools: []string{"Read", "Edit"}}}
	allowExtra := []string{"Bash", "Read"} // Read 重复
	out, err := BuildClaudeSettings(base, nil, allowExtra)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"Read", "Edit", "Bash"}
	if len(got.Permissions.Allow) != len(want) {
		t.Fatalf("allow len = %d, want %d: %v", len(got.Permissions.Allow), len(want), got.Permissions.Allow)
	}
	for i, w := range want {
		if got.Permissions.Allow[i] != w {
			t.Errorf("allow[%d] = %q, want %q", i, got.Permissions.Allow[i], w)
		}
	}
}

func TestBuildClaudeSettings_OmitsAllowFieldWhenEmpty(t *testing.T) {
	out, err := BuildClaudeSettings(Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"allow"`) {
		t.Errorf("empty allow should not output the field, got: %s", out)
	}
}

func TestAggregateAllowedTools_DedupsAndSorts(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{Meta: &skilllibrary.SkillMeta{Name: "a", AllowedTools: []string{"Read", "Bash"}}},
		{Meta: &skilllibrary.SkillMeta{Name: "b", AllowedTools: []string{"Bash", "Edit"}}},
		{Meta: &skilllibrary.SkillMeta{Name: "c", Disabled: true, AllowedTools: []string{"Skip"}}},
	}
	got := AggregateAllowedTools(entries)
	want := []string{"Bash", "Edit", "Read"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
}
