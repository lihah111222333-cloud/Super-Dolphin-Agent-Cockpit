package dedup

import (
	"strings"
	"testing"
)

// ---- helpers ----

func makeBigrams(tokens ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		m[tok] = struct{}{}
	}
	return m
}

// ---- Decide ----

func TestDecide_IdenticalContent(t *testing.T) {
	bg := makeBigrams("a", "b", "c", "d", "e")
	if got := Decide(bg, bg); got != Skip {
		t.Errorf("expected Skip for identical bigrams, got %v", got)
	}
}

func TestDecide_OldContainsNew90Percent(t *testing.T) {
	// old has 11 bigrams; newBg has 10, of which 9 are in old → 90% overlap → Skip
	oldBg := makeBigrams("a", "b", "c", "d", "e", "f", "g", "h", "i", "NOVEL", "extra")
	// 9 of 10 in old (XNOVEL not in old)
	newBg := makeBigrams("a", "b", "c", "d", "e", "f", "g", "h", "i", "XNOVEL")
	if got := Decide(oldBg, newBg); got != Skip {
		t.Errorf("expected Skip for 90%% overlap, got %v", got)
	}
}

func TestDecide_NovelLessThan15Percent(t *testing.T) {
	// 20 new bigrams, only 2 novel (10%) → novel < 15% → Skip
	oldBg := makeBigrams("a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t")
	// newBg has 18 of old + 2 novel
	newBg := makeBigrams("a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "NOVEL1", "NOVEL2")
	// 2/20 = 10% < 15% → Skip
	if got := Decide(oldBg, newBg); got != Skip {
		t.Errorf("expected Skip for novel<15%%, got %v", got)
	}
}

func TestDecide_NovelAtLeast15Percent(t *testing.T) {
	// 10 new bigrams, 4 novel (40%) → Merge
	oldBg := makeBigrams("a", "b", "c", "d", "e", "f")
	newBg := makeBigrams("a", "b", "c", "d", "e", "f", "N1", "N2", "N3", "N4")
	// 4/10 = 40% → Merge
	if got := Decide(oldBg, newBg); got != Merge {
		t.Errorf("expected Merge for novel>=15%%, got %v", got)
	}
}

func TestDecide_BothEmpty(t *testing.T) {
	if got := Decide(makeBigrams(), makeBigrams()); got != Skip {
		t.Errorf("expected Skip for both empty, got %v", got)
	}
}

// ---- MergeRulePoints ----

func TestMergeRulePoints_NewLineAdded(t *testing.T) {
	// Use lines with sufficiently distinct content so bigram containment < 0.7.
	// "用中文回复用户消息" vs "冻结订单限额无法修改" → completely different bigrams.
	oldContent := "用中文回复用户消息\n日报格式固定四个段落\n代码提交信息必须英文"
	newContent := "用中文回复用户消息\n日报格式固定四个段落\n冻结订单限额无法修改"

	result := MergeRulePoints(oldContent, newContent)

	for _, rule := range []string{"用中文回复用户消息", "日报格式固定四个段落", "代码提交信息必须英文", "冻结订单限额无法修改"} {
		if !strings.Contains(result, rule) {
			t.Errorf("result missing %q; got:\n%s", rule, result)
		}
	}
}

func TestMergeRulePoints_AllDuplicate(t *testing.T) {
	oldContent := "用中文回复用户消息\n日报格式固定四个段落\n代码提交信息必须英文"
	newContent := "用中文回复用户消息\n日报格式固定四个段落\n代码提交信息必须英文"

	result := MergeRulePoints(oldContent, newContent)

	if result != oldContent {
		t.Errorf("expected result == old when all lines duplicate\ngot:  %q\nwant: %q", result, oldContent)
	}
}

// ---- MergeParagraphs ----

func TestMergeParagraphs_NewParagraphAdded(t *testing.T) {
	// Use paragraphs with very different content so containment < 0.5
	oldContent := "用中文回复用户消息语言风格自然流畅\n\n冻结订单限额政策无法更改"
	newContent := "用中文回复用户消息语言风格自然流畅\n\n日报格式固定四个段落章节标题"

	result := MergeParagraphs(oldContent, newContent)

	for _, para := range []string{"用中文回复用户消息语言风格自然流畅", "冻结订单限额政策无法更改", "日报格式固定四个段落章节标题"} {
		if !strings.Contains(result, para) {
			t.Errorf("result missing %q; got:\n%s", para, result)
		}
	}
}

func TestMergeParagraphs_ReplaceShorterParagraph(t *testing.T) {
	// old has a short version; new has a longer version of the same paragraph
	oldShort := "用中文回复用户"
	newLong := "用中文回复用户，包含完整语言风格说明以及详细要求背景"
	oldContent := oldShort + "\n\n其他独立段落关于冻结限额策略"
	newContent := newLong + "\n\n其他独立段落关于冻结限额策略"

	result := MergeParagraphs(oldContent, newContent)

	if !strings.Contains(result, newLong) {
		t.Errorf("expected longer paragraph to replace shorter one; got:\n%s", result)
	}
}

// ---- MergeContent routing ----

func TestMergeContent_FeedbackRoutesToRulePoints(t *testing.T) {
	// Use lines with distinct CJK bigrams so the novel line gets appended.
	oldContent := "用中文回复用户消息语言要求\n日报格式固定四个段落"
	newContent := "用中文回复用户消息语言要求\n冻结订单限额政策无法更改"

	result := MergeContent("feedback", oldContent, newContent)

	for _, rule := range []string{"用中文回复用户消息语言要求", "日报格式固定四个段落", "冻结订单限额政策无法更改"} {
		if !strings.Contains(result, rule) {
			t.Errorf("result missing %q via MergeRulePoints path; got:\n%s", rule, result)
		}
	}
}

func TestMergeContent_ProjectRoutesToParagraphs(t *testing.T) {
	// Use paragraphs with very different bigrams so novel paragraph gets appended.
	oldContent := "用中文回复用户消息语言风格自然\n\n冻结订单限额无法更改政策"
	newContent := "用中文回复用户消息语言风格自然\n\n日报格式固定四个段落章节"

	result := MergeContent("project", oldContent, newContent)

	for _, p := range []string{"用中文回复用户消息语言风格自然", "冻结订单限额无法更改政策", "日报格式固定四个段落章节"} {
		if !strings.Contains(result, p) {
			t.Errorf("result missing %q via MergeParagraphs path; got:\n%s", p, result)
		}
	}
}

// ---- MergeFrontmatter ----

func TestMergeFrontmatter_SearchKeysUnion(t *testing.T) {
	oldSnap := EntrySnapshot{Name: "e", Type: "feedback", SearchKeys: []string{"a", "b"}}
	newSnap := EntrySnapshot{Name: "e", Type: "feedback", SearchKeys: []string{"b", "c"}}

	result := MergeFrontmatter(oldSnap, newSnap)

	keySet := make(map[string]struct{})
	for _, k := range result.SearchKeys {
		keySet[k] = struct{}{}
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := keySet[k]; !ok {
			t.Errorf("result.SearchKeys missing %q; got %v", k, result.SearchKeys)
		}
	}
}

func TestMergeFrontmatter_DescriptionTakesLonger(t *testing.T) {
	oldSnap := EntrySnapshot{Name: "e", Description: "短"}
	newSnap := EntrySnapshot{Name: "e", Description: "这是一个更长的描述说明"}

	result := MergeFrontmatter(oldSnap, newSnap)

	if result.Description != newSnap.Description {
		t.Errorf("expected longer description %q, got %q", newSnap.Description, result.Description)
	}
}

func TestMergeFrontmatter_SourcePreservedWhenNotDream(t *testing.T) {
	oldSnap := EntrySnapshot{Name: "e", Source: ""}
	newSnap := EntrySnapshot{Name: "e", Source: "some-source"}

	result := MergeFrontmatter(oldSnap, newSnap)

	// old.Source is not "dream", so old.Source should be preserved
	if result.Source != "" {
		t.Errorf("expected Source=%q (old preserved), got %q", "", result.Source)
	}
}

func TestMergeFrontmatter_SourceDreamOverridden(t *testing.T) {
	oldSnap := EntrySnapshot{Name: "e", Source: "dream"}
	newSnap := EntrySnapshot{Name: "e", Source: ""}

	result := MergeFrontmatter(oldSnap, newSnap)

	// old.Source == "dream" → take new.Source
	if result.Source != "" {
		t.Errorf("expected Source=%q (new overrides dream), got %q", "", result.Source)
	}
}

func TestMergeFrontmatter_NamePreservesOld(t *testing.T) {
	oldSnap := EntrySnapshot{Name: "old-name"}
	newSnap := EntrySnapshot{Name: "new-name"}

	result := MergeFrontmatter(oldSnap, newSnap)

	if result.Name != "old-name" {
		t.Errorf("expected Name=%q, got %q", "old-name", result.Name)
	}
}
