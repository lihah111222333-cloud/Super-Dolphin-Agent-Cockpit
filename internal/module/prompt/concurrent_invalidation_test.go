package prompt

import (
	"context"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract/contracttest"
)

func registerPromptGoroutineCleanup(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s goroutines did not stop", label)
		}
	})
}

// TestServiceInvalidateSectionsIsConcurrentSafe pins the contract documented
// on contract.SectionInvalidator for the in-tree prompt.Service
// implementation: callers fan out from background goroutines without external
// synchronization, so InvalidateSections must be race-free and the generation
// counter must advance monotonically.
func TestServiceInvalidateSectionsIsConcurrentSafe(t *testing.T) {
	contracttest.SectionInvalidatorConcurrent(t, func() contract.SectionInvalidator {
		svc := NewService(&Config{}, nil)
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
