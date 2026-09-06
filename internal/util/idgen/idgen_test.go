package idgen

import (
	"regexp"
	"strconv"
	"strings"
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

func TestGeneratorInstancesProduceStrictlyIncreasingIDs(t *testing.T) {
	first := NewGenerator()
	second := NewGenerator()

	firstID := first.NewAgentID()
	secondID := second.NewAgentID()
	firstValue := parseAgentIDValue(t, firstID)
	secondValue := parseAgentIDValue(t, secondID)
	if secondValue <= firstValue {
		t.Fatalf("IDs are not strictly increasing across generators: %q then %q", firstID, secondID)
	}
}

func TestGeneratorInstancesConcurrentIDsAreUnique(t *testing.T) {
	const generatorCount = 16
	const idsPerGenerator = 250
	generators := make([]*Generator, generatorCount)
	for index := range generators {
		generators[index] = NewGenerator()
	}

	ids := make(chan string, generatorCount*idsPerGenerator)
	var wg sync.WaitGroup
	for _, generator := range generators {
		for range idsPerGenerator {
			wg.Go(func() { ids <- generator.NewAgentID() })
		}
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, generatorCount*idsPerGenerator)
	for id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate agent id across generators: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func parseAgentIDValue(t *testing.T, id string) uint64 {
	t.Helper()
	value, err := strconv.ParseUint(strings.TrimPrefix(id, "agent_"), 10, 64)
	if err != nil {
		t.Fatalf("parse agent ID %q: %v", id, err)
	}
	return value
}
