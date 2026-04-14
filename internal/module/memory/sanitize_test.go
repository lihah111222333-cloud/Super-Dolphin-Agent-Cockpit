package memory

import (
	"strings"
	"testing"
)

func TestSanitizeAgentTypePreservesReadableNames(t *testing.T) {
	const raw = "Writer Agent"
	if got := SanitizeAgentType(raw); got != raw {
		t.Fatalf("SanitizeAgentType(%q) = %q, want %q", raw, got, raw)
	}
}

func TestSanitizeAgentTypeNormalizesNFCAndColon(t *testing.T) {
	raw := "Cafe\u0301:Lead"
	want := "Café-Lead"
	if got := SanitizeAgentType(raw); got != want {
		t.Fatalf("SanitizeAgentType(%q) = %q, want %q", raw, got, want)
	}
}

func TestSanitizeAgentTypeFallsBackForDangerousCharacters(t *testing.T) {
	got := SanitizeAgentType("Writer/Alpha?")
	if got == "" {
		t.Fatal("SanitizeAgentType returned empty fallback")
	}
	if !strings.HasPrefix(got, "Writer-Alpha-") {
		t.Fatalf("SanitizeAgentType fallback = %q, want readable prefix", got)
	}
}

func TestSanitizeAgentTypeRejectsTraversalAndEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", ".", "..", "../writer", "..\\writer"} {
		if got := SanitizeAgentType(raw); got != "" {
			t.Fatalf("SanitizeAgentType(%q) = %q, want empty rejection", raw, got)
		}
	}
}

func TestSanitizeAgentTypeRejectsTooLongInput(t *testing.T) {
	raw := strings.Repeat("A", agentTypeMaxLen+1)
	if got := SanitizeAgentType(raw); got != "" {
		t.Fatalf("SanitizeAgentType(long) = %q, want empty rejection", got)
	}
}
