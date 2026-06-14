package dedup

import (
	"strings"
	"unicode"
)

// Chinese stop words (inline constant set)
var chineseStopWords = map[string]struct{}{
	"的": {}, "是": {}, "在": {}, "了": {}, "把": {},
	"被": {}, "和": {}, "与": {}, "或": {}, "不": {},
	"也": {}, "都": {}, "要": {}, "会": {}, "到": {},
	"就": {}, "这": {}, "那": {}, "有": {}, "个": {},
	"为": {}, "上": {}, "中": {}, "下": {}, "让": {},
	"从": {}, "对": {}, "已": {}, "但": {}, "而": {},
	"之": {},
}

// English stop words (inline constant set)
var englishStopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {},
	"was": {}, "were": {}, "be": {}, "been": {}, "to": {},
	"of": {}, "in": {}, "for": {}, "on": {}, "with": {},
	"at": {}, "by": {}, "and": {}, "or": {}, "but": {},
	"not": {}, "this": {}, "that": {}, "it": {}, "its": {},
}

// isCJK reports whether r is a CJK (Chinese/Japanese/Korean) character.
// isCJK 判断cjk是否可用。
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Extension B
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0x2F800 && r <= 0x2FA1F) // CJK Compatibility Supplement
}

// toHalfwidth converts a fullwidth character to its halfwidth equivalent.
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

// stripFrontmatter removes YAML frontmatter enclosed in --- delimiters.
// stripFrontmatter 处理stripfrontmatter。
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

// stripMarkdown removes common Markdown formatting characters.
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

// convertFullwidth converts fullwidth characters to their halfwidth equivalents.
func convertFullwidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(toHalfwidth(r))
	}
	return b.String()
}

// Normalize normalizes raw memory content into comparable text.
// Steps: strip frontmatter (--- ... ---) → strip Markdown formatting (#*->`) →
// fullwidth to halfwidth → remove Chinese/English stop words → collapse whitespace.
// Normalize 规范化记忆。
func Normalize(raw string) string {
	s := stripFrontmatter(raw)
	s = stripMarkdown(s)
	s = convertFullwidth(s)
	return tokenizeAndFilter(s)
}

// tokenizeAndFilter splits text into words/CJK runs, filters stop words, and reassembles.
// tokenizeAndFilter 处理tokenize过滤条件。
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

// collectCJKRun collects a CJK character run starting at index start, filtering stop words.
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

// Bigrams splits normalized text into a bigram set.
// CJK characters are processed as adjacent pairs (bigrams).
// English words (sequences of ASCII letters/digits) are kept as whole tokens.
// Returns a deduplicated map[string]struct{}.
// Bigrams 处理bigrams。
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

func isWordRune(r rune) bool {
	return (unicode.IsLetter(r) || unicode.IsDigit(r)) && !isCJK(r)
}

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

func addASCIIWord(runes []rune, start, n int, result map[string]struct{}) int {
	j := start
	for j < n && isWordRune(runes[j]) {
		j++
	}
	result[string(runes[start:j])] = struct{}{}
	return j
}

// Containment computes the containment coefficient = |A∩B| / |shorter set|.
// Returns 0 if either set is empty.
// Containment 返回两段文本的包含关系。
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

// Jaccard computes Jaccard similarity = |A∩B| / |A∪B|.
// Returns 0 if both sets are empty.
// Jaccard 处理jaccard。
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
