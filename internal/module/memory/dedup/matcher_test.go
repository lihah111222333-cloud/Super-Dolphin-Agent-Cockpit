package dedup

import (
	"strings"
	"testing"
)

// ---- NormalizeName ----

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			// Hyphens are punctuation: stripped without inserting a space.
			// "Reply-in-Chinese!!!" → lower → "reply-in-chinese!!!" → strip punct → "replyinchinese"
			name:  "basic_ascii_punctuation",
			input: "Reply-in-Chinese!!!",
			want:  "replyinchinese",
		},
		{
			// CJK chars are not punctuation or symbols → pass through unchanged (lowercased has no effect)
			name:  "unicode_passthrough",
			input: "用中文回复",
			want:  "用中文回复",
		},
		{
			name:  "collapse_whitespace",
			input: "  hello   world  ",
			want:  "hello world",
		},
		{
			// Hyphens are stripped; result has no spaces from them
			name:  "mixed_case",
			input: "Reply-In-Chinese",
			want:  "replyinchinese",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeName_LeadingTrailingSpaces(t *testing.T) {
	got := NormalizeName("  hello  ")
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Errorf("NormalizeName should trim spaces, got %q", got)
	}
	if got != "hello" {
		t.Errorf("expected %q, got %q", "hello", got)
	}
}

// ---- FindDuplicate helpers ----

func makeEntry(name, typ string, keys []string, content string) EntrySnapshot {
	return EntrySnapshot{
		Name:       name,
		Type:       typ,
		SearchKeys: keys,
		Content:    content,
	}
}

// ---- FindDuplicate tests ----

func TestFindDuplicate_NameExactMatch(t *testing.T) {
	candidate := makeEntry("reply-in-chinese", "feedback", nil, "some content")
	existing := []EntrySnapshot{
		makeEntry("other-entry", "feedback", nil, "other content"),
		makeEntry("reply-in-chinese", "feedback", nil, "existing content about replying in chinese"),
	}

	result := FindDuplicate(candidate, existing)

	if !result.Found {
		t.Fatal("expected Found=true for name exact match")
	}
	if result.Level != "name" {
		t.Errorf("expected Level=%q, got %q", "name", result.Level)
	}
	if result.Target.Name != "reply-in-chinese" {
		t.Errorf("expected Target.Name=%q, got %q", "reply-in-chinese", result.Target.Name)
	}
}

func TestFindDuplicate_CrossTypeNoMatch(t *testing.T) {
	// candidate is type "project", existing has same name but type "feedback"
	candidate := makeEntry("reply-in-chinese", "project", nil, "some content")
	existing := []EntrySnapshot{
		makeEntry("reply-in-chinese", "feedback", nil, "existing content"),
	}

	result := FindDuplicate(candidate, existing)

	if result.Found {
		t.Error("expected Found=false for cross-type name match")
	}
}

func TestFindDuplicate_SearchKeysMatch(t *testing.T) {
	// candidate keys: ["language","chinese"]
	// existing keys:  ["language","chinese","communication"]
	// Jaccard = 2/3 ≈ 0.667 >= 0.5
	candidate := makeEntry("new-entry", "feedback", []string{"language", "chinese"}, "content a")
	existing := []EntrySnapshot{
		makeEntry("old-entry", "feedback", []string{"language", "chinese", "communication"}, "content b"),
	}

	result := FindDuplicate(candidate, existing)

	if !result.Found {
		t.Fatal("expected Found=true for search_keys Jaccard match")
	}
	if result.Level != "search_keys" {
		t.Errorf("expected Level=%q, got %q", "search_keys", result.Level)
	}
	expected := 2.0 / 3.0
	diff := result.Score - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.01 {
		t.Errorf("expected Score≈%.4f, got %.4f", expected, result.Score)
	}
}

func TestFindDuplicate_ContentContainment(t *testing.T) {
	// Short candidate is contained within the longer existing entry
	shortContent := "用中文回复用户消息"
	longContent := "用中文回复用户消息，包括详细背景说明，以及语言风格要求"

	candidate := makeEntry("short-entry", "feedback", nil, shortContent)
	existing := []EntrySnapshot{
		makeEntry("long-entry", "feedback", nil, longContent),
	}

	result := FindDuplicate(candidate, existing)

	if !result.Found {
		t.Fatal("expected Found=true for content containment")
	}
	if result.Level != "content" {
		t.Errorf("expected Level=%q, got %q", "content", result.Level)
	}
	if result.Score < 0.7 {
		t.Errorf("expected Score>=0.7, got %.4f", result.Score)
	}
}

func TestFindDuplicate_DifferentContent_NoMatch(t *testing.T) {
	candidate := makeEntry("entry-a", "feedback", nil, "freeze 限额不可改变")
	existing := []EntrySnapshot{
		makeEntry("entry-b", "feedback", nil, "日报格式固定四段"),
	}

	result := FindDuplicate(candidate, existing)

	if result.Found {
		t.Errorf("expected Found=false for unrelated content, got Level=%q Score=%.4f", result.Level, result.Score)
	}
}

func TestFindDuplicate_EmptyExisting(t *testing.T) {
	candidate := makeEntry("any-entry", "feedback", nil, "some content")

	result := FindDuplicate(candidate, []EntrySnapshot{})

	if result.Found {
		t.Error("expected Found=false for empty existing list")
	}
}

func TestFindDuplicate_NameNormalizationMatch(t *testing.T) {
	// "reply in chinese" and "reply in chinese" are the same after NormalizeName.
	// Use names that normalize identically: spaces are preserved, only case differs.
	candidate := makeEntry("Reply In Chinese", "feedback", nil, "content")
	existing := []EntrySnapshot{
		makeEntry("reply in chinese", "feedback", nil, "existing content"),
	}

	result := FindDuplicate(candidate, existing)

	if !result.Found {
		t.Fatal("expected Found=true after name normalization (case-insensitive)")
	}
	if result.Level != "name" {
		t.Errorf("expected Level=%q, got %q", "name", result.Level)
	}
}
