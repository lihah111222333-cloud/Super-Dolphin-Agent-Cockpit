package edit

import (
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// MatchMode identifies the normalization strategy that found a line sequence.
type MatchMode string

const (
	seekMatchExact             MatchMode = "exact"
	seekMatchTrimRight         MatchMode = "trim_right"
	seekMatchTrimBoth          MatchMode = "trim_both"
	seekMatchUnicodeNormalized MatchMode = "unicode_normalized"
	seekMatchEscapeNormalized  MatchMode = "escape_normalized"
)

// SeekSequence finds the first occurrence of pattern in lines using the 4-pass
// relaxed matching strategy mandated by the migration plan.
// SeekSequence 查找序列。
func SeekSequence(lines []string, pattern []string, start int) (int, MatchMode, error) {
	if len(pattern) == 0 {
		return -1, "", fmt.Errorf("%w: empty pattern", ErrSequenceNotFound)
	}
	from, to, err := seekSequenceBounds(lines, pattern, start)
	if err != nil {
		return -1, "", err
	}
	for _, mode := range allSeekModes() {
		if pos := seekSequenceMode(lines, pattern, from, to, mode); pos >= 0 {
			return pos, mode, nil
		}
	}
	return -1, "", fmt.Errorf("%w: pattern not found", ErrSequenceNotFound)
}

func seekSequenceBounds(lines []string, pattern []string, start int) (int, int, error) {
	if len(pattern) > len(lines) {
		return -1, -1, fmt.Errorf("%w: pattern is longer than input", ErrSequenceNotFound)
	}
	if start < 0 {
		start = 0
	}
	if start > len(lines)-len(pattern) {
		return -1, -1, fmt.Errorf("%w: start offset is outside the searchable range", ErrSequenceNotFound)
	}
	return start, len(lines) - len(pattern), nil
}

func seekSequenceMode(lines []string, pattern []string, start int, end int, mode MatchMode) int {
	for idx := start; idx <= end; idx++ {
		if sequenceMatchAt(lines, pattern, idx, mode) {
			return idx
		}
	}
	return -1
}

// collectSequenceMatches 收集序列matches。
func collectSequenceMatches(lines []string, pattern []string) ([]int, MatchMode) {
	if len(pattern) == 0 || len(pattern) > len(lines) {
		return nil, ""
	}
	limit := len(lines) - len(pattern)
	for _, mode := range allSeekModes() {
		matches := make([]int, 0, 4)
		for idx := 0; idx <= limit; idx++ {
			if sequenceMatchAt(lines, pattern, idx, mode) {
				matches = append(matches, idx)
			}
		}
		if len(matches) > 0 {
			return matches, mode
		}
	}
	return nil, ""
}

func sequenceMatchAt(lines []string, pattern []string, start int, mode MatchMode) bool {
	for idx, want := range pattern {
		if !lineMatch(lines[start+idx], want, mode) {
			return false
		}
	}
	return true
}

// lineMatch 判断行是否匹配。
func lineMatch(have string, want string, mode MatchMode) bool {
	switch mode {
	case seekMatchExact:
		return have == want
	case seekMatchTrimRight:
		return trimRightSpace(have) == trimRightSpace(want)
	case seekMatchTrimBoth:
		return strings.TrimSpace(have) == strings.TrimSpace(want)
	case seekMatchUnicodeNormalized:
		return normalizeUnicode(strings.TrimSpace(have)) == normalizeUnicode(strings.TrimSpace(want))
	case seekMatchEscapeNormalized:
		return normalizeEscape(strings.TrimSpace(have)) == normalizeEscape(strings.TrimSpace(want))
	default:
		return false
	}
}

func allSeekModes() []MatchMode {
	return []MatchMode{
		seekMatchExact,
		seekMatchTrimRight,
		seekMatchTrimBoth,
		seekMatchUnicodeNormalized,
		seekMatchEscapeNormalized,
	}
}

func trimRightSpace(value string) string {
	return strings.TrimRight(value, " \t\r\n")
}

// normalizeUnicode maps common Unicode punctuation variants to their ASCII
// equivalents. Ported from V2's hand-written replacement table which matches
// codex-rs apply-patch seek_sequence.rs:76-94.
// normalizeUnicode 规范化Unicode。
func normalizeUnicode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			b.WriteByte('-')
		case '\u2018', '\u2019', '\u201A', '\u201B':
			b.WriteByte('\'')
		case '\u201C', '\u201D', '\u201E', '\u201F':
			b.WriteByte('"')
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	// Apply NFC normalization after the replacement table so that
	// combining character sequences (e.g. e+\u0301) are also handled.
	return norm.NFC.String(b.String())
}

// normalizeEscape collapses common escape-level differences that arise when
// LLMs generate patch old_text with one fewer (or one more) layer of
// backslash escaping than the actual file content.
//
// Rules (applied left-to-right, greedy):
//   - `\"` → `"` (backslash-quote → quote)
//   - `\\` → `\` (double-backslash → single-backslash)
//   - `\n`  → `\n` (literal backslash-n stays; real newline is already split)
//   - `\t`  → `\t` (literal backslash-t stays)
//
// This is intentionally a last-resort fallback; earlier passes (exact,
// trim_right, trim_both, unicode_normalized) take priority.
// normalizeEscape 规范化转义。
func normalizeEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			switch next {
			case '"', '\'', '\\': // \" → " , \' → ' , \\ → \
				b.WriteByte(next)
				i++
				continue
			case 'n': // \n stays as literal \n
				b.WriteByte('\\')
				b.WriteByte('n')
				i++
				continue
			case 't': // \t stays
				b.WriteByte('\\')
				b.WriteByte('t')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
