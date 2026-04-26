package prompt

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/contract/contracttest"
)

// TestServiceInvalidateSectionsIsConcurrentSafe pins the contract documented
// on contract.SectionInvalidator for the in-tree prompt.Service
// implementation: callers fan out from background goroutines (auto-dream,
// extractor, turn-tracking) without external synchronization, so
// InvalidateSections must be race-free and the generation counter must
// advance monotonically.
//
// The 16 writers × 200 invalidations stress loop lives in
// contracttest.SectionInvalidatorConcurrent so any future SectionInvalidator
// implementation can opt into the same conformance check.
func TestServiceInvalidateSectionsIsConcurrentSafe(t *testing.T) {
	contracttest.SectionInvalidatorConcurrent(t, func() contract.SectionInvalidator {
		svc := NewService(&Config{}, nil)
		// Prime the cache so InvalidateSections has entries to drop and
		// concurrent readers exist on the flight singleflight path.
		if _, err := svc.AssembleStart(context.Background(), StartInput{
			Provider: "claudecli",
			CWD:      t.TempDir(),
			Language: "English",
		}); err != nil {
			t.Fatalf("AssembleStart() error = %v", err)
		}
		return svc
	})
}
