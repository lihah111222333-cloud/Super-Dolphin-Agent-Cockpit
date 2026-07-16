package localci

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

const testDaemonID = "daemon-capacity-test"

func testObservedAt() time.Time {
	return time.Date(2026, time.July, 16, 9, 30, 0, 0, time.UTC)
}

type capacityInspectorStub struct {
	capacity DaemonCapacity
	err      error
	calls    int
}

func (stub *capacityInspectorStub) InspectDaemonCapacity(context.Context, string) (DaemonCapacity, error) {
	stub.calls++
	return stub.capacity, stub.err
}

func TestValidateDaemonCapacityBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		logicalCPUs   int64
		memoryBytes   int64
		wantErr       bool
		wantSubstring string
	}{
		{name: "current class environment lacks memory", logicalCPUs: 18, memoryBytes: 765 * bytesPerGiB / 100, wantErr: true, wantSubstring: "memory capacity insufficient"},
		{name: "eighteen CPUs and twenty five GiB pass", logicalCPUs: 18, memoryBytes: 25 * bytesPerGiB},
		{name: "eleven CPUs fail", logicalCPUs: 11, memoryBytes: 25 * bytesPerGiB, wantErr: true, wantSubstring: "logical CPU capacity insufficient"},
		{name: "one byte below memory requirement fails", logicalCPUs: 18, memoryBytes: 24*bytesPerGiB - 1, wantErr: true, wantSubstring: "memory capacity insufficient"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &capacityInspectorStub{capacity: validDaemonCapacity(test.logicalCPUs, test.memoryBytes)}
			evidence, err := ValidateDaemonCapacity(context.Background(), testDaemonID, stub)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), test.wantSubstring) {
					t.Fatalf("ValidateDaemonCapacity() error = %v, want substring %q", err, test.wantSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateDaemonCapacity() error = %v", err)
			}
			if evidence.Required.LogicalCPUs != 12 || evidence.Required.MemoryBytes != 24*bytesPerGiB {
				t.Fatalf("required capacity = %#v", evidence.Required)
			}
			if evidence.DaemonID != testDaemonID || evidence.ObservedAt != testObservedAt() {
				t.Fatalf("capacity evidence identity = %#v", evidence)
			}
		})
	}
}

func TestValidateDaemonCapacityRejectsInvalidInputs(t *testing.T) {
	validInspector := &capacityInspectorStub{capacity: validDaemonCapacity(18, 25*bytesPerGiB)}
	tests := []struct {
		name      string
		ctx       context.Context
		daemonID  string
		inspector DaemonCapacityInspector
	}{
		{name: "nil context", daemonID: testDaemonID, inspector: validInspector},
		{name: "nil inspector", ctx: context.Background(), daemonID: testDaemonID},
		{name: "empty daemon ID", ctx: context.Background(), inspector: validInspector},
		{name: "whitespace daemon ID", ctx: context.Background(), daemonID: "  ", inspector: validInspector},
		{name: "non canonical daemon ID", ctx: context.Background(), daemonID: " " + testDaemonID, inspector: validInspector},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateDaemonCapacity(test.ctx, test.daemonID, test.inspector); err == nil {
				t.Fatal("ValidateDaemonCapacity() error = nil")
			}
		})
	}

	var typedNil *capacityInspectorStub
	if _, err := ValidateDaemonCapacity(context.Background(), testDaemonID, typedNil); err == nil {
		t.Fatal("ValidateDaemonCapacity() accepted typed-nil inspector")
	}
}

func TestValidateDaemonCapacityRejectsInvalidCapacityValues(t *testing.T) {
	tests := []struct {
		name     string
		capacity DaemonCapacity
	}{
		{name: "zero CPUs", capacity: validDaemonCapacity(0, 25*bytesPerGiB)},
		{name: "negative CPUs", capacity: validDaemonCapacity(-1, 25*bytesPerGiB)},
		{name: "zero memory", capacity: validDaemonCapacity(18, 0)},
		{name: "negative memory", capacity: validDaemonCapacity(18, -1)},
		{name: "zero observation time", capacity: DaemonCapacity{DaemonID: testDaemonID, LogicalCPUs: 18, MemoryBytes: 25 * bytesPerGiB}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &capacityInspectorStub{capacity: test.capacity}
			if _, err := ValidateDaemonCapacity(context.Background(), testDaemonID, stub); err == nil {
				t.Fatal("ValidateDaemonCapacity() error = nil")
			}
		})
	}
}

func TestValidateDaemonCapacityPropagatesInspectorError(t *testing.T) {
	wantErr := errors.New("docker info unavailable")
	stub := &capacityInspectorStub{err: wantErr}
	_, err := ValidateDaemonCapacity(context.Background(), testDaemonID, stub)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ValidateDaemonCapacity() error = %v, want %v", err, wantErr)
	}
}

func TestValidateDaemonCapacityRejectsDaemonIdentityMismatch(t *testing.T) {
	capacity := validDaemonCapacity(18, 25*bytesPerGiB)
	capacity.DaemonID = "different-daemon"
	stub := &capacityInspectorStub{capacity: capacity}
	_, err := ValidateDaemonCapacity(context.Background(), testDaemonID, stub)
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("ValidateDaemonCapacity() error = %v, want identity mismatch", err)
	}
}

func TestValidateDaemonCapacityRejectsCanceledContextBeforeInspection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stub := &capacityInspectorStub{capacity: validDaemonCapacity(18, 25*bytesPerGiB)}
	_, err := ValidateDaemonCapacity(ctx, testDaemonID, stub)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateDaemonCapacity() error = %v, want context canceled", err)
	}
	if stub.calls != 0 {
		t.Fatalf("inspector calls = %d, want 0", stub.calls)
	}
}

func TestCapacityEvidenceIsCopiedAndStrictlyValidated(t *testing.T) {
	stub := &capacityInspectorStub{capacity: validDaemonCapacity(18, 25*bytesPerGiB)}
	evidence, err := ValidateDaemonCapacity(context.Background(), testDaemonID, stub)
	if err != nil {
		t.Fatalf("ValidateDaemonCapacity() error = %v", err)
	}
	stub.capacity.DaemonID = "mutated-daemon"
	stub.capacity.MemoryBytes = 1
	if evidence.Available.DaemonID != testDaemonID || evidence.Available.MemoryBytes != 25*bytesPerGiB {
		t.Fatalf("evidence changed with inspector state: %#v", evidence)
	}

	invalidIdentity := evidence
	invalidIdentity.Available.DaemonID = "different-daemon"
	if err := invalidIdentity.Validate(); err == nil {
		t.Fatal("CapacityEvidence.Validate() accepted mismatched daemon identity")
	}
	invalidObservation := evidence
	invalidObservation.ObservedAt = invalidObservation.ObservedAt.Add(time.Second)
	if err := invalidObservation.Validate(); err == nil {
		t.Fatal("CapacityEvidence.Validate() accepted mismatched observedAt")
	}
	invalidRequirement := evidence
	invalidRequirement.Required.MemoryBytes = 0
	if err := invalidRequirement.Validate(); err == nil {
		t.Fatal("CapacityEvidence.Validate() accepted zero requirement")
	}
}

func TestCheckedCapacityProductRejectsInvalidAndOverflow(t *testing.T) {
	tests := []struct {
		value      int64
		multiplier int64
	}{
		{value: 0, multiplier: 1},
		{value: 1, multiplier: 0},
		{value: -1, multiplier: 1},
		{value: 1, multiplier: -1},
		{value: math.MaxInt64, multiplier: 2},
	}
	for _, test := range tests {
		if _, err := checkedCapacityProduct(test.value, test.multiplier); err == nil {
			t.Fatalf("checkedCapacityProduct(%d, %d) error = nil", test.value, test.multiplier)
		}
	}
}

func validDaemonCapacity(logicalCPUs, memoryBytes int64) DaemonCapacity {
	return DaemonCapacity{
		DaemonID:    testDaemonID,
		ObservedAt:  testObservedAt(),
		LogicalCPUs: logicalCPUs,
		MemoryBytes: memoryBytes,
	}
}
