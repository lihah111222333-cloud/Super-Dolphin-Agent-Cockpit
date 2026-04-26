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
	"regexp"
	"strings"
	"unicode/utf8"
)

// StripUTF8BOM removes any leading UTF-8 byte-order marks and returns the
// remainder unchanged. Looping (rather than a single TrimPrefix) closes a
// minor parser bypass: a crafted file with multiple BOMs ("\ufeff\ufeff---")
// would otherwise still start with a BOM after one trim, the leading `---`
// fence would not be detected, and frontmatter (including the cleanup it
// triggers) would be silently skipped.
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

var htmlCommentLinePattern = regexp.MustCompile(`<!--.*?-->`)

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
//   - An UNCLOSED `<!--` is treated as plain content (not silently dropped)
//     so a malformed file cannot smuggle hidden directives into the prompt
//     by pretending the rest of the file is one giant comment.
func StripHTMLComments(content string) string {
	stripped, _ := stripHTMLCommentsScan(content)
	return stripped
}

func stripHTMLCommentsScan(content string) (string, bool) {
	if !strings.Contains(content, "<!--") {
		return content, false
	}
	state := &htmlCommentStripState{}
	for _, line := range strings.SplitAfter(content, "\n") {
		state.processLine(line)
	}
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
}

func (s *htmlCommentStripState) processLine(line string) {
	if s.processPendingLine(line) {
		return
	}
	if lineInMarkdownFence(&s.fence, line) {
		s.builder.WriteString(line)
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
	_, residue, ok := strings.Cut(line, "-->")
	if !ok {
		return true
	}
	s.stripped = true
	appendNonEmptyLine(&s.builder, residue)
	s.pendingComment.Reset()
	s.inComment = false
	return true
}

func (s *htmlCommentStripState) stripInlineComment(line string) bool {
	if !strings.Contains(line, "-->") {
		return false
	}
	s.stripped = true
	appendNonEmptyLine(&s.builder, htmlCommentLinePattern.ReplaceAllString(line, ""))
	return true
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
