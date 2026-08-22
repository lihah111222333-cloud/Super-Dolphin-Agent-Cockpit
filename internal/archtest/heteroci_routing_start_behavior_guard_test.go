package archtest

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// TestHeterociRoutingStartRejectsProbeBootDriftWithoutMutation proves that a
// fresh candidate snapshot cannot replace the stored local boot identity.
func TestHeterociRoutingStartRejectsProbeBootDriftWithoutMutation(t *testing.T) {
	fixture, assignment, book := prepareHeterociRoutingStartBehavior(t, "r10b-boot-drift")
	candidate := cloneHeterociRoutingStartSnapshot(fixture.snapshot)
	const sentinel = "r10b-boot-drift-sentinel"
	candidate.Nodes[0].ProbeBootID = sentinel
	beforeStart := cloneHeterociRoutingStartSnapshot(candidate)
	if err := candidate.ValidateAgainst(fixture.manifest, fixture.now, fixture.heartbeatTTL); err != nil {
		t.Fatalf("ProbeBootID-drift candidate ValidateAgainst() = %v", err)
	}
	beforeBook := snapshotHeterociRoutingStartBook(t, book, "r10b-boot-drift")
	_, err := book.Start("r10b-boot-drift", assignment.UnitID, candidate, fixture.now.Add(time.Second))
	assertHeterociRoutingStartRejectsWithoutBootSentinel(t, err, sentinel)
	assertHeterociRoutingStartBookUnchanged(t, book, "r10b-boot-drift", beforeBook)
	if !reflect.DeepEqual(candidate, beforeStart) {
		t.Fatal("ProbeBootID-drift candidate snapshot was mutated")
	}
}

// TestHeterociRoutingStartRejectsStaleHeartbeatWithoutMutation proves that a
// stale candidate is rejected before any stored lease transition occurs.
func TestHeterociRoutingStartRejectsStaleHeartbeatWithoutMutation(t *testing.T) {
	fixture, assignment, book := prepareHeterociRoutingStartBehavior(t, "r10b-stale-heartbeat")
	candidate := cloneHeterociRoutingStartSnapshot(fixture.snapshot)
	const sentinel = "r10b-stale-heartbeat-sentinel"
	candidate.Nodes[0].ProbeBootID = sentinel
	candidate.Nodes[0].HeartbeatUnixMilli = fixture.now.Add(-fixture.heartbeatTTL - time.Millisecond).UnixMilli()
	beforeStart := cloneHeterociRoutingStartSnapshot(candidate)
	validationErr := candidate.ValidateAgainst(fixture.manifest, fixture.now, fixture.heartbeatTTL)
	if validationErr == nil {
		t.Fatal("stale-heartbeat candidate ValidateAgainst() unexpectedly succeeded")
	}
	if strings.Contains(validationErr.Error(), sentinel) {
		t.Fatalf("stale-heartbeat validation leaked boot sentinel %q: %v", sentinel, validationErr)
	}
	beforeBook := snapshotHeterociRoutingStartBook(t, book, "r10b-stale-heartbeat")
	_, err := book.Start("r10b-stale-heartbeat", assignment.UnitID, candidate, fixture.now.Add(time.Second))
	assertHeterociRoutingStartRejectsWithoutBootSentinel(t, err, sentinel)
	assertHeterociRoutingStartBookUnchanged(t, book, "r10b-stale-heartbeat", beforeBook)
	if !reflect.DeepEqual(candidate, beforeStart) {
		t.Fatal("stale-heartbeat candidate snapshot was mutated")
	}
}

func prepareHeterociRoutingStartBehavior(t *testing.T, runID string) (heterociRoutingLeaseFixture, heteroci.RouteAssignment, *heteroci.RoutingLeaseBook) {
	t.Helper()
	fixture := newHeterociRoutingLeaseFixture(t)
	assignment := assertHeterociRoutingDecision(t, fixture, heteroci.ExecutorKindLocalNode)
	book := heteroci.NewRoutingLeaseBook()
	leases, err := book.Reserve(runID, fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.heartbeatTTL)
	if err != nil {
		t.Fatalf("RoutingLeaseBook.Reserve() = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("planned lease count = %d, want 1", len(leases))
	}
	assertHeterociRoutingLeaseBinding(t, leases[0], assignment, heteroci.LeaseStatePlanned)
	return fixture, assignment, book
}

func cloneHeterociRoutingStartSnapshot(snapshot heteroci.SnapshotSet) heteroci.SnapshotSet {
	clone := snapshot
	clone.Nodes = slices.Clone(snapshot.Nodes)
	for index := range clone.Nodes {
		clone.Nodes[index].ActiveCapabilities = slices.Clone(snapshot.Nodes[index].ActiveCapabilities)
		for capabilityIndex, capability := range snapshot.Nodes[index].ActiveCapabilities {
			if capability.Qualification != nil {
				qualification := *capability.Qualification
				clone.Nodes[index].ActiveCapabilities[capabilityIndex].Qualification = &qualification
			}
		}
	}
	return clone
}

func assertHeterociRoutingStartRejectsWithoutBootSentinel(t *testing.T, err error, sentinel string) {
	t.Helper()
	if err == nil {
		t.Fatal("RoutingLeaseBook.Start() unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("RoutingLeaseBook.Start() leaked boot sentinel %q: %v", sentinel, err)
	}
}

func snapshotHeterociRoutingStartBook(t *testing.T, book *heteroci.RoutingLeaseBook, runID string) []heteroci.RoutingLease {
	t.Helper()
	leases, err := book.Snapshot(runID)
	if err != nil {
		t.Fatalf("RoutingLeaseBook.Snapshot(%q) = %v", runID, err)
	}
	return leases
}

func assertHeterociRoutingStartBookUnchanged(t *testing.T, book *heteroci.RoutingLeaseBook, runID string, before []heteroci.RoutingLease) {
	t.Helper()
	after := snapshotHeterociRoutingStartBook(t, book, runID)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("RoutingLeaseBook.Snapshot(%q) changed after rejected Start: before=%#v after=%#v", runID, before, after)
	}
}
