package dedup

import (
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
func FindMostSimilarPair(entries []EntrySnapshot) (i, j int, score float64, found bool) {
	if len(entries) < 2 {
		return 0, 0, 0, false
	}

	bestI, bestJ := 0, 1
	bestScore := -1.0

	for a := 0; a < len(entries); a++ {
		bigramsA := Bigrams(Normalize(entries[a].Content))
		for b := a + 1; b < len(entries); b++ {
			bigramsB := Bigrams(Normalize(entries[b].Content))
			s := Containment(bigramsA, bigramsB)
			if s > bestScore {
				bestScore = s
				bestI = a
				bestJ = b
			}
		}
	}

	if bestScore < MinMergePairContainment {
		return 0, 0, 0, false
	}
	return bestI, bestJ, bestScore, true
}

// TruncateOldestParagraphs truncates content to at most maxRunes runes.
// Paragraphs are separated by "\n\n". Paragraphs are removed from the beginning
// (oldest first) until the total length is <= maxRunes.
// At least the last paragraph is always kept, even if it alone exceeds maxRunes.
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
