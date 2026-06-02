package tools

import "testing"

func assertEnvelopeCounts(t *testing.T, label string, dataLen, total, showing int, truncated bool, hint string) {
	t.Helper()
	if dataLen != 1 {
		t.Fatalf("%s data len = %d, want 1", label, dataLen)
	}
	if total != 1 {
		t.Fatalf("%s total = %d, want 1", label, total)
	}
	if showing != 1 {
		t.Fatalf("%s showing = %d, want 1", label, showing)
	}
	if truncated {
		t.Fatalf("%s truncated = true, want false", label)
	}
	if hint == "" {
		t.Fatalf("%s hint is empty", label)
	}
}
