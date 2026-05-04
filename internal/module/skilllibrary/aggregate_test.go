package skilllibrary

import (
	"testing"
)

func TestAggregateAllReplacements_WildcardKey(t *testing.T) {
	entries := []SkillEntry{
		{Meta: &SkillMeta{Name: "lsp", ReplacesNative: map[string][]string{
			"*": {"Read", "Write", "Bash"},
		}}},
	}
	got := AggregateAllReplacements(entries)
	want := []string{"Bash", "Read", "Write"}
	if len(got) != len(want) {
		t.Fatalf("AggregateAllReplacements = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AggregateAllReplacements[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAggregateAllReplacements_MixedKeys(t *testing.T) {
	entries := []SkillEntry{
		{Meta: &SkillMeta{Name: "a", ReplacesNative: map[string][]string{
			"claude": {"Read"},
		}}},
		{Meta: &SkillMeta{Name: "b", ReplacesNative: map[string][]string{
			"*": {"Write", "Read"},
		}}},
	}
	got := AggregateAllReplacements(entries)
	want := []string{"Read", "Write"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateAllReplacements_SkipsDisabledAndNilMeta(t *testing.T) {
	entries := []SkillEntry{
		{Meta: nil},
		{Meta: &SkillMeta{Name: "off", Disabled: true, ReplacesNative: map[string][]string{
			"*": {"Bash"},
		}}},
		{Meta: &SkillMeta{Name: "on", ReplacesNative: map[string][]string{
			"*": {"Read"},
		}}},
	}
	got := AggregateAllReplacements(entries)
	if len(got) != 1 || got[0] != "Read" {
		t.Fatalf("got %v, want [Read]", got)
	}
}

func TestAggregateAllReplacements_Empty(t *testing.T) {
	got := AggregateAllReplacements(nil)
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestAggregateReplacementsForProvider(t *testing.T) {
	entries := []SkillEntry{
		{Meta: &SkillMeta{Name: "a", ReplacesNative: map[string][]string{
			"claude": {"Read"},
			"codex":  {"shell"},
			"*":      {"WebFetch"},
		}}},
	}
	got := AggregateReplacementsForProvider(entries, "codex")
	want := []string{"WebFetch", "shell"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
