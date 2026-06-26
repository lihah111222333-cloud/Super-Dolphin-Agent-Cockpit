package parse

import (
	"strings"
	"testing"
)

func TestSplitFrontmatterCases(t *testing.T) {
	cases := []struct {
		name            string
		in              string
		wantFrontmatter string
		wantBody        string
		wantOK          bool
	}{
		{name: "empty", in: "", wantOK: false, wantBody: ""},
		{name: "no_frontmatter", in: "- entry\n", wantOK: false, wantBody: "- entry\n"},
		{
			name:            "minimal",
			in:              "---\nname: x\n---\nbody\n",
			wantFrontmatter: "name: x",
			wantBody:        "body\n",
			wantOK:          true,
		},
		{
			name:            "trailing_whitespace_on_fences",
			in:              "--- \nname: x\n--- \nbody\n",
			wantFrontmatter: "name: x",
			wantBody:        "body\n",
			wantOK:          true,
		},
		{
			name:            "tab_after_fence",
			in:              "---\t\nname: x\n---\t\nbody\n",
			wantFrontmatter: "name: x",
			wantBody:        "body\n",
			wantOK:          true,
		},
		{
			name:            "crlf_input_normalised",
			in:              "---\r\nname: x\r\n---\r\nbody\r\n",
			wantFrontmatter: "name: x",
			wantBody:        "body\n",
			wantOK:          true,
		},
		{
			name:   "unclosed_frontmatter_returns_no_frontmatter",
			in:     "---\nname: x\n",
			wantOK: false,
			// 没有闭合 fence 时，完整内容都应作为 body 返回。
			wantBody: "---\nname: x\n",
		},
		{
			name:            "inline_dashes_in_body_are_not_a_close_fence",
			in:              "---\nname: x\n---\nbefore ---inline--- after\n",
			wantFrontmatter: "name: x",
			wantBody:        "before ---inline--- after\n",
			wantOK:          true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fm, body, ok := SplitFrontmatter(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("SplitFrontmatter(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if fm != tc.wantFrontmatter {
				t.Fatalf("SplitFrontmatter(%q) frontmatter = %q, want %q", tc.in, fm, tc.wantFrontmatter)
			}
			if body != tc.wantBody {
				t.Fatalf("SplitFrontmatter(%q) body = %q, want %q", tc.in, body, tc.wantBody)
			}
		})
	}
}

func TestIsFenceCases(t *testing.T) {
	for _, line := range []string{"---", "--- ", "---\t", "---  \t \t"} {
		if !IsFence(line) {
			t.Fatalf("IsFence(%q) = false, want true", line)
		}
	}
	// 负例覆盖曾经容易误判为闭合 fence 的真实输入形态。
	for _, line := range []string{
		"--",
		"----",
		" ---",  // 前置空格。
		"\t---", // 前置 tab。
		"---x",
		"---inline---",
		"--- inline",  // 闭合 fence 后仍有后缀。
		"---trailing", // 短横线与正文粘连。
	} {
		if IsFence(line) {
			t.Fatalf("IsFence(%q) = true, want false", line)
		}
	}
}

// TestSplitFrontmatterRejectsFalseCloseFences ensures body lines that
// merely contain `---` characters (rather than a fence on their own line)
// do not terminate the frontmatter block.
func TestSplitFrontmatterRejectsFalseCloseFences(t *testing.T) {
	cases := []string{
		"---\nname: x\n----\n",              // 四个短横线不是闭合 fence。
		"---\nname: x\n---inline-mark---\n", // 行内类似 fence 的文本不应闭合。
		"---\nname: x\n--- trailing\n",      // 短横线后跟单词不应闭合。
		"---\nname: x\n  ---\n",             // 缩进 fence 不应闭合。
	}
	for _, in := range cases {
		_, _, ok := SplitFrontmatter(in)
		if ok {
			t.Fatalf("SplitFrontmatter(%q) ok = true, want false (no real closing fence)", in)
		}
	}
}

func TestStripUTF8BOM(t *testing.T) {
	bom := "\ufeff"
	if got := StripUTF8BOM(bom + "hello"); got != "hello" {
		t.Fatalf("StripUTF8BOM with BOM = %q, want %q", got, "hello")
	}
	if got := StripUTF8BOM("hello"); got != "hello" {
		t.Fatalf("StripUTF8BOM no BOM = %q, want %q", got, "hello")
	}
	if got := StripUTF8BOM(""); got != "" {
		t.Fatalf("StripUTF8BOM empty = %q, want empty", got)
	}
	// 堆叠 BOM 必须全部剥离，避免构造的 `\ufeff\ufeff---...` 绕过 frontmatter 检测。
	if got := StripUTF8BOM(bom + bom + "---\nname: x\n---\nbody\n"); !strings.HasPrefix(got, "---\n") {
		t.Fatalf("StripUTF8BOM did not strip stacked BOMs; prefix = %q", got)
	}
	if got := StripUTF8BOM(bom + bom + bom + "x"); got != "x" {
		t.Fatalf("StripUTF8BOM 3 BOMs = %q, want %q", got, "x")
	}
}

func TestScanFrontmatterHeaderStripsBOMOnFirstLineSoSplitFrontmatterAccepts(t *testing.T) {
	bom := "\ufeff"
	input := bom + "---\nname: x\n---\nbody after fence should not be returned\n"
	header, err := ScanFrontmatterHeader(strings.NewReader(input), 4096)
	if err != nil {
		t.Fatalf("ScanFrontmatterHeader err = %v", err)
	}
	if strings.HasPrefix(header, bom) {
		t.Fatalf("header still carries BOM: %q", header)
	}
	fm, _, ok := SplitFrontmatter(header)
	if !ok {
		t.Fatalf("SplitFrontmatter on scanned header reported no frontmatter; header = %q", header)
	}
	if !strings.Contains(fm, "name: x") {
		t.Fatalf("frontmatter missing name field: %q", fm)
	}
}

func TestScanFrontmatterHeaderUnterminatedReportsNoFrontmatter(t *testing.T) {
	// 限制范围内没有第二个 fence 时，scanner 返回已读内容。
	// SplitFrontmatter 随后判定无 frontmatter，确保 YAML 不会被注入 prompt。
	input := "---\nname: x\n" + strings.Repeat("line\n", 1000)
	header, err := ScanFrontmatterHeader(strings.NewReader(input), 64)
	if err != nil {
		t.Fatalf("ScanFrontmatterHeader err = %v", err)
	}
	if _, _, ok := SplitFrontmatter(header); ok {
		t.Fatalf("unterminated frontmatter must not be reported as ok; header = %q", header)
	}
}

// UTF-16 与 HTML 注释清理 helper 的直接回归测试。

func TestJSStringLengthCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"bmp_chinese", "你好", 2},
		{"emoji_supplementary", "😀", 2}, // 补充平面 emoji 是 surrogate pair，占 2 个 UTF-16 unit。
		{"mixed", "a你😀", 1 + 1 + 2},     // ASCII、BMP 字符和 surrogate pair 混合。
		{"crlf", "a\r\nb", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JSStringLength(c.in); got != c.want {
				t.Fatalf("JSStringLength(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestUTF16CodeUnitsCases(t *testing.T) {
	cases := []struct {
		name string
		in   rune
		want int
	}{
		{"ascii", 'a', 1},
		{"bmp", '中', 1},
		{"supplementary", 0x1F600, 2}, // 补充平面字符需要 2 个 UTF-16 unit。
		{"max_bmp", 0xFFFF, 1},
		{"min_supplementary", 0x10000, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := UTF16CodeUnits(c.in); got != c.want {
				t.Fatalf("UTF16CodeUnits(%U) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestTruncateAtCodeUnitLimitCases(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"under_limit", "hello", 10, "hello"},
		{"exact_limit", "hello", 5, "hello"},
		{"ascii_truncate", "hello world", 5, "hello"},
		{"truncate_at_newline_boundary", "line1\nline2\nline3", 12, "line1\nline2"},
		{"newline_at_zero_position", "\nrest", 1, ""},     // 首字符为换行时不应越界。
		{"surrogate_pair_keeps_emoji", "ab😀cd", 4, "ab😀"}, // 4-unit 限制刚好容纳 ab 加 emoji。
		{"surrogate_pair_drops_emoji", "ab😀cd", 3, "ab"},  // 3-unit 限制不能容纳 emoji 的两个 unit。
		{"empty", "", 5, ""},
		{"limit_zero", "hello", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TruncateAtCodeUnitLimit(c.in, c.limit); got != c.want {
				t.Fatalf("TruncateAtCodeUnitLimit(%q, %d) = %q, want %q", c.in, c.limit, got, c.want)
			}
		})
	}
}

func TestStripHTMLCommentsCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no_comments", "hello world", "hello world"},
		{"midline_inline_preserved", "before <!-- middle --> after\n", "before <!-- middle --> after\n"},
		{"line_only_dropped", "<!-- whole line -->\n", ""},
		{"line_with_residue_kept", "<!-- gone --> visible\n", " visible\n"}, // 同行剩余文本保留原前导空格。
		{"multiline_block", "before\n<!-- start\nbody\nend -->\nafter\n", "before\nafter\n"},
		{"unclosed_eof_kept_as_content", "before\n<!-- never closed", "before\n<!-- never closed"},
		{"empty", "", ""},
		// HTML5 不支持嵌套注释；第一个 --> 关闭后，后续内容按普通文本保留。
		{"nested_attempt_first_close_wins", "<!-- outer <!-- inner --> outer -->\n", " outer -->\n"},
		{"nested_attempt_multiline", "<!-- outer\n<!-- inner\n--> rest -->\n", " rest -->\n"},
		// CDATA 内容不参与 HTML 注释扫描。
		{"cdata_multiline_protects_comment", "before\n<![CDATA[\n<!-- not a comment -->\n]]>\nafter\n", "before\n<![CDATA[\n<!-- not a comment -->\n]]>\nafter\n"},
		{"cdata_singleline_keeps_inline_comment", "<![CDATA[ <!-- preserved --> ]]>\n", "<![CDATA[ <!-- preserved --> ]]>\n"},
		{"cdata_unclosed_eof_kept", "head\n<![CDATA[\n<!-- looks like comment -->\n", "head\n<![CDATA[\n<!-- looks like comment -->\n"},
		// fenced code block 内的 HTML 注释样式文本必须保留。
		{"fence_protects_comment", "```\n<!-- not stripped -->\n```\n", "```\n<!-- not stripped -->\n```\n"},
		{"fence_tilde_protects_comment", "~~~\n<!-- preserved -->\n~~~\n", "~~~\n<!-- preserved -->\n~~~\n"},
		// 连续多行注释应全部删除。
		{"two_block_comments", "a\n<!-- one\nstill one -->\nmid\n<!-- two\nstill two -->\nz\n", "a\nmid\nz\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StripHTMLComments(c.in); got != c.want {
				t.Fatalf("StripHTMLComments(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// FuzzStripHTMLComments 用任意字节流覆盖 HTML 注释扫描器，并固定两个不变量：
// 函数不能 panic；重复清理两次必须与清理一次结果一致，避免下游缓存看到漂移 hash。
// 如需扩展覆盖，可运行 `go test -fuzz=FuzzStripHTMLComments ./internal/module/memory/parse/`。
func FuzzStripHTMLComments(f *testing.F) {
	seeds := []string{
		"",
		"plain text\n",
		"<!-- a -->\n",
		"prefix <!-- inline --> suffix\n",
		"<!-- outer <!-- inner --> outer -->\n",
		"<!-- multi\nline\n--> tail\n",
		"<!-- never closed",
		"<![CDATA[ <!-- shielded --> ]]>\n",
		"<![CDATA[\n<!-- shielded -->\n]]>\n",
		"<![CDATA[\nstill open",
		"```\n<!-- in fence -->\n```\n",
		"~~~\n<!-- in tilde -->\n~~~\n",
		"<!-- a -->\n<!-- b -->\n<!-- c -->\n",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		once := StripHTMLComments(content)
		twice := StripHTMLComments(once)
		if once != twice {
			t.Fatalf("StripHTMLComments not idempotent\ninput  = %q\nonce   = %q\ntwice  = %q", content, once, twice)
		}
	})
}
