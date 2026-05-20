package multilsp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBootstrapWaitReturnsErrorWhenInflightEntryExpires(t *testing.T) {
	store := newBootstrapStateStore()
	decision := store.prepare("/repo", "file:///repo/a.go", "fp1")
	entry := store.entries[bootstrapKey{workspace: "/repo", uri: "file:///repo/a.go"}]
	entry.updatedAt = time.Now().Add(-bootstrapInFlightTTL - time.Second)

	next := store.prepare("/repo", "file:///repo/a.go", "fp2")
	if next.action != bootstrapActionRun {
		t.Fatalf("next action = %v, want run", next.action)
	}
	err := store.waitFor(context.Background(), "/repo", "file:///repo/a.go", decision.wait)
	if err == nil || !strings.Contains(err.Error(), "stale bootstrap") {
		t.Fatalf("waitFor() error = %v, want stale bootstrap error", err)
	}
}

func TestBootstrapWaitReturnsErrorWhenInflightEntryIsDeleted(t *testing.T) {
	store := newBootstrapStateStore()
	decision := store.prepare("/repo", "file:///repo/a.go", "fp1")

	store.delete("/repo", "file:///repo/a.go")
	err := store.waitFor(context.Background(), "/repo", "file:///repo/a.go", decision.wait)
	if err == nil || !strings.Contains(err.Error(), "deleted bootstrap") {
		t.Fatalf("waitFor() error = %v, want deleted bootstrap error", err)
	}
}
