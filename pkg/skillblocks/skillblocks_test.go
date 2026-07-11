package skillblocks

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

// TestTrimInjectedSkillBlocks_NewFormatSummary 覆盖写端 v1 summary：
// 外层是稳定 paired marker，内层仍保留 legacy "摘要:" / "使用方式:" 文本。
func TestTrimInjectedSkillBlocks_NewFormatSummary(t *testing.T) {
	input := strings.Join([]string{
		"prompt text",
		"[skill:rpc-tracing::summary@v1]",
		"摘要: Trace JSON-RPC flow across bus/router.",
		`使用方式: Call skill_expand_body("rpc-tracing") for full body`,
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
	// 有 [skill:feature] 但没 AND 命中 markers -> legacy 识别不算数
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
		"[tool:foo]",        // 不是 skill 前缀
		"[skill:foo",        // 没有闭合 ]
		"plain [skill:foo]", // 非行首
	}
	for _, line := range cases {
		h := ParseSkillBlockHeader(line)
		if h.Format != SkillBlockFormatNone {
			t.Fatalf("%q should be None, got %+v", line, h)
		}
	}
}

// TestParseSkillBlockHeader_EdgeCasesFallToLegacy 验证放宽后的 regex 将 new-format
// 不完整的 header 与空 name header 当作 legacy 处理，与旧实现一致。
// 这些本身不会触发剥离（还需 AND 命中 markers），但有助于匹配旧 rollout 内的
// 人工编辑或 bug 产生的残留标记。
func TestParseSkillBlockHeader_EdgeCasesFallToLegacy(t *testing.T) {
	cases := []string{
		"[skill:]",             // 空 name
		"[skill:foo:bar]",      // name 含单冒号
		"[skill:foo::invalid]", // new-format 不完整（缺 @vN）
	}
	for _, line := range cases {
		h := ParseSkillBlockHeader(line)
		if h.Format != SkillBlockFormatLegacy {
			t.Fatalf("%q should be Legacy after regex relaxation, got %+v", line, h)
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

// TestTrimInjectedSkillBlocks_LegacyEmptyName 确认空 name 的 legacy header
// 与旧实现行为一致：配合 AND 命中仍可被剥离。旧 codexapp/claudecli 只检查
// `strings.HasPrefix("[skill:")` + `Contains("]")`，[skill:] 合格。
func TestTrimInjectedSkillBlocks_LegacyEmptyName(t *testing.T) {
	input := strings.Join([]string{
		"user prompt",
		"[skill:]",
		"摘要: anything",
		"使用方式: whatever",
	}, "\n")
	if got := TrimInjectedSkillBlocks(input); got != "user prompt" {
		t.Fatalf("legacy empty-name: got %q want %q", got, "user prompt")
	}
}

// TestTrimInjectedSkillBlocks_LegacyNameWithColon 确认 name 内部含 `:` 的 legacy
// header（如 [skill:foo:bar]）与旧实现行为一致。放宽后的 regex `[^\]]*` 溢出
// 被限制为“不可含 `]`”，其他内容（含 `:`、空格）全部当 name 正文处理。
func TestTrimInjectedSkillBlocks_LegacyNameWithColon(t *testing.T) {
	input := strings.Join([]string{
		"user prompt",
		"[skill:foo:bar]",
		"摘要: text",
		"使用方式: text",
	}, "\n")
	if got := TrimInjectedSkillBlocks(input); got != "user prompt" {
		t.Fatalf("legacy name-with-colon: got %q want %q", got, "user prompt")
	}
}

// TestTrimInjectedSkillBlocks_LegacyOnlyOneMarker 防御 AND 退化为 OR：
// 仅命中 "摘要:" 但没有 "使用方式: " 时，legacy 判定必须不剥离。
// 未来有人误改 `len(matched) == len(legacySkillMarkers)` 为 `>= 1` 会被卡住。
func TestTrimInjectedSkillBlocks_LegacyOnlyOneMarker(t *testing.T) {
	input := strings.Join([]string{
		"normal user text",
		"[skill:foo]",
		"摘要: only summary, missing the second marker",
		"...",
	}, "\n")
	if got := TrimInjectedSkillBlocks(input); got != input {
		t.Fatalf("single-marker MUST NOT trim, got %q", got)
	}
}

// TestTrimInjectedSkillBlocks_LegacyLookaheadBoundary 防御 legacySkillLookahead
// 被改小：第二个 marker 移到第 10 行（超出 lookahead=8）时，AND 不命→不剥离。
func TestTrimInjectedSkillBlocks_LegacyLookaheadBoundary(t *testing.T) {
	lines := []string{
		"user msg",
		"[skill:foo]",
		"摘要: first marker",
		// 7 行填充（使用方式: 出现在超 lookahead=8 的位置）
		"filler 1", "filler 2", "filler 3", "filler 4",
		"filler 5", "filler 6", "filler 7", "filler 8",
		"使用方式: too late", // 在 header 后第 10 行，超 lookahead
	}
	input := strings.Join(lines, "\n")
	if got := TrimInjectedSkillBlocks(input); got != input {
		t.Fatalf("out-of-lookahead marker MUST NOT count, got %q", got)
	}
}

// ============================================================================
// P20.1 §3.4 加固：成对裁剪 / block 后正常文本保留 / footer 缺失兑底
// ============================================================================

// TestTrimInjectedSkillBlocks_P20_1_PreserveTrailingText P20.1 核心新能力：
// 新格式 block 裁剪后，block 后面的普通用户文本必须保留。旧实现会剪到 EOF
// 将后面的用户文本丢失——这正是 P20.1 §3.4 指出的安全隐患。
func TestTrimInjectedSkillBlocks_P20_1_PreserveTrailingText(t *testing.T) {
	input := strings.Join([]string{
		"user prompt before",
		"[skill:foo::summary@v1]",
		"summary text",
		"[/skill:foo::summary@v1]",
		"user continues writing after block",
		"additional question or detail",
	}, "\n")
	want := strings.Join([]string{
		"user prompt before",
		"user continues writing after block",
		"additional question or detail",
	}, "\n")
	if got := TrimInjectedSkillBlocks(input); got != want {
		t.Fatalf("trailing text MUST be preserved:\ngot  %q\nwant %q", got, want)
	}
}

// TestTrimInjectedSkillBlocks_P20_1_MultipleNewBlocks 同一 payload 内多个新格式
// block 应逐个裁剪，block 间的用户文本保留。
func TestTrimInjectedSkillBlocks_P20_1_MultipleNewBlocks(t *testing.T) {
	input := strings.Join([]string{
		"intro",
		"[skill:first::full@v1]",
		"body1",
		"[/skill:first::full@v1]",
		"middle text",
		"[skill:second::summary@v1]",
		"summary2",
		"[/skill:second::summary@v1]",
		"outro",
	}, "\n")
	want := strings.Join([]string{
		"intro",
		"middle text",
		"outro",
	}, "\n")
	if got := TrimInjectedSkillBlocks(input); got != want {
		t.Fatalf("multi-block pair trim:\ngot  %q\nwant %q", got, want)
	}
	res := TrimInjectedSkillBlocksWithDiag(input)
	if res.NewBlocksTrimmed != 2 {
		t.Fatalf("NewBlocksTrimmed = %d, want 2", res.NewBlocksTrimmed)
	}
	if res.FooterMissingCount != 0 {
		t.Fatalf("no footer missing expected, got %d", res.FooterMissingCount)
	}
}

// TestTrimInjectedSkillBlocks_P20_1_FooterMissingFallback P20.1 §3.4：header
// 存在但 footer 缺失 → 走损坏兑底（剪到 EOF）+ FooterMissingCount 递增。
func TestTrimInjectedSkillBlocks_P20_1_FooterMissingFallback(t *testing.T) {
	input := strings.Join([]string{
		"user msg",
		"[skill:foo::full@v1]",
		"body without closing footer...",
		"orphaned content",
	}, "\n")
	res := TrimInjectedSkillBlocksWithDiag(input)
	if res.Text != "user msg" {
		t.Fatalf("fallback text = %q, want %q", res.Text, "user msg")
	}
	if res.FooterMissingCount != 1 {
		t.Fatalf("FooterMissingCount = %d, want 1", res.FooterMissingCount)
	}
	if res.NewBlocksTrimmed != 0 {
		t.Fatalf("NewBlocksTrimmed should be 0 on fallback, got %d", res.NewBlocksTrimmed)
	}
}

// TestTrimInjectedSkillBlocks_P20_1_MismatchedFooter header/footer name 不匹配时
// footer 不算成对 → 整段走 fallback 剪到 EOF。
func TestTrimInjectedSkillBlocks_P20_1_MismatchedFooter(t *testing.T) {
	input := strings.Join([]string{
		"question",
		"[skill:foo::full@v1]",
		"body",
		"[/skill:different-name::full@v1]", // name 不匹配
		"still within block?",
	}, "\n")
	res := TrimInjectedSkillBlocksWithDiag(input)
	if res.Text != "question" {
		t.Fatalf("mismatched footer should fallback: got %q", res.Text)
	}
	if res.FooterMissingCount != 1 {
		t.Fatalf("FooterMissingCount = %d, want 1", res.FooterMissingCount)
	}
}

// TestTrimInjectedSkillBlocks_P20_1_SameSkillMultipleExpands 同一 skill 多次
// skill_expand 的场景（每次模型触发会先出个 new block）。
func TestTrimInjectedSkillBlocks_P20_1_SameSkillMultipleExpands(t *testing.T) {
	input := strings.Join([]string{
		"task 1",
		"[skill:foo::expanded@v1]",
		"first expand",
		"[/skill:foo::expanded@v1]",
		"task 2",
		"[skill:foo::expanded@v1]",
		"second expand",
		"[/skill:foo::expanded@v1]",
		"final",
	}, "\n")
	want := strings.Join([]string{
		"task 1",
		"task 2",
		"final",
	}, "\n")
	if got := TrimInjectedSkillBlocks(input); got != want {
		t.Fatalf("same-skill multi-expand trim: got %q want %q", got, want)
	}
}

// TestTrimInjectedSkillBlocks_P20_1_LegacyStillEOFTrim legacy 保留旧语义（剪到
// EOF），新格式 §3.4 的成对裁剪不应引起 legacy 语义漂移。
func TestTrimInjectedSkillBlocks_P20_1_LegacyStillEOFTrim(t *testing.T) {
	input := strings.Join([]string{
		"preserved text",
		"[skill:old]",
		"摘要: old summary",
		"使用方式: usage",
		"THIS MUST be dropped (legacy EOF trim)",
		"AND this too",
	}, "\n")
	res := TrimInjectedSkillBlocksWithDiag(input)
	if res.Text != "preserved text" {
		t.Fatalf("legacy EOF-trim: got %q want %q", res.Text, "preserved text")
	}
	if !res.LegacyTrimmed {
		t.Fatalf("LegacyTrimmed should be true")
	}
}

// TestTrimInjectedSkillBlocks_P20_1_WithDiagNoMatch 未命中时返回原 text，所有
// 诊断指标为零值。
func TestTrimInjectedSkillBlocks_P20_1_WithDiagNoMatch(t *testing.T) {
	input := "plain user prompt without any skill block"
	res := TrimInjectedSkillBlocksWithDiag(input)
	if res.Text != input {
		t.Fatalf("no-match must preserve original text: got %q", res.Text)
	}
	if res.NewBlocksTrimmed != 0 || res.LegacyTrimmed || res.FooterMissingCount != 0 {
		t.Fatalf("no-match diagnostics should be zero, got %+v", res)
	}
}

// TestTrimInjectedSkillBlocks_LegacyBreakOnNextHeader 防御 "碰到下一个 [skill:] 时
// break" 逻辑被删：第一个 header 和第二个 header 之间无 marker，只在第二个 header
// 之后才出现；AND 判定应在遇到第二个 header 时立刻停止，不该把后面的 marker 算进来。
func TestTrimInjectedSkillBlocks_LegacyBreakOnNextHeader(t *testing.T) {
	input := strings.Join([]string{
		"user text",
		"[skill:foo]", // 第一个 header——被测对象
		"unrelated content",
		"[skill:bar]", // 第二个 header——应该导致 break
		"摘要: only here",
		"使用方式: only here",
	}, "\n")
	// 第一个 header 的 AND 判定应在碰到第二个 header 时停止，不命中 → 不剥离。
	// 但第二个 header [skill:bar] + 后续两行 markers → 第二个 header 处命中剥离。
	// 最终结果应保留到第二个 header 之前。
	want := strings.Join([]string{
		"user text",
		"[skill:foo]",
		"unrelated content",
	}, "\n")
	if got := TrimInjectedSkillBlocks(input); got != want {
		t.Fatalf("break-on-next-header:\ngot  %q\nwant %q", got, want)
	}
}
