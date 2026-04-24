package shared

import (
	"regexp"
	"strings"
	"testing"
)

func TestNewAgentID_Format(t *testing.T) {
	id := NewAgentID()
	// Must match agent_{digits} with no trailing random hex.
	matched, err := regexp.MatchString(`^agent_\d+$`, id)
	if err != nil {
		t.Fatalf("regexp error: %v", err)
	}
	if !matched {
		t.Fatalf("NewAgentID() = %q, want format agent_{digits}", id)
	}
}

func TestNewAgentID_NoRandomSuffix(t *testing.T) {
	id := NewAgentID()
	parts := strings.SplitN(id, "_", 2)
	if len(parts) != 2 {
		t.Fatalf("NewAgentID() = %q, expected exactly one underscore after prefix", id)
	}
	// The timestamp part must not contain underscores (no random hex appended).
	if strings.Contains(parts[1], "_") {
		t.Fatalf("NewAgentID() = %q, timestamp part contains unexpected underscore", id)
	}
}

func TestNewAgentID_Uniqueness(t *testing.T) {
	// Two calls separated by at least 1ms should differ.
	a := NewAgentID()
	// Busy-wait to ensure clock advances.
	for {
		b := NewAgentID()
		if b != a {
			return // good — they differ
		}
	}
}

func TestNewChildAgentID_Format(t *testing.T) {
	tests := []struct {
		parent string
		seq    int
		want   string
	}{
		{"agent_1777009467426", 1, "agent_1777009467426-1"},
		{"agent_1777009467426", 100, "agent_1777009467426-100"},
		{"agent_1777009467426-3", 2, "agent_1777009467426-3-2"},
	}
	for _, tc := range tests {
		got := NewChildAgentID(tc.parent, tc.seq)
		if got != tc.want {
			t.Errorf("NewChildAgentID(%q, %d) = %q, want %q", tc.parent, tc.seq, got, tc.want)
		}
	}
}
