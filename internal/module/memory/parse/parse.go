// Package parse holds the small set of memory-file parsing primitives that
// must stay byte-for-byte identical across the memory package and its
// retrieval / nested subpackages. Phase 2.0 review flagged the previous
// 3-copy duplication of these helpers as a security-parity risk: a fix
// landing in only one copy would let prompt-injection vectors regress.
//
// This package intentionally has zero external dependencies (stdlib only)
// so it can be a leaf import for any consumer in the memory module without
// triggering import cycles.
package parse

import (
	"bufio"
	"io"
	"strings"
	"unicode/utf8"
)

// StripUTF8BOM removes any leading UTF-8 byte-order marks and returns the
// remainder unchanged. Looping (rather than a single TrimPrefix) closes a
// minor parser bypass: a crafted file with multiple BOMs ("\ufeff\ufeff---")
// would otherwise still start with a BOM after one trim, the leading `---`
// fence would not be detected, and frontmatter (including the cleanup it
// triggers) would be silently skipped.
// StripUTF8BOM 处理striputf8bom。
func StripUTF8BOM(content string) string {
	for strings.HasPrefix(content, "\ufeff") {
		content = strings.TrimPrefix(content, "\ufeff")
	}
	return content
}

// IsFence reports whether a single (already CRLF-normalized) line is exactly
// the YAML frontmatter delimiter `---`, ignoring any horizontal whitespace
// (ASCII space and tab) that follows the three dashes. Editors and shell
// pipelines occasionally append a stray space; without this tolerance the
// parser would silently treat such files as having no frontmatter and leak
// the YAML into prompts.
// IsFence 判断fence是否可用。
func IsFence(line string) bool {
	return strings.TrimRight(line, " \t") == "---"
}

// SplitFrontmatter recognises a YAML frontmatter block at the top of a
// memory file and returns (frontmatter, body, ok=true) when one is found,
// or ("", originalContent, false) otherwise. Both the leading and the
// closing fence may carry trailing horizontal whitespace per IsFence.
//
// Semantics match Claude Code's claudemd parser:
//   - CRLF input is normalised to LF before scanning.
//   - The closing fence MUST appear on its own line; substrings within
//     body lines (e.g. an inline `---inline-mark---`) are not accepted as
//     a close.
//   - When no closing fence is found before EOF, the input is treated as
//     having no frontmatter (the YAML is NOT injected into the prompt).
//
// ScanFrontmatterHeader reads from r (capped at limit bytes) and returns the
// joined leading lines up to and including the second `---` fence, so the
// result can be handed directly to SplitFrontmatter without further BOM or
// fence handling. The first line has any leading UTF-8 BOM stripped before
// fence detection so files saved with a BOM are not silently treated as
// having no frontmatter. If only one (or zero) fences appear within the
// limit, every byte read is returned and SplitFrontmatter will then report
// no frontmatter.
// ScanFrontmatterHeader 扫描frontmatter头部。
func ScanFrontmatterHeader(r io.Reader, limit int) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, int64(limit)))
	scanner.Buffer(make([]byte, 0, 4096), limit)
	var builder strings.Builder
	openMarkers := 0
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = StripUTF8BOM(line)
			first = false
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
		if IsFence(line) {
			openMarkers++
			if openMarkers >= 2 {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// SplitFrontmatter 拆分frontmatter。
func SplitFrontmatter(content string) (string, string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) < 2 || !IsFence(lines[0]) {
		return "", content, false
	}
	rest := lines[1]
	var frontmatter strings.Builder
	for {
		next := strings.SplitN(rest, "\n", 2)
		candidate := next[0]
		if IsFence(candidate) {
			body := ""
			if len(next) == 2 {
				body = next[1]
			}
			return strings.TrimRight(frontmatter.String(), "\n"), body, true
		}
		if len(next) < 2 {
			return "", content, false
		}
		frontmatter.WriteString(candidate)
		frontmatter.WriteByte('\n')
		rest = next[1]
	}
}

// StripHTMLComments removes Claude Code editor scaffolding HTML comments
// while preserving content semantics that downstream parsers depend on.
// Specifically:
//   - Lines beginning with `<!--` (after leading whitespace) are dropped
//     entirely; if a `-->` close appears later on the same line, the
//     trailing residue is kept (the comment block is removed in place).
//   - Multi-line block comments (`<!-- ...\n... -->`) are dropped, and the
//     residue after `-->` on the closing line is kept if non-empty.
//   - Inline comments mid-line (e.g. `Use <!-- note --> here`) are LEFT
//     UNCHANGED. Stripping them here would break inline annotations that
//     authors deliberately include in prose.
//   - Comments inside fenced code blocks (``` or ~~~) are preserved so that
//     documentation can render literal HTML comment examples.
//   - Content inside `<![CDATA[ ... ]]>` blocks (single- or multi-line)
//     is preserved verbatim; a `<!--` appearing inside an open CDATA span
//     is NOT treated as a comment opener. Once `]]>` closes the CDATA,
//     normal scanning resumes.
//   - HTML5 forbids nesting `<!-- <!-- --> -->`; the first `-->` closes
//     the comment and the trailing `-->` survives as plain text. The
//     scanner mirrors that behaviour rather than implementing real
//     nesting depth tracking.
//   - An UNCLOSED `<!--` (or `<![CDATA[`) is treated as plain content
//     (not silently dropped) so a malformed file cannot smuggle hidden
//     directives into the prompt by pretending the rest of the file is
//     one giant comment.
//
// StripHTMLComments 处理striphtmlcomments。
func StripHTMLComments(content string) string {
	// Iterate to a fixed point. A single pass can produce a string whose
	// removed-then-rejoined neighbour bytes accidentally compose a new
	// `<!-- ... -->` (e.g. residue `<!` followed by `---->` after a
	// closed `<!---->` is dropped between them). Re-running until the
	// scanner reports no further strips guarantees idempotence:
	// StripHTMLComments(StripHTMLComments(x)) == StripHTMLComments(x).
	// Termination: every productive iteration removes at least one byte,
	// so the loop runs at most len(content) times.
	for {
		next, stripped := stripHTMLCommentsScan(content)
		if !stripped {
			return next
		}
		if next == content {
			return next
		}
		content = next
	}
}

func stripHTMLCommentsScan(content string) (string, bool) {
	// Fast path: no comment opener and no CDATA opener anywhere in the
	// input means there is nothing to strip and nothing to shield.
	if !strings.Contains(content, "<!--") && !strings.Contains(content, "<![CDATA[") {
		return content, false
	}
	state := &htmlCommentStripState{}
	for _, line := range strings.SplitAfter(content, "\n") {
		state.processLine(line)
	}
	// Unclosed `<!--` at EOF: surface the buffered text as plain content
	// so a truncated comment cannot swallow the rest of the file.
	if state.inComment {
		state.builder.WriteString(state.pendingComment.String())
	}
	return state.builder.String(), state.stripped
}

type markdownFenceState struct {
	open   bool
	marker byte
}

type htmlCommentStripState struct {
	fence          markdownFenceState
	builder        strings.Builder
	pendingComment strings.Builder
	stripped       bool
	inComment      bool
	inCDATA        bool
}

// processLine 处理进程行。
func (s *htmlCommentStripState) processLine(line string) {
	// CDATA dominates: while inside a CDATA span no comment scanning
	// runs, so a `<!--` token between `<![CDATA[` and `]]>` survives.
	if s.inCDATA {
		s.builder.WriteString(line)
		if strings.Contains(line, "]]>") {
			s.inCDATA = false
		}
		return
	}
	if s.processPendingLine(line) {
		return
	}
	if lineInMarkdownFence(&s.fence, line) {
		s.builder.WriteString(line)
		return
	}
	if s.openCDATAIfUnclosed(line) {
		return
	}
	if !startsHTMLCommentBlock(line) {
		s.builder.WriteString(line)
		return
	}
	if s.stripInlineComment(line) {
		return
	}
	s.pendingComment.WriteString(line)
	s.inComment = true
}

func (s *htmlCommentStripState) processPendingLine(line string) bool {
	if !s.inComment {
		return false
	}
	s.pendingComment.WriteString(line)
	// HTML5 does not nest comments; the first `-->` closes the span. We
	// keep the residue after that token as plain content so a stray
	// `--> trailing -->` becomes ` trailing -->` rather than vanishing.
	_, residue, ok := strings.Cut(line, "-->")
	if !ok {
		return true
	}
	s.stripped = true
	s.pendingComment.Reset()
	s.inComment = false
	s.emitCommentResidue(residue)
	return true
}

// openCDATAIfUnclosed handles a line that begins (or contains) a CDATA
// opener whose `]]>` close lives on a later line. The whole line is
// emitted verbatim and the scanner switches to inCDATA so the next line
// short-circuits comment scanning. Returns true if it took ownership of
// the line. CDATA spans that open and close on the same line are not
// special-cased here: comment stripping ignores mid-line `<!--` anyway,
// so a single-line CDATA carrying a fake comment is already preserved.
func (s *htmlCommentStripState) openCDATAIfUnclosed(line string) bool {
	idx := strings.Index(line, "<![CDATA[")
	if idx < 0 {
		return false
	}
	if strings.Contains(line[idx+len("<![CDATA["):], "]]>") {
		return false
	}
	s.builder.WriteString(line)
	s.inCDATA = true
	return true
}

// stripInlineComment is invoked when the line begins (after leading
// whitespace) with `<!--` AND contains a `-->` somewhere later on the
// same line. It peels off the leading `<!-- ... -->` and hands the tail
// to emitCommentResidue, which itself recursively strips any further
// already-closed `<!-- ... -->` segments hiding inside the residue. The
// double pass is what gives StripHTMLComments idempotence: without it,
// fuzz inputs like `<!----><!--0\n-->0` and `<!--\n--> <!--0-->` would
// leave a residual `<!--...-->` that a second call would then drop.
func (s *htmlCommentStripState) stripInlineComment(line string) bool {
	if !strings.Contains(line, "-->") {
		return false
	}
	s.stripped = true
	trimmed := strings.TrimLeft(line, " \t")
	afterOpen := trimmed[len("<!--"):]
	_, residue, ok := strings.Cut(afterOpen, "-->")
	if !ok {
		// strings.Contains(line, "-->") was true above, so the only way Cut
		// can fail is if the `-->` lives inside the leading whitespace —
		// impossible because TrimLeft removed it. Defensive fallback: treat
		// the line as an unclosed opener.
		s.pendingComment.WriteString(line)
		s.inComment = true
		return true
	}
	s.emitCommentResidue(residue)
	return true
}

// emitCommentResidue handles the tail produced after a `-->` close. It
// silently drops any further already-closed `<!-- ... -->` segments
// hiding in the tail (they would otherwise re-trigger stripInlineComment
// on a second pass and break idempotence) and, if the tail ends on an
// unclosed `<!--`, transitions the scanner into inComment with the open
// span buffered. The non-comment text in between is preserved so authors
// do not lose words tucked between scaffolding markers.
func (s *htmlCommentStripState) emitCommentResidue(residue string) {
	var plain strings.Builder
	pos := 0
	for {
		idx := strings.Index(residue[pos:], "<!--")
		if idx < 0 {
			plain.WriteString(residue[pos:])
			appendNonEmptyLine(&s.builder, plain.String())
			return
		}
		absIdx := pos + idx
		afterOpen := absIdx + len("<!--")
		closeIdx := strings.Index(residue[afterOpen:], "-->")
		if closeIdx < 0 {
			// Tail opens an unclosed comment: keep the plain prefix, buffer
			// the open span so the next line either closes or surfaces it.
			plain.WriteString(residue[pos:absIdx])
			appendNonEmptyLine(&s.builder, plain.String())
			s.pendingComment.WriteString(residue[absIdx:])
			s.inComment = true
			return
		}
		plain.WriteString(residue[pos:absIdx])
		pos = afterOpen + closeIdx + len("-->")
	}
}

func appendNonEmptyLine(builder *strings.Builder, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	builder.WriteString(line)
}

func startsHTMLCommentBlock(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "<!--")
}

func lineInMarkdownFence(state *markdownFenceState, line string) bool {
	marker, ok := markdownFenceMarker(line)
	if state.open {
		if ok && marker == state.marker {
			state.open = false
			state.marker = 0
		}
		return true
	}
	if !ok {
		return false
	}
	state.open = true
	state.marker = marker
	return true
}

func markdownFenceMarker(line string) (byte, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "```") {
		return '`', true
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return '~', true
	}
	return 0, false
}

// JSStringLength returns the length of content measured in UTF-16 code units,
// matching the value `String.prototype.length` would report in JavaScript.
// The memory module uses this to mirror the truncation budgets enforced by
// the Claude Code UI, which speaks in UTF-16 units rather than Go runes or
// raw bytes.
// JSStringLength 处理jsstringlength。
func JSStringLength(content string) int {
	count := 0
	for bytePos := 0; bytePos < len(content); {
		r, size := utf8.DecodeRuneInString(content[bytePos:])
		count += UTF16CodeUnits(r)
		bytePos += size
	}
	return count
}

// UTF16CodeUnits returns 2 for runes outside the Basic Multilingual Plane
// (which JavaScript represents as a surrogate pair) and 1 otherwise.
// UTF16CodeUnits 处理utf16代码units。
func UTF16CodeUnits(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

// TruncateAtCodeUnitLimit cuts content so that JSStringLength(result) <= limit,
// preferring to break at the last newline before the limit. When the limit
// falls inside the very first line the result is byte-truncated instead of
// returned empty, so callers always make forward progress when they intend
// to append a warning footer.
// TruncateAtCodeUnitLimit 截断at代码unitlimit。
func TruncateAtCodeUnitLimit(content string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if JSStringLength(content) <= limit {
		return content
	}
	bytePos := 0
	codeUnits := 0
	lastNewline := -1
	for bytePos < len(content) {
		r, size := utf8.DecodeRuneInString(content[bytePos:])
		units := UTF16CodeUnits(r)
		if codeUnits+units > limit {
			break
		}
		if r == '\n' {
			lastNewline = bytePos
		}
		codeUnits += units
		bytePos += size
	}
	if lastNewline >= 0 {
		return content[:lastNewline]
	}
	return content[:bytePos]
}
