package idgen

import (
	"regexp"
	"sync"
	"testing"
)

func TestGeneratorPreservesAgentIDFormat(t *testing.T) {
	got := NewGenerator().NewAgentID()
	if !regexp.MustCompile(`^agent_[0-9]+$`).MatchString(got) {
		t.Fatalf("NewAgentID() = %q, want agent_{digits}", got)
	}
}

func TestGeneratorConcurrentIDsAreUnique(t *testing.T) {
	const count = 1000
	generator := NewGenerator()
	ids := make(chan string, count)
	var wg sync.WaitGroup
	for range count {
		wg.Go(func() { ids <- generator.NewAgentID() })
	}
	wg.Wait()
	close(ids)
	seen := make(map[string]struct{}, count)
	for id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate agent id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestGeneratorInstancesOwnIndependentState(t *testing.T) {
	first := NewGenerator()
	second := NewGenerator()
	if first == second || first.lastAgentIDValue.Load() != 0 || second.lastAgentIDValue.Load() != 0 {
		t.Fatal("NewGenerator() must return independent zero-state owners")
	}
	_ = first.NewAgentID()
	if second.lastAgentIDValue.Load() != 0 {
		t.Fatal("generator state leaked across owners")
	}
}
