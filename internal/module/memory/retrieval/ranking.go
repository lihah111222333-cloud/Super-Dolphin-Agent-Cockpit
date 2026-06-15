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

func normalizeSearchValues(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if canonical := CanonicalName(value); canonical != "" {
			normalized = append(normalized, canonical)
		}
	}
	return normalized
}

// matchWeight 判断weight是否匹配。
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
