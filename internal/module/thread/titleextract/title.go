// Package titleextract 从用户首条消息中提取简洁的会话标题，用于线程命名。
package titleextract

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var mentionRe = regexp.MustCompile(`^@[^，,、\s]+[，,、\s]+`)
var fencedCodeBlockRe = regexp.MustCompile("(?s)```[^`]*```")
var backtickStripRe = regexp.MustCompile("`[^`]*`")
var sentenceSplitRe = regexp.MustCompile(`[。？！?!,，\n]`)
var continuationRe = regexp.MustCompile(`^(.+) \(续(?: (\d+))?\)$`)

var fillerPrefixes = []string{
	"能不能帮我", "能不能", "帮我一下", "帮我", "请帮我", "请",
	"你能", "你帮我", "你", "看看", "我想", "我要",
	"给我",
}

var chineseFillerWords = []string{
	"一下", "的", "了", "吧", "啊", "呢", "嘛", "哦", "嗯", "吗",
}

var chinesePronouns = map[string]bool{
	"这":  true,
	"这个": true,
	"那":  true,
	"那个": true,
	"它":  true,
	"他":  true,
	"她":  true,
	"我":  true,
	"你":  true,
	"这些": true,
	"那些": true,
}

var englishFillerWords = map[string]bool{
	"the": true, "a": true, "an": true, "in": true,
	"at": true, "on": true, "of": true, "to": true,
	"for": true, "and": true, "or": true, "is": true,
	"are": true, "was": true, "were": true, "it": true,
	"this": true, "that": true, "with": true,
}

// Extract 从 prompt 文本中提取简洁标题，清理前缀填充词、代码块和标点，返回空字符串表示无法提取有效标题。
func Extract(prompt string) string {
	if prompt == "" {
		return ""
	}
	prompt = mentionRe.ReplaceAllString(strings.TrimSpace(prompt), "")
	prompt = strings.TrimSpace(removeFiller(prompt))
	prompt = fencedCodeBlockRe.ReplaceAllString(prompt, "")
	prompt = backtickStripRe.ReplaceAllString(prompt, "")
	prompt = strings.TrimSpace(prompt)

	sentence := firstSentence(prompt)
	if sentence == "" {
		return ""
	}
	sentence = removeChineseParticles(sentence)
	if !containsChinese(sentence) {
		sentence = removeEnglishFillers(sentence)
	}
	sentence = strings.TrimSpace(sentence)
	if sentence == "" {
		return ""
	}
	sentence = truncateToUnits(sentence, 8)
	if CountDisplayUnits(sentence) <= 2 || isAllPronouns(sentence) {
		return ""
	}
	return sentence
}

// ContinuationName 根据父线程名生成续集名称，如 "标题 (续)" 或 "标题 (续 2)"，支持多级续集编号递增。
func ContinuationName(parentName string) string {
	if m := continuationRe.FindStringSubmatch(parentName); m != nil {
		base := m[1]
		if m[2] == "" {
			return fmt.Sprintf("%s (续 2)", base)
		}
		n := 0
		_, _ = fmt.Sscanf(m[2], "%d", &n)
		return fmt.Sprintf("%s (续 %d)", base, n+1)
	}
	return fmt.Sprintf("%s (续)", parentName)
}

func firstSentence(prompt string) string {
	for _, part := range sentenceSplitRe.Split(prompt, -1) {
		if sentence := strings.TrimSpace(part); sentence != "" {
			return sentence
		}
	}
	return ""
}

func removeFiller(s string) string {
	for _, prefix := range fillerPrefixes {
		if strings.HasPrefix(s, prefix) {
			return removeFiller(strings.TrimSpace(strings.TrimPrefix(s, prefix)))
		}
	}
	return s
}

func removeChineseParticles(s string) string {
	for _, p := range chineseFillerWords {
		s = strings.ReplaceAll(s, p, "")
	}
	return s
}

func removeEnglishFillers(s string) string {
	tokens := strings.Fields(s)
	out := tokens[:0]
	for _, t := range tokens {
		if !englishFillerWords[strings.ToLower(t)] {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}

func containsChinese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han) {
			return true
		}
	}
	return false
}

// CountDisplayUnits 统计字符串的显示单元数：每个汉字计 1 个单元，连续英文单词计 1 个单元。
func CountDisplayUnits(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	units := 0
	inEnglishWord := false
	for _, r := range s {
		if unicode.In(r, unicode.Han) {
			if inEnglishWord {
				units++
				inEnglishWord = false
			}
			units++
		} else if unicode.IsSpace(r) {
			if inEnglishWord {
				units++
				inEnglishWord = false
			}
		} else {
			inEnglishWord = true
		}
	}
	if inEnglishWord {
		units++
	}
	return units
}

type runeKind int

const (
	runeKindCJK runeKind = iota
	runeKindSpace
	runeKindOther
)

func classifyRune(r rune) runeKind {
	if unicode.In(r, unicode.Han) {
		return runeKindCJK
	}
	if unicode.IsSpace(r) {
		return runeKindSpace
	}
	return runeKindOther
}

// truncateToUnits 把字符串截断到不超过 maxUnits 个显示单元，保留完整词边界。
func truncateToUnits(s string, maxUnits int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	units := 0
	inWord := false
	i := 0
	for _, r := range s {
		runeLen := len(string(r))
		kind := classifyRune(r)
		if inWord && kind != runeKindOther {
			units++
			inWord = false
			if units >= maxUnits {
				return strings.TrimSpace(s[:i])
			}
		}
		switch kind {
		case runeKindCJK:
			units++
			if units >= maxUnits {
				return strings.TrimSpace(s[:i+runeLen])
			}
		case runeKindOther:
			inWord = true
		}
		i += runeLen
	}
	return strings.TrimSpace(s)
}

func isAllPronouns(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	tokens := strings.Fields(s)
	if len(tokens) == 0 {
		return false
	}
	for _, t := range tokens {
		if !chinesePronouns[t] {
			return false
		}
	}
	return true
}
