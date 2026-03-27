package edit

import "testing"

func TestSeekSequenceModes(t *testing.T) {
	lines := []string{"alpha", "beta   ", "  gamma  ", "cafe\u0301"}

	if pos, mode, err := SeekSequence(lines, []string{"alpha"}, 0); err != nil || pos != 0 || mode != seekMatchExact {
		t.Fatalf("exact = (%d, %q, %v)", pos, mode, err)
	}
	if pos, mode, err := SeekSequence(lines, []string{"beta"}, 0); err != nil || pos != 1 || mode != seekMatchTrimRight {
		t.Fatalf("trim_right = (%d, %q, %v)", pos, mode, err)
	}
	if pos, mode, err := SeekSequence(lines, []string{"gamma"}, 0); err != nil || pos != 2 || mode != seekMatchTrimBoth {
		t.Fatalf("trim_both = (%d, %q, %v)", pos, mode, err)
	}
	if pos, mode, err := SeekSequence(lines, []string{"caf\u00e9"}, 0); err != nil || pos != 3 || mode != seekMatchUnicodeNormalized {
		t.Fatalf("unicode = (%d, %q, %v)", pos, mode, err)
	}
}

func TestSeekSequenceRejectsEmptyPattern(t *testing.T) {
	if _, _, err := SeekSequence([]string{"x"}, nil, 0); err == nil {
		t.Fatal("SeekSequence returned nil error for empty pattern")
	}
}
