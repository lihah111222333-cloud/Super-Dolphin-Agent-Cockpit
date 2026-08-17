//go:build darwin || linux

package processobserve_test

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
	"golang.org/x/sync/errgroup"
)

func TestDurableStoreConcurrentStoresKeepOneAcknowledgedIncident(t *testing.T) {
	root := canonicalTempRoot(t)
	left := openDurableTestStore(t, root)
	defer left.Close()
	right := openDurableTestStore(t, root)
	defer right.Close()
	snapshot := probeMustFail(t, 9_999_994)
	start := make(chan struct{})
	results := make(chan durableRecordResult, 2)
	var group errgroup.Group
	group.Go(func() error { return recordFromStore(start, left, snapshot, results) })
	group.Go(func() error { return recordFromStore(start, right, snapshot, results) })
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent worker error: %v", err)
	}
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent RecordGhost errors: first=%v second=%v", first.err, second.err)
	}
	decisions, err := left.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	assertSingleAcknowledgedIncident(t, decisions)
}

type durableRecordResult struct {
	decision processobserve.Decision
	err      error
}

func recordFromStore(start <-chan struct{}, store *processobserve.Store, snapshot processprobe.Snapshot, results chan<- durableRecordResult) error {
	<-start
	decision, err := store.RecordGhost(context.Background(), snapshot)
	results <- durableRecordResult{decision: decision, err: err}
	return nil
}

func assertSingleAcknowledgedIncident(t *testing.T, decisions []processobserve.Decision) {
	t.Helper()
	if len(decisions) != 1 {
		t.Fatalf("decisions = %#v, want one compacted incident", decisions)
	}
	decision := decisions[0]
	if decision.Status() != processobserve.DecisionPersisted {
		t.Fatalf("status = %q, want persisted", decision.Status())
	}
	if !decision.CandidateProjection().Acked() || !decision.BlockedProjection().Acked() {
		t.Fatal("concurrent write lost a projection acknowledgement")
	}
	if !sameProjectionIdentity(decision) {
		t.Fatal("candidate and blocked projections lost shared event/operation identity")
	}
}
