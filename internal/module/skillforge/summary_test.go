package skillforge

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtractSummary_FirstSentence(t *testing.T) {
	body := "先写失败测试，再实现，最后重构。具体步骤如下：\n- 红：写测试"
	got := ExtractSummary(body, 80)
	want := "先写失败测试，再实现，最后重构。"
	if got != want {
		t.Errorf("ExtractSummary = %q, want %q", got, want)
	}
}

func TestExtractSummary_TruncatesLongFirstSentence(t *testing.T) {
	body := strings.Repeat("a", 200) + "."
	got := ExtractSummary(body, 80)
	if utf8.RuneCountInString(got) > 80 {
		t.Errorf("RuneCount(got) = %d, want <= 80", utf8.RuneCountInString(got))
	}
}

func TestExtractSummary_EmptyBody(t *testing.T) {
	if got := ExtractSummary("", 80); got != "" {
		t.Errorf("ExtractSummary(empty) = %q, want \"\"", got)
	}
}

func TestExtractSummary_SkipsLeadingHeading(t *testing.T) {
	body := "# Inner heading should be skipped\n\n这才是正文第一句。"
	got := ExtractSummary(body, 80)
	want := "这才是正文第一句。"
	if got != want {
		t.Errorf("ExtractSummary = %q, want %q", got, want)
	}
}

func TestExtractSummary_MaxRunesZeroOrNegative(t *testing.T) {
	cases := []int{-1, 0}
	for _, n := range cases {
		if got := ExtractSummary("anything here.", n); got != "" {
			t.Errorf("ExtractSummary(_, %d) = %q, want empty", n, got)
		}
	}
}

func TestExtractSummary_MaxRunesVerySmall(t *testing.T) {
	// maxRunes=1 should not panic and should return at most 1 rune.
	got := ExtractSummary("许多内容超过限制。", 1)
	if utf8.RuneCountInString(got) > 1 {
		t.Errorf("RuneCount(got) = %d, want <= 1", utf8.RuneCountInString(got))
	}
}

func TestExtractSummary_HeadingOnlyBody(t *testing.T) {
	body := "# H\n## H2\n### H3\n"
	if got := ExtractSummary(body, 80); got != "" {
		t.Errorf("ExtractSummary(headings only) = %q, want empty", got)
	}
}

func TestExtractSummary_NoPunctuationFallback(t *testing.T) {
	got := ExtractSummary("没有句末标点的中文段落", 80)
	if !strings.HasSuffix(got, "。") {
		t.Errorf("ExtractSummary fallback should append 。, got %q", got)
	}
}

func TestExtractSummary_MixedScriptStopChars(t *testing.T) {
	// "Hello. 世界。" has both English . at index 5 and Chinese 。 later.
	// First stop wins -> "Hello."
	got := ExtractSummary("Hello. 世界。", 80)
	want := "Hello."
	if got != want {
		t.Errorf("ExtractSummary mixed-script = %q, want %q", got, want)
	}
}

func TestExtractSummary_OneLongLineMultipleSentences(t *testing.T) {
	got := ExtractSummary("abc. def. ghi", 80)
	if got != "abc." {
		t.Errorf("ExtractSummary one-line multi-sentence = %q, want %q", got, "abc.")
	}
}
