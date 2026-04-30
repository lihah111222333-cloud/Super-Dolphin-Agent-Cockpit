package nativefilter

import (
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

func TestAggregateReplacesNative_DeduplicatesAcrossSkills(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{Meta: &skilllibrary.SkillMeta{Name: "a", ReplacesNative: map[string][]string{"claude": {"simplify", "init"}}}},
		{Meta: &skilllibrary.SkillMeta{Name: "b", ReplacesNative: map[string][]string{"claude": {"init", "review"}}}},
	}
	got := AggregateReplacesNative(entries, "claude")
	want := []string{"init", "review", "simplify"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAggregateReplacesNative_SkipsDisabledSkill(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{Meta: &skilllibrary.SkillMeta{Name: "active", ReplacesNative: map[string][]string{"claude": {"keep"}}}},
		{Meta: &skilllibrary.SkillMeta{Name: "dead", Disabled: true, ReplacesNative: map[string][]string{"claude": {"should-skip"}}}},
	}
	got := AggregateReplacesNative(entries, "claude")
	want := []string{"keep"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (disabled skill must be skipped)", got, want)
	}
}

func TestAggregateReplacesNative_PerCliKey(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{Meta: &skilllibrary.SkillMeta{Name: "x", ReplacesNative: map[string][]string{
			"claude": {"claude-only"},
			"codex":  {"codex-only"},
		}}},
	}
	gotClaude := AggregateReplacesNative(entries, "claude")
	gotCodex := AggregateReplacesNative(entries, "codex")
	if !reflect.DeepEqual(gotClaude, []string{"claude-only"}) {
		t.Errorf("claude: got %v", gotClaude)
	}
	if !reflect.DeepEqual(gotCodex, []string{"codex-only"}) {
		t.Errorf("codex: got %v", gotCodex)
	}
}

func TestAggregateReplacesNative_EmptyEntriesReturnEmpty(t *testing.T) {
	got := AggregateReplacesNative(nil, "claude")
	if len(got) != 0 {
		t.Errorf("empty input should yield empty slice, got %v", got)
	}
}

func TestAggregateReplacesNative_NilMetaSkipped(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{Meta: nil}, // 防御性：正常路径不出现
		{Meta: &skilllibrary.SkillMeta{Name: "x", ReplacesNative: map[string][]string{"claude": {"y"}}}},
	}
	got := AggregateReplacesNative(entries, "claude")
	if !reflect.DeepEqual(got, []string{"y"}) {
		t.Errorf("nil meta should be skipped without panic, got %v", got)
	}
}

func TestAggregateReplacesNative_EmptyStringFiltered(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{Meta: &skilllibrary.SkillMeta{Name: "x", ReplacesNative: map[string][]string{"claude": {"", "real"}}}},
	}
	got := AggregateReplacesNative(entries, "claude")
	if !reflect.DeepEqual(got, []string{"real"}) {
		t.Errorf("empty string should be filtered, got %v", got)
	}
}

func TestAggregateReplacesNative_UnknownCliKey(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{Meta: &skilllibrary.SkillMeta{Name: "x", ReplacesNative: map[string][]string{"claude": {"a"}}}},
	}
	got := AggregateReplacesNative(entries, "unknown-cli")
	if len(got) != 0 {
		t.Errorf("unknown cli key should yield empty, got %v", got)
	}
}
