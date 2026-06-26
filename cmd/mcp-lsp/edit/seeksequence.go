// Package edit 提供 patch 解析与匹配功能，用于 LSP replace_range 编辑操作。
package edit

import (
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
)

type MatchMode string

// seek 匹配模式按从严格到宽松的顺序尝试，后续模式只在前序模式未命中时使用。
const (
	seekMatchExact             MatchMode = "exact"
	seekMatchTrimRight         MatchMode = "trim_right"
	seekMatchTrimBoth          MatchMode = "trim_both"
	seekMatchUnicodeNormalized MatchMode = "unicode_normalized"
	seekMatchEscapeNormalized  MatchMode = "escape_normalized"
)

// SeekSequence 在行列表中查找 pattern 的首个安全落点。
// 它按严格到宽松的匹配模式依次尝试，仍未命中时返回 ErrSequenceNotFound。
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

// collectSequenceMatches 返回同一种匹配模式下的全部候选行。
// 调用方可据此判断是否需要上下文锚点进一步消歧。
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

// lineMatch 按指定模式比较单行文本。
// 宽松模式只处理空白、Unicode 标点和转义层级差异，不改动原始文本。
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

// normalizeUnicode 把常见 Unicode 标点和空格折叠成 ASCII 形式。
// 该函数只服务于补丁匹配容错，不会改变最终写入的 replacement。
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
	// 替换表之后再做 NFC，确保组合字符也能在宽松匹配中对齐。
	return norm.NFC.String(b.String())
}

// normalizeEscape 折叠常见反斜杠转义层级差异。
// 它是最后兜底的匹配模式，只让引号和反斜杠少一层转义，保留字面 \n 与 \t。
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
