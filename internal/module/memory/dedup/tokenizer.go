// Package dedup 提供记忆条目的文本归一化、bigram 相似度计算与去重决策能力。
// 核心流程：原始文本 → Normalize → Bigrams → Decide / FindDuplicate。
package dedup

import (
	"strings"
	"unicode"
)

// 中文停用词表（内联常量集合）
var chineseStopWords = map[string]struct{}{
	"的": {}, "是": {}, "在": {}, "了": {}, "把": {},
	"被": {}, "和": {}, "与": {}, "或": {}, "不": {},
	"也": {}, "都": {}, "要": {}, "会": {}, "到": {},
	"就": {}, "这": {}, "那": {}, "有": {}, "个": {},
	"为": {}, "上": {}, "中": {}, "下": {}, "让": {},
	"从": {}, "对": {}, "已": {}, "但": {}, "而": {},
	"之": {},
}

// 英文停用词表（内联常量集合）
var englishStopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {},
	"was": {}, "were": {}, "be": {}, "been": {}, "to": {},
	"of": {}, "in": {}, "for": {}, "on": {}, "with": {},
	"at": {}, "by": {}, "and": {}, "or": {}, "but": {},
	"not": {}, "this": {}, "that": {}, "it": {}, "its": {},
}

// isCJK 判断 r 是否属于 CJK（中日韩）字符范围。
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Extension B
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0x2F800 && r <= 0x2FA1F) // CJK Compatibility Supplement
}

// toHalfwidth 将全角字符转换为对应的半角字符。
func toHalfwidth(r rune) rune {
	// Fullwidth ASCII variants: U+FF01 (！) to U+FF5E （～)
	if r >= 0xFF01 && r <= 0xFF5E {
		return r - 0xFEE0
	}
	// Ideographic space → regular space
	if r == 0x3000 {
		return ' '
	}
	return r
}

// stripFrontmatter 去除以 --- 包裹的 YAML frontmatter，返回正文部分。
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	// Skip past the opening ---
	rest := s[3:]
	// Skip optional newline after opening ---
	if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	} else if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	}
	// Search for closing --- at start of a line
	for _, sep := range []string{"\n---\n", "\n---\r\n", "\n---"} {
		if idx := strings.Index(rest, sep); idx >= 0 {
			after := rest[idx+len(sep):]
			return after
		}
	}
	return s
}

// stripMarkdown 去除常见 Markdown 格式符号，用空格替换。
func stripMarkdown(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '#', '*', '`', '>', '~', '_', '|', '\\', '!', '-':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// convertFullwidth 将字符串中的全角字符批量转换为半角字符。
func convertFullwidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(toHalfwidth(r))
	}
	return b.String()
}

// Normalize 将原始记忆内容归一化为可比较的文本。
// 依次执行：去除 frontmatter → 去除 Markdown 符号 → 全角转半角 → 过滤停用词。
func Normalize(raw string) string {
	s := stripFrontmatter(raw)
	s = stripMarkdown(s)
	s = convertFullwidth(s)
	return tokenizeAndFilter(s)
}

// tokenizeAndFilter 将文本拆分为词项（CJK 逐字、英文整词），过滤停用词后重组。
func tokenizeAndFilter(s string) string {
	var tokens []string
	var word strings.Builder

	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		w := word.String()
		word.Reset()
		if _, ok := englishStopWords[strings.ToLower(w)]; ok {
			return
		}
		tokens = append(tokens, w)
	}

	runes := []rune(s)
	n := len(runes)
	i := 0
	for i < n {
		r := runes[i]
		if isCJK(r) {
			flushWord()
			tok, end := collectCJKRun(runes, i, n)
			if tok != "" {
				tokens = append(tokens, tok)
			}
			i = end
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
			i++
		} else {
			flushWord()
			i++
		}
	}
	flushWord()
	return strings.Join(tokens, " ")
}

// collectCJKRun 收集从 start 开始的连续 CJK 字符，过滤停用词，返回拼接字符串和结束下标。
func collectCJKRun(runes []rune, start, n int) (string, int) {
	var b strings.Builder
	i := start
	for i < n && isCJK(runes[i]) {
		rs := string(runes[i])
		if _, ok := chineseStopWords[rs]; !ok {
			b.WriteString(rs)
		}
		i++
	}
	return b.String(), i
}

// Bigrams 将归一化文本拆分为 bigram 集合，CJK 字符按相邻字对生成，英文词作整体 token 保留。
// 返回去重后的 map[string]struct{}。
func Bigrams(normalized string) map[string]struct{} {
	result := make(map[string]struct{})
	if normalized == "" {
		return result
	}
	runes := []rune(normalized)
	n := len(runes)
	i := 0
	for i < n {
		if isCJK(runes[i]) {
			i = addCJKBigrams(runes, i, n, result)
		} else if isWordRune(runes[i]) {
			i = addASCIIWord(runes, i, n, result)
		} else {
			i++
		}
	}
	return result
}

// isWordRune 判断 r 是否为非 CJK 的字母或数字（ASCII 词的组成字符）。
func isWordRune(r rune) bool {
	return (unicode.IsLetter(r) || unicode.IsDigit(r)) && !isCJK(r)
}

// addCJKBigrams 从 start 开始收集连续 CJK 字符，生成相邻字对并写入 result。
func addCJKBigrams(runes []rune, start, n int, result map[string]struct{}) int {
	j := start
	for j < n && isCJK(runes[j]) {
		j++
	}
	run := runes[start:j]
	for k := 0; k+1 < len(run); k++ {
		result[string(run[k:k+2])] = struct{}{}
	}
	return j
}

// addASCIIWord 从 start 开始收集连续非 CJK 词字符，作为整体 token 写入 result。
func addASCIIWord(runes []rune, start, n int, result map[string]struct{}) int {
	j := start
	for j < n && isWordRune(runes[j]) {
		j++
	}
	result[string(runes[start:j])] = struct{}{}
	return j
}

// Containment 计算包含系数 = |A∩B| / |较短集合|，任一集合为空时返回 0。
func Containment(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	// Ensure shorter is the smaller set
	shorter, longer := a, b
	if len(b) < len(a) {
		shorter, longer = b, a
	}

	intersection := 0
	for k := range shorter {
		if _, ok := longer[k]; ok {
			intersection++
		}
	}

	return float64(intersection) / float64(len(shorter))
}

// Jaccard 计算 Jaccard 相似度 = |A∩B| / |A∪B|，两集合均为空时返回 0。
func Jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
