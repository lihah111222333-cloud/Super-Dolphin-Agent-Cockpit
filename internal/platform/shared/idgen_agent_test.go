package shared

import (
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
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
	a := NewAgentID()
	b := NewAgentID()
	if b == a {
		t.Fatalf("NewAgentID() returned duplicate %q", a)
	}
}

func TestNewAgentID_ConcurrentUniqueness(t *testing.T) {
	const n = 1000
	start := make(chan struct{})
	ids := make(chan string, n)
	var wg sync.WaitGroup
	workersDone := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-workersDone:
		case <-time.After(time.Second):
			t.Fatal("agent id goroutines did not stop")
		}
	})
	for range n {
		wg.Go(func() {
			<-start
			ids <- NewAgentID()
		})
	}
	close(start)
	wg.Wait()
	close(workersDone)
	close(ids)

	seen := make(map[string]struct{}, n)
	for id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("NewAgentID() returned duplicate %q under concurrent load", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("unique id count = %d, want %d", len(seen), n)
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
