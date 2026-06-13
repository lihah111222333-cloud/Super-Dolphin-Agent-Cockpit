package dedup

import (
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MaxEntriesPerType       = 15
	MaxEntryContentRunes    = 1500
	MinMergePairContainment = 0.40
)

// FindMostSimilarPair finds the pair of entries with the highest containment score.
// Returns the indices i, j and the containment score.
// If the highest containment < MinMergePairContainment, returns found=false.
// If entries has 0 or 1 elements, returns found=false.
// FindMostSimilarPair 找出最相似的一对条目。
func FindMostSimilarPair(entries []EntrySnapshot) (i, j int, score float64, found bool) {
	pairs := FindSimilarPairs(entries)
	if len(pairs) == 0 {
		return 0, 0, 0, false
	}
	best := pairs[0] // sorted by score descending
	return best.IdxA, best.IdxB, best.Score, true
}

// SimilarPair describes a pair of entries with high containment.
type SimilarPair struct {
	IdxA   int // index into the original entries slice
	IdxB   int // index into the original entries slice
	NameA  string
	NameB  string
	PathA  string
	PathB  string
	ScopeA string
	ScopeB string
	Score  float64 // 0~1
}

// FindSimilarPairs returns all pairs of entries whose containment score
// is at least MinMergePairContainment.  Results are sorted by score descending.
// FindSimilarPairs 查找相似条目配对。
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

func pairSortKey(pair SimilarPair) string {
	return strings.Join([]string{pair.ScopeA, pair.PathA, pair.ScopeB, pair.PathB}, "\x00")
}

// TruncateOldestParagraphs truncates content to at most maxRunes runes.
// Paragraphs are separated by "\n\n". Paragraphs are removed from the beginning
// (oldest first) until the total length is <= maxRunes.
// At least the last paragraph is always kept, even if it alone exceeds maxRunes.
// TruncateOldestParagraphs 截断oldestparagraphs。
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
