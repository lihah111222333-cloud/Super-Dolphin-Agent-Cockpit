package edit

import (
	"fmt"
	"sort"
	"strings"
)

type Match struct {
	MatchedBy           string
	ResolvedStartOffset int
	ResolvedEndOffset   int
	ResolvedLSPLine     int
	AffectedStartLine   int
	AffectedEndLine     int
	EditContext         string
}

type matchCandidate struct {
	startOffset    int
	endOffset      int
	startLine      int
	endLine        int
	startLineIndex int
	endLineIndex   int
	matchedBy      string
}

// MatchContext resolves patch hunks against content using line-sequence matching
// first, then a raw substring fallback. Later hunks are matched against the
// working content produced by earlier matches.
func MatchContext(content string, hunks []Hunk) ([]Match, error) {
	if err := GuardContentAndReplacement(content, ""); err != nil {
		return nil, err
	}
	matches := make([]Match, 0, len(hunks))
	working := content
	for idx, hunk := range hunks {
		if err := GuardContentAndReplacement(working, hunk.NewText); err != nil {
			return nil, err
		}
		candidate, err := resolveContextMatch(working, hunk)
		if err != nil {
			return nil, fmt.Errorf("hunk %d: %w", idx+1, err)
		}
		editContext, affectedStart, affectedEnd, err := BuildEditContext(working, candidate.startOffset, candidate.endOffset, hunk.NewText)
		if err != nil {
			return nil, err
		}
		matches = append(matches, Match{
			MatchedBy:           candidate.matchedBy,
			ResolvedStartOffset: candidate.startOffset,
			ResolvedEndOffset:   candidate.endOffset,
			ResolvedLSPLine:     candidate.startLine,
			AffectedStartLine:   affectedStart,
			AffectedEndLine:     affectedEnd,
			EditContext:         editContext,
		})
		working = working[:candidate.startOffset] + hunk.NewText + working[candidate.endOffset:]
	}
	return matches, nil
}

func resolveContextMatch(content string, hunk Hunk) (matchCandidate, error) {
	index, err := indexContent(content)
	if err != nil {
		return matchCandidate{}, err
	}
	anchors := resolveContextAnchorStarts(index.lines, hunk.BeforeContext)
	candidates := collectLineSequenceCandidates(index, hunk, anchors)
	candidates = filterContextCandidates(index.lines, hunk, candidates)
	if len(candidates) == 0 {
		candidates = collectRawSubstringCandidates(index, hunk)
		candidates = filterContextCandidates(index.lines, hunk, candidates)
	}
	if len(candidates) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: no candidate matched the patch context", ErrSequenceNotFound)
	}
	if len(candidates) > 1 {
		return matchCandidate{}, fmt.Errorf("%w: multiple candidates matched the patch context", ErrAmbiguousMatch)
	}
	return candidates[0], nil
}

func resolveContextAnchorStarts(lines []string, before []string) map[int]struct{} {
	if len(before) == 0 {
		return nil
	}
	anchors := make(map[int]struct{})
	for _, mode := range allSeekModes() {
		limit := len(lines) - len(before)
		if limit < 0 {
			return anchors
		}
		for idx := 0; idx <= limit; idx++ {
			if sequenceMatchAt(lines, before, idx, mode) {
				anchors[idx+len(before)] = struct{}{}
			}
		}
	}
	return anchors
}

func collectLineSequenceCandidates(index contentIndex, hunk Hunk, anchors map[int]struct{}) []matchCandidate {
	oldLines := splitPatchText(hunk.OldText)
	if len(oldLines) == 0 {
		return nil
	}
	positions, mode := collectSequenceMatches(index.lines, oldLines)
	if len(positions) == 0 {
		return nil
	}
	candidates := make([]matchCandidate, 0, len(positions))
	for _, pos := range positions {
		if anchors != nil {
			if _, ok := anchors[pos]; !ok {
				continue
			}
		}
		endIdx := pos + len(oldLines)
		candidates = append(candidates, matchCandidate{
			startOffset:    index.start[pos],
			endOffset:      index.end[endIdx-1],
			startLine:      pos + 1,
			endLine:        endIdx,
			startLineIndex: pos,
			endLineIndex:   endIdx,
			matchedBy:      string(mode),
		})
	}
	return dedupeCandidates(candidates)
}

func collectRawSubstringCandidates(index contentIndex, hunk Hunk) []matchCandidate {
	if hunk.OldText == "" {
		return nil
	}
	var candidates []matchCandidate
	for start := 0; ; {
		rel := strings.Index(index.raw[start:], hunk.OldText)
		if rel < 0 {
			break
		}
		startOffset := start + rel
		endOffset := startOffset + len(hunk.OldText)
		startLine := offsetToLineIndex(index.start, startOffset)
		endLine := offsetToEndLineIndex(index.end, endOffset)
		candidates = append(candidates, matchCandidate{
			startOffset:    startOffset,
			endOffset:      endOffset,
			startLine:      startLine + 1,
			endLine:        endLine,
			startLineIndex: startLine,
			endLineIndex:   endLine,
			matchedBy:      "substring_exact",
		})
		start = startOffset + 1
	}
	return dedupeCandidates(candidates)
}

func filterContextCandidates(lines []string, hunk Hunk, candidates []matchCandidate) []matchCandidate {
	if len(candidates) == 0 {
		return nil
	}
	filtered := make([]matchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !contextMatches(lines, hunk.BeforeContext, candidate.startLineIndex-len(hunk.BeforeContext)) {
			continue
		}
		if !contextMatches(lines, hunk.AfterContext, candidate.endLineIndex) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return dedupeCandidates(filtered)
}

func contextMatches(lines []string, context []string, start int) bool {
	if len(context) == 0 {
		return true
	}
	if start < 0 || start+len(context) > len(lines) {
		return false
	}
	for _, mode := range allSeekModes() {
		if sequenceMatchAt(lines, context, start, mode) {
			return true
		}
	}
	return false
}

func dedupeCandidates(candidates []matchCandidate) []matchCandidate {
	if len(candidates) < 2 {
		return candidates
	}
	sort.Slice(candidates, func(i int, j int) bool {
		if candidates[i].startOffset != candidates[j].startOffset {
			return candidates[i].startOffset < candidates[j].startOffset
		}
		if candidates[i].endOffset != candidates[j].endOffset {
			return candidates[i].endOffset < candidates[j].endOffset
		}
		return candidates[i].matchedBy < candidates[j].matchedBy
	})
	deduped := candidates[:1]
	for _, candidate := range candidates[1:] {
		prev := deduped[len(deduped)-1]
		if prev.startOffset == candidate.startOffset && prev.endOffset == candidate.endOffset {
			continue
		}
		deduped = append(deduped, candidate)
	}
	return deduped
}

func offsetToLineIndex(starts []int, offset int) int {
	if len(starts) == 0 {
		return 0
	}
	pos := sort.Search(len(starts), func(i int) bool {
		return starts[i] > offset
	})
	if pos == 0 {
		return 0
	}
	return pos - 1
}

func offsetToEndLineIndex(ends []int, offset int) int {
	if len(ends) == 0 {
		return 1
	}
	pos := sort.Search(len(ends), func(i int) bool {
		return ends[i] >= offset
	})
	if pos >= len(ends) {
		return len(ends)
	}
	return pos + 1
}
