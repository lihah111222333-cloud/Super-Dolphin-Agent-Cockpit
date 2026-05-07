package dedup

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// ---- FindMostSimilarPair ----

func TestFindMostSimilarPair_ReturnsCorrectPair(t *testing.T) {
	// Entries 0 and 1 share lots of CJK content; entry 2 is unrelated.
	entries := []EntrySnapshot{
		{Content: "用中文回复用户消息，语言风格要求自然流畅"},
		{Content: "用中文回复用户消息，语言风格要求友好"},
		{Content: "日报格式固定四段落结构与时间线"},
	}

	i, j, score, found := FindMostSimilarPair(entries)

	if !found {
		t.Fatal("expected found=true")
	}
	// The most similar pair should be (0,1)
	if (i != 0 || j != 1) && (i != 1 || j != 0) {
		t.Errorf("expected pair (0,1), got (%d,%d)", i, j)
	}
	if score < MinMergePairContainment {
		t.Errorf("expected score >= %.2f, got %.4f", MinMergePairContainment, score)
	}
}

func TestFindMostSimilarPair_AllPairsBelowThreshold(t *testing.T) {
	// Three completely unrelated entries (all ASCII, no shared bigrams)
	entries := []EntrySnapshot{
		{Content: "freeze limit policy cannot changed"},
		{Content: "dailyreport format fixed section"},
		{Content: "commit message english only"},
	}

	_, _, _, found := FindMostSimilarPair(entries)

	if found {
		t.Error("expected found=false when all pairs below threshold")
	}
}

func TestFindSimilarPairsOnlyComparesSameType(t *testing.T) {
	pairs := FindSimilarPairs([]EntrySnapshot{
		{Name: "Feedback", Type: "feedback", Content: "same reusable memory content"},
		{Name: "Project", Type: "project", Content: "same reusable memory content"},
	})
	if len(pairs) != 0 {
		t.Fatalf("FindSimilarPairs() returned cross-type pairs: %#v", pairs)
	}
}

func TestFindMostSimilarPair_ZeroEntries(t *testing.T) {
	_, _, _, found := FindMostSimilarPair([]EntrySnapshot{})
	if found {
		t.Error("expected found=false for 0 entries")
	}
}

func TestFindMostSimilarPair_OneEntry(t *testing.T) {
	entries := []EntrySnapshot{
		{Content: "some content"},
	}
	_, _, _, found := FindMostSimilarPair(entries)
	if found {
		t.Error("expected found=false for 1 entry")
	}
}

// ---- TruncateOldestParagraphs ----

func TestTruncateOldestParagraphs_NoTruncationNeeded(t *testing.T) {
	content := strings.Repeat("x", 1000)
	got := TruncateOldestParagraphs(content, MaxEntryContentRunes)
	if got != content {
		t.Error("expected content unchanged when within limit")
	}
}

func TestTruncateOldestParagraphs_DropsEarliestParagraphs(t *testing.T) {
	// 4 paragraphs, each ~600 runes → total ~2448 runes > 1500
	para := strings.Repeat("a", 600)
	content := para + "\n\n" + para + "\n\n" + para + "\n\n" + para

	got := TruncateOldestParagraphs(content, MaxEntryContentRunes)

	if utf8.RuneCountInString(got) > MaxEntryContentRunes {
		t.Errorf("result still exceeds limit: %d runes", utf8.RuneCountInString(got))
	}
	// The last paragraph must be preserved
	paras := strings.Split(got, "\n\n")
	lastPara := paras[len(paras)-1]
	if lastPara != para {
		t.Errorf("last paragraph should always be kept; got len=%d, want len=%d", len(lastPara), len(para))
	}
}

func TestTruncateOldestParagraphs_SingleLargeParagraph(t *testing.T) {
	// A single paragraph larger than the limit must be preserved as-is
	content := strings.Repeat("x", 2000)
	got := TruncateOldestParagraphs(content, MaxEntryContentRunes)
	if got != content {
		t.Error("single paragraph exceeding limit must be preserved unchanged")
	}
}

func TestTruncateOldestParagraphs_EmptyContent(t *testing.T) {
	got := TruncateOldestParagraphs("", MaxEntryContentRunes)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestFindSimilarPairsStableTieBreaker(t *testing.T) {
	entries := []EntrySnapshot{
		{Name: "b", Type: "feedback", Content: "完全相同的内容用于稳定排序", Scope: "team", Path: "team/b.md"},
		{Name: "a", Type: "feedback", Content: "完全相同的内容用于稳定排序", Scope: "private", Path: "private/a.md"},
		{Name: "c", Type: "feedback", Content: "完全相同的内容用于稳定排序", Scope: "private", Path: "private/c.md"},
	}
	pairs := FindSimilarPairs(entries)
	if len(pairs) < 2 {
		t.Fatalf("FindSimilarPairs() len = %d, want at least 2", len(pairs))
	}
	for i := 1; i < len(pairs); i++ {
		prev := pairs[i-1]
		cur := pairs[i]
		if prev.Score == cur.Score && pairSortKey(prev) > pairSortKey(cur) {
			t.Fatalf("pairs not stably sorted at %d: %#v before %#v", i, prev, cur)
		}
	}
}
