package skillforge

import (
	"strings"
	"testing"
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
	if len(got) > 100 {
		t.Errorf("filename too long: %d", len(got))
	}
}
