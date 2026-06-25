// Package dedup 见 tokenizer.go。
package dedup

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// 溢出与合并相关常量
const (
	MaxEntriesPerType       = 15   // 每种类型允许的最大条目数
	MaxEntryContentRunes    = 1500 // 合并后内容的最大 rune 数
	MinMergePairContainment = 0.40 // 触发合并的最低包含系数阈值
)

// FindMostSimilarPair 在条目列表中找出包含系数最高的一对。
// 返回下标 i、j 及分数；若最高分 < MinMergePairContainment 或条目数 < 2，返回 found=false。
func FindMostSimilarPair(entries []EntrySnapshot) (i, j int, score float64, found bool) {
	pairs := FindSimilarPairs(entries)
	if len(pairs) == 0 {
		return 0, 0, 0, false
	}
	best := pairs[0] // sorted by score descending
	return best.IdxA, best.IdxB, best.Score, true
}

// SimilarPair 描述一对包含系数较高的条目。
type SimilarPair struct {
	IdxA   int // 原始 entries 切片中的下标
	IdxB   int // 原始 entries 切片中的下标
	NameA  string
	NameB  string
	PathA  string
	PathB  string
	ScopeA string
	ScopeB string
	Score  float64 // 0~1
}

// FindSimilarPairs 返回所有包含系数 >= MinMergePairContainment 的条目对，按分数降序排列。
func FindSimilarPairs(entries []EntrySnapshot) []SimilarPair {
	var pairs []SimilarPair
	for a := 0; a < len(entries); a++ {
		bigramsA := Bigrams(Normalize(entries[a].Content))
		for b := a + 1; b < len(entries); b++ {
			if entries[a].Type != entries[b].Type {
				continue
			}
			bigramsB := Bigrams(Normalize(entries[b].Content))
			s := Containment(bigramsA, bigramsB)
			if s >= MinMergePairContainment {
				pairs = append(pairs, SimilarPair{
					IdxA:   a,
					IdxB:   b,
					NameA:  entries[a].Name,
					NameB:  entries[b].Name,
					PathA:  entries[a].Path,
					PathB:  entries[b].Path,
					ScopeA: entries[a].Scope,
					ScopeB: entries[b].Scope,
					Score:  s,
				})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Score != pairs[j].Score {
			return pairs[i].Score > pairs[j].Score
		}
		return pairSortKey(pairs[i]) < pairSortKey(pairs[j])
	})
	return pairs
}

// pairSortKey 生成用于相同分数时稳定排序的字符串键。
func pairSortKey(pair SimilarPair) string {
	return strings.Join([]string{pair.ScopeA, pair.PathA, pair.ScopeB, pair.PathB}, "\x00")
}

// TruncateOldestParagraphs 将 content 截断至不超过 maxRunes 个 rune。
// 按 "\n\n" 分段，从最旧（最前）的段落开始丢弃，至少保留最后一段。
func TruncateOldestParagraphs(content string, maxRunes int) string {
	if utf8.RuneCountInString(content) <= maxRunes {
		return content
	}

	paras := strings.Split(content, "\n\n")

	// Remove paragraphs from the front until we fit within maxRunes,
	// but always keep at least the last paragraph.
	for len(paras) > 1 {
		joined := strings.Join(paras, "\n\n")
		if utf8.RuneCountInString(joined) <= maxRunes {
			return joined
		}
		// Drop the oldest (first) paragraph
		paras = paras[1:]
	}

	// Only one paragraph remains — return it regardless of length
	return paras[0]
}
