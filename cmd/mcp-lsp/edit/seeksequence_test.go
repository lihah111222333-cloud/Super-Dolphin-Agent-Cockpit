package edit

import "testing"

func TestSeekSequenceModes(t *testing.T) {
	lines := []string{"alpha", "beta   ", "  gamma  ", "cafe\u0301"}

	assertSeekSequenceMatch(t, "exact", lines, "alpha", 0, seekMatchExact)
	assertSeekSequenceMatch(t, "trim_right", lines, "beta", 1, seekMatchTrimRight)
	assertSeekSequenceMatch(t, "trim_both", lines, "gamma", 2, seekMatchTrimBoth)
	assertSeekSequenceMatch(t, "unicode", lines, "caf\u00e9", 3, seekMatchUnicodeNormalized)
}

func TestSeekSequenceRejectsEmptyPattern(t *testing.T) {
	if _, _, err := SeekSequence([]string{"x"}, nil, 0); err == nil {
		t.Fatal("SeekSequence returned nil error for empty pattern")
	}
}

func assertSeekSequenceMatch(t *testing.T, label string, lines []string, pattern string, wantPos int, wantMode MatchMode) {
	t.Helper()
	pos, mode, err := SeekSequence(lines, []string{pattern}, 0)
	if err != nil || pos != wantPos || mode != wantMode {
		t.Fatalf("%s = (%d, %q, %v)", label, pos, mode, err)
	}
}
