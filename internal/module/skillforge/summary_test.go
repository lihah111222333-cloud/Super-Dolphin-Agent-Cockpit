package skillforge

import (
	"strings"
	"testing"
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
	if len(got) > 80 {
		t.Errorf("len(got) = %d, want <= 80", len(got))
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
