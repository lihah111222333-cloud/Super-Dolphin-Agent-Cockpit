//go:build darwin || linux

package processobserve_test

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

func TestDurableStoreOverflowPreservesDroppedObservationCount(t *testing.T) {
	root := canonicalTempRoot(t)
	store := openDurableTestStore(t, root)
	defer store.Close()
	snapshots := make([]processprobe.Snapshot, processobserve.MaxObservationBuckets+1)
	for index := range snapshots {
		snapshots[index] = probeMustFail(t, 2_000_000+index)
	}
	if _, err := store.RecordGhostBatch(context.Background(), snapshots); err != nil {
		t.Fatalf("RecordGhostBatch() error = %v", err)
	}
	decisions, err := store.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	assertOverflowDroppedCount(t, decisions)
}

func assertOverflowDroppedCount(t *testing.T, decisions []processobserve.Decision) {
	t.Helper()
	if len(decisions) != processobserve.MaxObservationBuckets {
		t.Fatalf("decisions = %d, want %d bounded buckets", len(decisions), processobserve.MaxObservationBuckets)
	}
	for _, decision := range decisions {
		if decision.DroppedCount() > 0 {
			return
		}
	}
	t.Fatal("overflow bucket did not preserve dropped observation count")
}
