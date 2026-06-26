// Package dedup 实现 durable memory 写入前的重复检测、合并和溢出处理。
package dedup

import (
	"strings"
)

// Decision 是重复检测后的写入动作。
// 调用方据此决定新建、跳过或覆盖既有记忆文件。
type Decision int

const (
	WriteNew Decision = iota // 无重复，正常写入
	Skip                     // 重复且无新增价值，跳过
	Merge                    // 重复但有新增内容，合并
)

// Decide 给定新旧条目的归一化 bigram 集合，返回决策。
//
//	完全相同 → Skip
//	新 bigram 90%+ 在旧里 → Skip
//	新独特 bigram < 15% of 新总量 → Skip
//	否则 → Merge
func Decide(oldBigrams, newBigrams map[string]struct{}) Decision {
	if len(newBigrams) == 0 && len(oldBigrams) == 0 {
		return Skip
	}
	if len(newBigrams) == 0 {
		return Skip
	}

	// 只计算新内容中已有 bigram 的覆盖率，避免旧内容长度影响新增价值判断。
	overlapCount := 0
	for bg := range newBigrams {
		if _, ok := oldBigrams[bg]; ok {
			overlapCount++
		}
	}

	total := len(newBigrams)

	// 新 bigram 90%+ 在旧里 → Skip
	if float64(overlapCount)/float64(total) >= 0.90 {
		return Skip
	}

	// 新独特 bigram < 15% of 新总量 → Skip
	novelCount := total - overlapCount
	if float64(novelCount)/float64(total) < 0.15 {
		return Skip
	}

	return Merge
}

// MergeRulePoints 合并偏好类记忆（规则点 diff）。
// 将 old 和 new 的内容按行拆分，找出 new 中不在 old 里的行（bigram containment < 0.7），追加到 old 末尾。
func MergeRulePoints(oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var novelLines []string
	for _, nl := range newLines {
		if strings.TrimSpace(nl) == "" {
			continue
		}
		nlBigrams := Bigrams(Normalize(nl))
		isNovel := true
		for _, ol := range oldLines {
			if strings.TrimSpace(ol) == "" {
				continue
			}
			olBigrams := Bigrams(Normalize(ol))
			if Containment(nlBigrams, olBigrams) >= 0.7 {
				isNovel = false
				break
			}
		}
		if isNovel {
			novelLines = append(novelLines, nl)
		}
	}

	if len(novelLines) == 0 {
		return oldContent
	}

	result := oldContent
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	result += strings.Join(novelLines, "\n")
	return result
}

// MergeParagraphs 合并项目类记忆（段落级去重）。
// 按空行拆分段落，new 段落与所有 old 段落的 containment < 0.5 → 追加；≥ 0.5 且更长 → 替换；否则保留旧段落。
func MergeParagraphs(oldContent, newContent string) string {
	oldParas := splitParagraphs(oldContent)
	newParas := splitParagraphs(newContent)

	// Work with a mutable copy of old paragraphs.
	result := make([]string, len(oldParas))
	copy(result, oldParas)

	var appendParas []string

	for _, np := range newParas {
		if strings.TrimSpace(np) == "" {
			continue
		}
		npBigrams := Bigrams(Normalize(np))

		maxContainment := 0.0
		maxIdx := -1
		for i, op := range result {
			if strings.TrimSpace(op) == "" {
				continue
			}
			opBigrams := Bigrams(Normalize(op))
			c := Containment(npBigrams, opBigrams)
			if c > maxContainment {
				maxContainment = c
				maxIdx = i
			}
		}

		if maxContainment < 0.5 {
			// Append the new paragraph.
			appendParas = append(appendParas, np)
		} else if maxIdx >= 0 && len([]rune(np)) > len([]rune(result[maxIdx])) {
			// Replace old paragraph with the longer new one.
			result[maxIdx] = np
		}
		// else: keep old paragraph (do nothing)
	}

	result = append(result, appendParas...)
	return strings.Join(result, "\n\n")
}

// splitParagraphs 按双换行拆分段落，过滤纯空白段落。
func splitParagraphs(content string) []string {
	parts := strings.Split(content, "\n\n")
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// MergeContent 根据记忆类型选择合并策略。
//
//	type 含 "feedback"/"user" → MergeRulePoints（行级追加）
//	其他 → MergeParagraphs（段落级去重）
func MergeContent(memType, oldContent, newContent string) string {
	lower := strings.ToLower(memType)
	if strings.Contains(lower, "feedback") || strings.Contains(lower, "user") {
		return MergeRulePoints(oldContent, newContent)
	}
	return MergeParagraphs(oldContent, newContent)
}

// MergeFrontmatter 合并两个快照的 frontmatter 字段，返回完整的 EntrySnapshot（不改 Content）。
//
//	name/type/lang/aliases/path/scope：保留 old
//	description：取更长的
//	search_keys：并集去重
//	source：old 为 "dream" 时取 new，否则保留 old
func MergeFrontmatter(old, new EntrySnapshot) EntrySnapshot {
	result := old // start with a copy of old

	// description: take the longer one
	if len(new.Description) > len(old.Description) {
		result.Description = new.Description
	}

	// search_keys: union, deduplicated
	result.SearchKeys = unionStrings(old.SearchKeys, new.SearchKeys)

	// source: if old is "dream", use new; otherwise keep old
	if old.Source == "dream" {
		result.Source = new.Source
	}

	// Name, Type, Lang, Aliases, Path, Scope: already preserved from old copy
	return result
}

// unionStrings 合并两个字符串切片并去重，保持原有顺序。
func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	var out []string
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
