package classifier

import (
	"sort"
	"strings"
	"unicode"
)

// PruneCandidates trims the candidate pool down to `max` entries by tag-based
// keyword overlap with the user's input. The motivation is cost + latency:
// claude haiku at ~5-15s is dominated by CLI cold-start plus token count; a
// smaller candidate list cuts both.
//
// Scoring: for each candidate, count how many of its `Tags` appear as a
// substring (case-insensitive) of the user input. Candidates with higher
// overlap rank first. Ties break by original order so enabled-row priority
// from the store stays stable.
//
// When `max <= 0` or `len(candidates) <= max`, the input is returned
// unchanged (the caller can treat prune as a no-op in that case).
//
// No candidate is *excluded* when its score is zero; we still ship the
// top-`max` by rank so the classifier always sees something even for vague
// inputs (e.g. "hi"), falling back to the LLM's own intuition rather than
// hard-cutting to an empty set.
func PruneCandidates(candidates []Candidate, userInput string, max int) []Candidate {
	if max <= 0 || len(candidates) <= max {
		return candidates
	}
	normalized := normalizeForMatch(userInput)
	type scored struct {
		cand  Candidate
		score int
		order int
	}
	ranked := make([]scored, 0, len(candidates))
	for i, c := range candidates {
		ranked = append(ranked, scored{
			cand:  c,
			score: tagOverlapScore(c.Tags, normalized),
			order: i,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].order < ranked[j].order
	})
	out := make([]Candidate, 0, max)
	for _, r := range ranked[:max] {
		out = append(out, r.cand)
	}
	return out
}

// tagOverlapScore counts how many non-empty tags appear as substrings in the
// pre-normalized user input. CJK text has no space tokenization in the stdlib;
// substring matching is the simplest signal that works for both Chinese tag
// phrases ("写 SQL") and English ones ("write a query").
func tagOverlapScore(tags []string, normalizedInput string) int {
	if normalizedInput == "" || len(tags) == 0 {
		return 0
	}
	score := 0
	for _, raw := range tags {
		tag := normalizeForMatch(raw)
		if tag == "" {
			continue
		}
		if strings.Contains(normalizedInput, tag) {
			score++
		}
	}
	return score
}

// FastPath tries to avoid the 5-15s claude -p round trip when the user's
// message has a clear, high-confidence tag overlap with exactly one
// candidate. The decision is intentionally conservative:
//
//   - top candidate must score >= minScore (default 2) non-zero tag matches,
//   - top score must exceed the runner-up by >= minGap (default 1).
//
// Both thresholds are deliberately harsher than "something matched": the
// original prompt-routing layer was removed from the project precisely
// because a loose keyword match could silently shadow a user's intent, and
// we don't want to reintroduce that. Anything below confidence falls
// through to the LLM so haiku makes the judgement call. Candidates with no
// tags (e.g. main/default) can never beat tagged rows here, which is why
// this path never routes to the default persona.
func FastPath(candidates []Candidate, userInput string) FastPathDecision {
	if len(candidates) == 0 {
		return FastPathDecision{}
	}
	return fastPathWithThresholds(candidates, userInput, 2, 1)
}

// fastPathWithThresholds is the tuning-exposed variant; production callers
// use FastPath which pins the defaults. Tests drive this directly so the
// threshold story stays auditable.
func fastPathWithThresholds(candidates []Candidate, userInput string, minScore, minGap int) FastPathDecision {
	normalized := normalizeForMatch(userInput)
	if normalized == "" {
		return FastPathDecision{}
	}
	topIdx := -1
	topScore := 0
	secondScore := 0
	for i, c := range candidates {
		s := tagOverlapScore(c.Tags, normalized)
		switch {
		case s > topScore:
			secondScore = topScore
			topScore = s
			topIdx = i
		case s > secondScore:
			secondScore = s
		}
	}
	if topIdx < 0 || topScore < minScore || topScore-secondScore < minGap {
		return FastPathDecision{Score: topScore, Gap: topScore - secondScore}
	}
	return FastPathDecision{
		Picked: candidates[topIdx],
		Hit:    true,
		Score:  topScore,
		Gap:    topScore - secondScore,
	}
}

// normalizeForMatch lowercases and collapses whitespace so case-insensitive
// substring matching works across ASCII and CJK without pulling in a real
// tokenizer. It intentionally keeps punctuation because trigger tags often
// include things like "JOIN 查询" and we want both sides normalized the same.
func normalizeForMatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimSpace(b.String())
}
