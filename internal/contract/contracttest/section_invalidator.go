// Package contracttest holds testkit helpers that exercise contract-level
// invariants. They let downstream implementations of a contract opt into
// the same conformance checks the in-tree implementation uses, instead of
// each implementation re-deriving its own concurrency / monotonicity test.
package contracttest

import (
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// SectionInvalidatorConcurrent stresses a contract.SectionInvalidator with
// 16 writers × 200 invalidations to surface forgotten mutexes via -race
// and to verify the generation counter advances monotonically (final
// invalidation must return a non-zero generation).
//
// The factory function must produce a fresh, ready-to-use
// SectionInvalidator on every invocation. Implementations that need cache
// priming before invalidations have entries to drop (e.g. prompt.Service
// must run AssembleStart first) should do that priming inside factory and
// return the primed instance.
//
// Run callers with `-race` for the regression to bite: a forgotten mutex
// on the shared cache or dynamic provider map produces a race detector
// report. Without -race the test still verifies completion without panic
// plus the monotonic generation contract.
// SectionInvalidatorConcurrent 处理sectioninvalidatorconcurrent。
func SectionInvalidatorConcurrent(t *testing.T, factory func() contract.SectionInvalidator) {
	t.Helper()
	inv := factory()
	if inv == nil {
		t.Fatal("contracttest.SectionInvalidatorConcurrent: factory returned nil SectionInvalidator")
	}

	const (
		writers   = 16
		perWriter = 200
	)
	sectionNames := []string{
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryEntrypoint,
		contract.DynamicSectionMemoryContext,
	}

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				name := sectionNames[(seed+j)%len(sectionNames)]
				_ = inv.InvalidateSections(contract.InvalidateMemoryWrite, name)
			}
		}(i)
	}
	wg.Wait()

	gen := inv.InvalidateSections(contract.InvalidateMemoryWrite, contract.DynamicSectionMemory)
	if gen == 0 {
		t.Fatalf("InvalidateSections() final generation = 0; expected monotonically increasing counter")
	}
}
