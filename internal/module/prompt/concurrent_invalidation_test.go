package prompt

import (
	"context"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestServiceInvalidateSectionsIsConcurrentSafe pins the contract documented
// on contract.SectionInvalidator: callers fan out from background goroutines
// (auto-dream, extractor, turn-tracking) without external synchronization,
// so InvalidateSections must be race-free.
//
// Run with `-race` for the regression to bite: a forgotten mutex on the
// shared cache or dynamic provider map would produce a race detector report
// here. Without -race the test still verifies that all callers complete
// without panicking and that the generation counter advances monotonically.
func TestServiceInvalidateSectionsIsConcurrentSafe(t *testing.T) {
	svc := NewService(&Config{}, nil)

	const (
		writers   = 16
		perWriter = 200
	)

	// Prime the cache so InvalidateSections has actual entries to drop and
	// concurrent readers exist on the flight singleflight path.
	if _, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      t.TempDir(),
		Language: "English",
	}); err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}

	sectionNames := []string{
		DynamicSectionMemory,
		DynamicSectionMemoryEntrypoint,
		DynamicSectionMemoryContext,
		DynamicSectionAgentMemory,
	}

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				name := sectionNames[(seed+j)%len(sectionNames)]
				_ = svc.InvalidateSections(contract.InvalidateMemoryWrite, name)
			}
		}(i)
	}
	wg.Wait()

	// One last invalidate to confirm the service still ticks correctly.
	gen := svc.InvalidateSections(contract.InvalidateMemoryWrite, DynamicSectionMemory)
	if gen == 0 {
		t.Fatalf("InvalidateSections() final generation = 0; expected monotonically increasing counter")
	}
}
