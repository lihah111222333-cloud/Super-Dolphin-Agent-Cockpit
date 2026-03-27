package edit

import (
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
)

type MatchMode string

const (
	seekMatchExact             MatchMode = "exact"
	seekMatchTrimRight         MatchMode = "trim_right"
	seekMatchTrimBoth          MatchMode = "trim_both"
	seekMatchUnicodeNormalized MatchMode = "unicode_normalized"
)

// SeekSequence finds the first occurrence of pattern in lines using the 4-pass
// relaxed matching strategy mandated by the migration plan.
func SeekSequence(lines []string, pattern []string, start int) (int, MatchMode, error) {
	if len(pattern) == 0 {
		return -1, "", fmt.Errorf("%w: empty pattern", ErrSequenceNotFound)
	}
	from, to, err := seekSequenceBounds(lines, pattern, start)
	if err != nil {
		return -1, "", err
	}
	for _, mode := range allSeekModes() {
		if pos := seekSequenceMode(lines, pattern, from, to, mode); pos >= 0 {
			return pos, mode, nil
		}
	}
	return -1, "", fmt.Errorf("%w: pattern not found", ErrSequenceNotFound)
}

func seekSequenceBounds(lines []string, pattern []string, start int) (int, int, error) {
	if len(pattern) > len(lines) {
		return -1, -1, fmt.Errorf("%w: pattern is longer than input", ErrSequenceNotFound)
	}
	if start < 0 {
		start = 0
	}
	if start > len(lines)-len(pattern) {
		return -1, -1, fmt.Errorf("%w: start offset is outside the searchable range", ErrSequenceNotFound)
	}
	return start, len(lines) - len(pattern), nil
}

func seekSequenceMode(lines []string, pattern []string, start int, end int, mode MatchMode) int {
	for idx := start; idx <= end; idx++ {
		if sequenceMatchAt(lines, pattern, idx, mode) {
			return idx
		}
	}
	return -1
}

func collectSequenceMatches(lines []string, pattern []string) ([]int, MatchMode) {
	if len(pattern) == 0 || len(pattern) > len(lines) {
		return nil, ""
	}
	limit := len(lines) - len(pattern)
	for _, mode := range allSeekModes() {
		matches := make([]int, 0, 4)
		for idx := 0; idx <= limit; idx++ {
			if sequenceMatchAt(lines, pattern, idx, mode) {
				matches = append(matches, idx)
			}
		}
		if len(matches) > 0 {
			return matches, mode
		}
	}
	return nil, ""
}

func sequenceMatchAt(lines []string, pattern []string, start int, mode MatchMode) bool {
	for idx, want := range pattern {
		if !lineMatch(lines[start+idx], want, mode) {
			return false
		}
	}
	return true
}

func lineMatch(have string, want string, mode MatchMode) bool {
	switch mode {
	case seekMatchExact:
		return have == want
	case seekMatchTrimRight:
		return trimRightSpace(have) == trimRightSpace(want)
	case seekMatchTrimBoth:
		return strings.TrimSpace(have) == strings.TrimSpace(want)
	case seekMatchUnicodeNormalized:
		return normalizeUnicode(strings.TrimSpace(have)) == normalizeUnicode(strings.TrimSpace(want))
	default:
		return false
	}
}

func allSeekModes() []MatchMode {
	return []MatchMode{
		seekMatchExact,
		seekMatchTrimRight,
		seekMatchTrimBoth,
		seekMatchUnicodeNormalized,
	}
}

func trimRightSpace(value string) string {
	return strings.TrimRight(value, " \t\r\n")
}

func normalizeUnicode(value string) string {
	return norm.NFC.String(value)
}
