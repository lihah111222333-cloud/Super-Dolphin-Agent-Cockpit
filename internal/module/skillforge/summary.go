package skillforge

import (
	"strings"
	"unicode/utf8"
)

// ExtractSummary 从 H2 段正文中抽取 1-2 句作为摘要，截到 maxRunes（按 rune 计）。
//
// 规则：
//  1. 跳过空行和以 "#" / "-" / "*" 开头的标题/列表起始行。
//  2. 第一段（连续非空非标题行）作为候选。
//  3. 用句末标点（中文 。！？ 与英文 . ! ?）切分，取第一句。
//  4. 没有句末标点时整段视为一句，并补回中文句号。
//  5. 超过 maxRunes 时按 rune 截断并加 "…"。
func ExtractSummary(body string, maxRunes int) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	first := firstParagraph(body)
	if first == "" {
		return ""
	}
	first = strings.ReplaceAll(first, "\n", " ")

	stops := []string{"。", "！", "？", ".", "!", "?"}
	cutAt := -1
	for _, s := range stops {
		if i := strings.Index(first, s); i >= 0 {
			if cutAt == -1 || i < cutAt {
				cutAt = i + len(s)
			}
		}
	}
	out := first
	if cutAt > 0 {
		out = first[:cutAt]
	} else {
		out = first + "。"
	}
	out = strings.TrimSpace(out)
	if utf8.RuneCountInString(out) > maxRunes {
		ellipsis := "…"
		ellipsisLen := utf8.RuneLen('…') // 3 bytes
		out = truncateRunes(out, maxRunes-ellipsisLen) + ellipsis
	}
	return out
}

// firstParagraph 返回 body 中第一段连续的非空非标题/列表行（合并为单行用空格连接）。
func firstParagraph(body string) string {
	var buf strings.Builder
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			if buf.Len() > 0 {
				return strings.TrimSpace(buf.String())
			}
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") {
			if buf.Len() > 0 {
				return strings.TrimSpace(buf.String())
			}
			continue
		}
		buf.WriteString(t)
		buf.WriteString(" ")
	}
	return strings.TrimSpace(buf.String())
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}
