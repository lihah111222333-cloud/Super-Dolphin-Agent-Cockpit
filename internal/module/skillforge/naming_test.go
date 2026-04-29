package skillforge

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSectionFilename(t *testing.T) {
	cases := []struct {
		index int
		title string
		want  string
	}{
		{1, "红绿重构循环", "01-红绿重构循环.md"},
		{12, "反模式：这太简单", "12-反模式-这太简单.md"},
		{1, "Path/with/slashes", "01-Path-with-slashes.md"},
		{1, "  trim  ", "01-trim.md"},
		{1, `Quotes "and" stuff`, "01-Quotes -and- stuff.md"},
	}
	for _, tc := range cases {
		got := SectionFilename(tc.index, tc.title)
		if got != tc.want {
			t.Errorf("SectionFilename(%d, %q) = %q, want %q", tc.index, tc.title, got, tc.want)
		}
	}
}

func TestSectionFilename_TruncatesVeryLong(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := SectionFilename(1, long)
	// 内容部分应被截到 ≤ 80 runes
	prefix := "01-"
	suffix := ".md"
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("unexpected envelope: %q", got)
	}
	content := strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix)
	if c := utf8.RuneCountInString(content); c > 80 {
		t.Errorf("content RuneCount = %d, want <= 80", c)
	}
}

func TestSectionFilename_EdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		index int
		title string
		want  string
	}{
		{"index zero", 0, "x", "00-x.md"},
		{"index three digits", 100, "x", "100-x.md"},
		{"empty title", 1, "", "01-untitled.md"},
		{"whitespace only title", 1, "   ", "01-untitled.md"},
		{"all illegal chars", 1, "///", "01-untitled.md"},
		{"redundant dashes collapsed", 1, "a//b", "01-a-b.md"},
		{"trailing illegal stripped", 1, "abc?", "01-abc.md"},
		{"leading illegal stripped", 1, "?abc", "01-abc.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SectionFilename(tc.index, tc.title)
			if got != tc.want {
				t.Errorf("SectionFilename(%d, %q) = %q, want %q", tc.index, tc.title, got, tc.want)
			}
		})
	}
}
