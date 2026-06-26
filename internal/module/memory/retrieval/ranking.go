package retrieval

import (
	"path/filepath"
	"sort"
	"strings"
)

type scoredMemoryEntry struct {
	entry MemoryEntry
	score int
}

type memorySearchFields struct {
	name        []string
	base        []string
	description []string
	path        []string
	aliases     []string
	searchKeys  []string
}

// scoreMemoryEntry 根据查询和分词结果计算单条记忆的相关性分数。
// 名称、文件名和搜索键权重高于描述和路径，鼓励命中语义标题的条目排在前面。
func scoreMemoryEntry(query string, terms []string, entry MemoryEntry) int {
	fields := searchableFields(entry)
	score := matchWeight(fields.name, query, 18) +
		matchWeight(fields.base, query, 16) +
		matchWeight(fields.aliases, query, 12) +
		matchWeight(fields.searchKeys, query, 12) +
		matchWeight(fields.description, query, 8) +
		matchWeight(fields.path, query, 6)
	for _, term := range terms {
		score += matchWeight(fields.name, term, 8) +
			matchWeight(fields.base, term, 7) +
			matchWeight(fields.aliases, term, 6) +
			matchWeight(fields.searchKeys, term, 6) +
			matchWeight(fields.description, term, 4) +
			matchWeight(fields.path, term, 3)
	}
	if score == 0 {
		return 0
	}
	return score + matchedTermCount(fields, terms)*5
}

// searchableFields 提取用于相关性打分的规范化字段。
// 所有字段都使用 CanonicalName，保证大小写、标点和空白不会影响匹配。
func searchableFields(entry MemoryEntry) memorySearchFields {
	return memorySearchFields{
		name:        []string{CanonicalName(entry.Frontmatter.Name)},
		base:        []string{CanonicalName(strings.TrimSuffix(filepath.Base(entry.FilePath), filepath.Ext(entry.FilePath)))},
		description: []string{CanonicalName(entry.Frontmatter.Description)},
		path:        []string{CanonicalName(filepath.ToSlash(entry.FilePath))},
		aliases:     normalizeSearchValues(entry.Frontmatter.Aliases),
		searchKeys:  normalizeSearchValues(entry.Frontmatter.SearchKeys),
	}
}

// matchedTermCount 统计查询分词命中的数量。
// 多个 term 命中的记忆会获得额外加分，减少单一弱命中的误选。
func matchedTermCount(fields memorySearchFields, terms []string) int {
	count := 0
	flat := flattenSearchFields(fields)
	for _, term := range terms {
		if matchWeight(flat, term, 1) > 0 {
			count++
		}
	}
	return count
}

// normalizeSearchValues 将 aliases/search_keys 规整成可匹配字段。
// 空值会被过滤，避免给 matchWeight 制造无效候选。
func normalizeSearchValues(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if canonical := CanonicalName(value); canonical != "" {
			normalized = append(normalized, canonical)
		}
	}
	return normalized
}

// matchWeight 在字段集合中找到 needle 时返回权重。
// 空查询或非正权重直接返回 0，防止调用方把无效条件计入得分。
func matchWeight(fields []string, needle string, weight int) int {
	if needle == "" || weight <= 0 {
		return 0
	}
	for _, field := range fields {
		if field != "" && strings.Contains(field, needle) {
			return weight
		}
	}
	return 0
}

// flattenSearchFields 将各字段组展开为统一列表。
// matchedTermCount 使用它统计 term 覆盖面。
func flattenSearchFields(fields memorySearchFields) []string {
	flattened := make([]string, 0, 4+len(fields.aliases)+len(fields.searchKeys))
	flattened = append(flattened, fields.name...)
	flattened = append(flattened, fields.base...)
	flattened = append(flattened, fields.description...)
	flattened = append(flattened, fields.path...)
	flattened = append(flattened, fields.aliases...)
	flattened = append(flattened, fields.searchKeys...)
	return flattened
}

func sortScoredMemories(entries []scoredMemoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		if !entries[i].entry.UpdatedAt.Equal(entries[j].entry.UpdatedAt) {
			return entries[i].entry.UpdatedAt.After(entries[j].entry.UpdatedAt)
		}
		return entries[i].entry.FilePath < entries[j].entry.FilePath
	})
}

func searchTerms(query string) (string, []string) {
	normalized := CanonicalName(query)
	if normalized == "" {
		return "", nil
	}
	seen := map[string]struct{}{normalized: {}}
	terms := []string{normalized}
	for _, part := range strings.Fields(normalized) {
		if _, ok := seen[part]; ok || part == "" {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	return normalized, terms
}
