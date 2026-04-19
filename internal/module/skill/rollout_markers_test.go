package skill

import (
	"strings"
	"testing"
)

// TestTrimInjectedSkillBlocks_Legacy 覆盖 P20 §3.4 "legacy 格式" 场景：
// [skill:<name>] + "摘要:" + "使用方式: " 标记 AND 命中 → 剥离
func TestTrimInjectedSkillBlocks_Legacy(t *testing.T) {
	input := strings.Join([]string{
		"hello world",
		"[skill:go-testing]",
		"摘要: run go test",
		"使用方式: call with arg",
		"more body",
	}, "\n")
	want := "hello world"
	got := TrimInjectedSkillBlocks(input)
	if got != want {
		t.Fatalf("legacy trim: got %q want %q", got, want)
	}
}

// TestTrimInjectedSkillBlocks_LegacyHeaderWithMarkerOnSameLine 还原 claudecli
// 原实现宽容性：header 与 marker 可出现在同一行。
func TestTrimInjectedSkillBlocks_LegacyHeaderWithMarkerOnSameLine(t *testing.T) {
	input := "hello\n[skill: test] 摘要: sample\n使用方式: sample\nmore"
	want := "hello"
	got := TrimInjectedSkillBlocks(input)
	if got != want {
		t.Fatalf("legacy single-line trim: got %q want %q", got, want)
	}
}

// TestTrimInjectedSkillBlocks_NewFormatFull 覆盖 P20 §3.4 "full@v1" 新格式：
// 严格正则匹配的 header 直接触发剥离（无需 footer 或 AND 标记）。
func TestTrimInjectedSkillBlocks_NewFormatFull(t *testing.T) {
	input := strings.Join([]string{
		"user question here",
		"[skill:go-testing::full@v1]",
		"FULL SKILL.md body text",
		"more body",
		"[/skill:go-testing::full@v1]",
	}, "\n")
	want := "user question here"
	got := TrimInjectedSkillBlocks(input)
	if got != want {
		t.Fatalf("new-full trim: got %q want %q", got, want)
	}
}

// TestTrimInjectedSkillBlocks_NewFormatSummary 覆盖 Phase 4 写端会产出的 summary
// 格式：只注入摘要 + skill_expand 指针。本 case 特别关键——legacy 无法识别此类
// 块（没有 "使用方式:"），这是 P20 §3.4 指出的漏洞修复依据。
func TestTrimInjectedSkillBlocks_NewFormatSummary(t *testing.T) {
	input := strings.Join([]string{
		"prompt text",
		"[skill:rpc-tracing::summary@v1]",
		"Trace JSON-RPC flow across bus/router.",
		`→ Call skill_expand("rpc-tracing") for full body`,
		"[/skill:rpc-tracing::summary@v1]",
	}, "\n")
	want := "prompt text"
	got := TrimInjectedSkillBlocks(input)
	if got != want {
		t.Fatalf("new-summary trim: got %q want %q", got, want)
	}
}

// TestTrimInjectedSkillBlocks_NewFormatExpanded 覆盖 Phase 6 skill_expand 二次
// 注入的 expanded 块：body 由工具返回后重新贴到 turn input。
func TestTrimInjectedSkillBlocks_NewFormatExpanded(t *testing.T) {
	input := strings.Join([]string{
		"question",
		"[skill:lint-go::expanded@v1]",
		"[expanded by skill_expand]",
		"body contents ...",
		"[/skill:lint-go::expanded@v1]",
	}, "\n")
	want := "question"
	got := TrimInjectedSkillBlocks(input)
	if got != want {
		t.Fatalf("new-expanded trim: got %q want %q", got, want)
	}
}

// TestTrimInjectedSkillBlocks_MixedLegacyAndNew legacy 与 new 同时存在时：首个
// 命中即剥离到文末，后续块一并消失（行为与旧实现一致的"剪到文末"语义）。
func TestTrimInjectedSkillBlocks_MixedLegacyAndNew(t *testing.T) {
	// 场景：legacy 在前（会先命中）
	inputLegacyFirst := strings.Join([]string{
		"user msg",
		"[skill:old]",
		"摘要: a",
		"使用方式: b",
		"[skill:new-one::summary@v1]",
		"summary text",
	}, "\n")
	if got := TrimInjectedSkillBlocks(inputLegacyFirst); got != "user msg" {
		t.Fatalf("legacy-first mixed: got %q", got)
	}
	// 场景：new-format 在前
	inputNewFirst := strings.Join([]string{
		"user msg 2",
		"[skill:new-one::full@v1]",
		"body",
		"[/skill:new-one::full@v1]",
		"[skill:old]",
		"摘要: x",
		"使用方式: y",
	}, "\n")
	if got := TrimInjectedSkillBlocks(inputNewFirst); got != "user msg 2" {
		t.Fatalf("new-first mixed: got %q", got)
	}
}

// TestTrimInjectedSkillBlocks_NoMatch 用户正常文本不应被误剥。
func TestTrimInjectedSkillBlocks_NoMatch(t *testing.T) {
	// 无 skill 块
	plain := "just a normal user message.\nno special markers."
	if got := TrimInjectedSkillBlocks(plain); got != plain {
		t.Fatalf("plain text altered: got %q", got)
	}
	// 有 [skill:xxx] 但没 AND 命中 markers → legacy 识别不算数
	partial := "discussing the [skill: feature] in general\n(no other markers)"
	if got := TrimInjectedSkillBlocks(partial); got != partial {
		t.Fatalf("partial legacy altered: got %q", got)
	}
}

// TestParseSkillBlockHeader_NewFormatCaptures 新格式精确捕获 name/mode/version。
func TestParseSkillBlockHeader_NewFormatCaptures(t *testing.T) {
	cases := []struct {
		line        string
		wantName    string
		wantMode    string
		wantVersion int
	}{
		{"[skill:go-testing::full@v1]", "go-testing", "full", 1},
		{"[skill:a::summary@v2]", "a", "summary", 2},
		{"[skill:lint-go::expanded@v42]", "lint-go", "expanded", 42},
	}
	for _, c := range cases {
		h := ParseSkillBlockHeader(c.line)
		if h.Format != SkillBlockFormatNew {
			t.Fatalf("%q Format = %v want New", c.line, h.Format)
		}
		if h.Name != c.wantName || h.Mode != c.wantMode || h.Version != c.wantVersion {
			t.Fatalf("%q parse = %+v want name=%q mode=%q ver=%d", c.line, h, c.wantName, c.wantMode, c.wantVersion)
		}
	}
}

// TestParseSkillBlockHeader_LegacyFormat legacy 只关心"是否识别"，不提取内容。
func TestParseSkillBlockHeader_LegacyFormat(t *testing.T) {
	legacy := []string{
		"[skill:foo]",
		"[skill:foo] 摘要: text",
		"[skill: any thing here]",
	}
	for _, line := range legacy {
		h := ParseSkillBlockHeader(line)
		if h.Format != SkillBlockFormatLegacy {
			t.Fatalf("%q Format = %v want Legacy", line, h.Format)
		}
	}
}

// TestParseSkillBlockHeader_None 非 skill 行返回 None。
func TestParseSkillBlockHeader_None(t *testing.T) {
	cases := []string{
		"",
		"plain text",
		"[tool:foo]",
		"[skill:foo::invalid]", // 缺 @vN，不是严格 new-format，也不是 legacy（含 "::"）
		"[skill:]",             // 空 name，legacy regex 要求 [^]:]+ 至少一个
	}
	for _, line := range cases {
		h := ParseSkillBlockHeader(line)
		if h.Format != SkillBlockFormatNone {
			t.Fatalf("%q should be None, got %+v", line, h)
		}
	}
}

// TestParseSkillBlockFooter 新格式尾标解析。
func TestParseSkillBlockFooter(t *testing.T) {
	h, ok := ParseSkillBlockFooter("[/skill:go-testing::full@v1]")
	if !ok {
		t.Fatalf("expected footer match")
	}
	if h.Name != "go-testing" || h.Mode != "full" || h.Version != 1 {
		t.Fatalf("footer parse = %+v", h)
	}
	// 非尾行
	if _, ok := ParseSkillBlockFooter("[skill:go-testing::full@v1]"); ok {
		t.Fatalf("header should not match footer pattern")
	}
	if _, ok := ParseSkillBlockFooter("legacy text"); ok {
		t.Fatalf("plain text should not match footer")
	}
}

// TestTrimInjectedSkillBlocks_NewFormatWithoutFooter 新格式 header 无 footer 时
// 仍然剥离（P20 §3.4 决策：header 严格正则即触发，不依赖 footer）。
func TestTrimInjectedSkillBlocks_NewFormatWithoutFooter(t *testing.T) {
	input := "question\n[skill:foo::full@v1]\nbody without closing sentinel"
	want := "question"
	if got := TrimInjectedSkillBlocks(input); got != want {
		t.Fatalf("new-format no-footer: got %q want %q", got, want)
	}
}
