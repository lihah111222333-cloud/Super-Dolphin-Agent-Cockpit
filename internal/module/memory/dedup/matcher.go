// Package dedup 见 tokenizer.go。
package dedup

import (
	"strings"
	"unicode"
)

// EntrySnapshot 是记忆条目的去重比较快照，字段与 shared.MemoryFrontmatter + MemoryEntry 一一对应，
// 确保合并时不丢失任何元数据。
type EntrySnapshot struct {
	Name        string
	Type        string // feedback / project / user / reference
	Description string
	SearchKeys  []string
	Lang        string   // 合并时保留 old 的值
	Aliases     []string // 合并时保留 old 的值
	Source      string   // "dream" / "" 等
	Content     string   // 正文（不含 frontmatter）
	Path        string   // 磁盘完整路径
	Scope       string   // "private" / "team"
}

// MatchResult 描述一次重复查找的结果。
type MatchResult struct {
	Found  bool
	Target EntrySnapshot // 被匹配到的已有条目
	Level  string        // "name" / "search_keys" / "content"
	Score  float64       // containment 或 Jaccard 值
}

// NormalizeName 将名称小写，去除标点符号，合并连续空白并裁剪首尾空格。
func NormalizeName(name string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
			}
			prevSpace = true
			continue
		}
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// sliceToSet 将字符串切片转换为集合 map。
func sliceToSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// FindDuplicate 在 existing 中查找 candidate 的最佳重复项。
//
// 匹配按三个层级依次进行：
//  1. 名称精确匹配（NormalizeName 后相等）—— 命中立即返回。
//  2. search_keys Jaccard >= 0.5 —— 仅在双方均有 search_keys 时参与。
//  3. 内容 containment >= 0.7 —— 取最高分者。
//
// 仅考虑 Type 与 candidate 相同的条目。candidate 有 Scope 时，优先在同作用域内查找，
// 避免同作用域重复项被跨作用域条目遮蔽。
func FindDuplicate(candidate EntrySnapshot, existing []EntrySnapshot) MatchResult {
	sameType := filterSameType(candidate.Type, existing)
	if len(sameType) == 0 {
		return MatchResult{}
	}

	if candidate.Scope != "" {
		if r := findDuplicateInSet(candidate, filterSameScope(candidate.Scope, sameType)); r.Found {
			return r
		}
	}
	return findDuplicateInSet(candidate, sameType)
}

// filterSameType 过滤出与 candidateType 类型相同的条目。
func filterSameType(candidateType string, existing []EntrySnapshot) []EntrySnapshot {
	var result []EntrySnapshot
	for _, e := range existing {
		if e.Type == candidateType {
			result = append(result, e)
		}
	}
	return result
}

// filterSameScope 过滤出与 candidateScope 作用域相同的条目。
func filterSameScope(candidateScope string, sameType []EntrySnapshot) []EntrySnapshot {
	var result []EntrySnapshot
	for _, e := range sameType {
		if e.Scope == candidateScope {
			result = append(result, e)
		}
	}
	return result
}

// findDuplicateInSet 在给定集合内依次按名称、search_keys、内容查找重复项。
func findDuplicateInSet(candidate EntrySnapshot, sameType []EntrySnapshot) MatchResult {
	if len(sameType) == 0 {
		return MatchResult{}
	}
	if r := matchByName(candidate.Name, sameType); r.Found {
		return r
	}
	if r := matchBySearchKeys(candidate.SearchKeys, sameType); r.Found {
		return r
	}
	return matchByContent(candidate.Content, sameType)
}

// matchByName 按归一化名称精确匹配，找到即返回，未找到返回空结果。
func matchByName(candidateName string, sameType []EntrySnapshot) MatchResult {
	candNorm := NormalizeName(candidateName)
	if candNorm == "" {
		return MatchResult{}
	}
	for _, e := range sameType {
		existingNorm := NormalizeName(e.Name)
		if existingNorm != "" && existingNorm == candNorm {
			return MatchResult{Found: true, Target: e, Level: "name", Score: 1.0}
		}
	}
	return MatchResult{}
}

// matchBySearchKeys 按 search_keys 的 Jaccard 相似度（>= 0.5）匹配，未找到返回空结果。
func matchBySearchKeys(candidateKeys []string, sameType []EntrySnapshot) MatchResult {
	if len(candidateKeys) == 0 {
		return MatchResult{}
	}
	candSet := sliceToSet(candidateKeys)
	for _, e := range sameType {
		if len(e.SearchKeys) == 0 {
			continue
		}
		score := Jaccard(candSet, sliceToSet(e.SearchKeys))
		if score >= 0.5 {
			return MatchResult{Found: true, Target: e, Level: "search_keys", Score: score}
		}
	}
	return MatchResult{}
}

// matchByContent 按内容 bigram containment（>= 0.7）匹配，返回得分最高的条目。
func matchByContent(candidateContent string, sameType []EntrySnapshot) MatchResult {
	candBigrams := Bigrams(Normalize(candidateContent))
	var best MatchResult
	for _, e := range sameType {
		score := Containment(candBigrams, Bigrams(Normalize(e.Content)))
		if score >= 0.7 && score > best.Score {
			best = MatchResult{Found: true, Target: e, Level: "content", Score: score}
		}
	}
	return best
}
