package dedup

import (
	"strings"
	"unicode"
)

// EntrySnapshot is the dedup comparison snapshot for a memory entry.
// Fields correspond 1-to-1 with shared.MemoryFrontmatter + MemoryEntry so
// no field is lost during a merge.
type EntrySnapshot struct {
	Name        string
	Type        string // feedback / project / user / reference
	Description string
	SearchKeys  []string
	Lang        string   // preserved from the old entry on merge
	Aliases     []string // preserved from the old entry on merge
	Source      string   // "dream" / "" etc.
	Content     string   // body text (no frontmatter)
	Path        string   // full on-disk path
	Scope       string   // "private" / "team"
}

// MatchResult describes the outcome of a single duplicate-search call.
type MatchResult struct {
	Found  bool
	Target EntrySnapshot // the existing entry that was matched
	Level  string        // "name" / "search_keys" / "content"
	Score  float64       // containment or Jaccard value
}

// NormalizeName lower-cases name, strips punctuation, collapses runs of
// whitespace, and trims leading/trailing space.
// NormalizeName 规范化名称。
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

// sliceToSet converts a string slice into a set map.
func sliceToSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// FindDuplicate searches existing for the best duplicate of candidate.
//
// Matching is performed in three levels, in order:
//  1. Exact name match (after NormalizeName) — returns immediately on hit.
//  2. search_keys Jaccard >= 0.5 — only when both sides have search_keys.
//  3. Content containment >= 0.7 — highest score wins when multiple hit.
//
// Only entries with the same Type as candidate are considered. When the
// candidate has a Scope, same-scope entries are searched before cross-scope
// entries so a current-scope duplicate is not shadowed by another scope.
// FindDuplicate 查找duplicate。
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

func filterSameType(candidateType string, existing []EntrySnapshot) []EntrySnapshot {
	var result []EntrySnapshot
	for _, e := range existing {
		if e.Type == candidateType {
			result = append(result, e)
		}
	}
	return result
}

func filterSameScope(candidateScope string, sameType []EntrySnapshot) []EntrySnapshot {
	var result []EntrySnapshot
	for _, e := range sameType {
		if e.Scope == candidateScope {
			result = append(result, e)
		}
	}
	return result
}

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
