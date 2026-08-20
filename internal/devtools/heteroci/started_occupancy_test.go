package heteroci

import (
	"reflect"
	"strings"
	"testing"
)

func TestStartedRoutingOccupancyValidatesAndDerivesFromStartedLease(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	lease := validRoutingLease(fixture, 0, LeaseStateStarted, fixture.now.UnixMilli()-5_000, fixture.now.UnixMilli()-1_000, 0)
	fence := validStartedOccupancyFence()
	started, err := NewStartedRoutingLease(fence, lease, fixture.decision, fixture.now)
	if err != nil {
		t.Fatalf("NewStartedRoutingLease: %v", err)
	}
	if err := started.ValidateAgainst(fixture.decision, fixture.now); err != nil {
		t.Fatalf("StartedRoutingLease.ValidateAgainst: %v", err)
	}
	if started.Occupancy.OwnerEpoch != fence.OwnerEpoch || started.Occupancy.RunID != lease.RunID || started.Occupancy.UnitID != lease.UnitID || started.Occupancy.DecisionDigest != lease.DecisionDigest {
		t.Fatalf("occupancy was not derived from fence/lease: %#v", started.Occupancy)
	}
}

func TestStartedRoutingOccupancyRejectsDriftAndWrongState(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	lease := validRoutingLease(fixture, 0, LeaseStateStarted, fixture.now.UnixMilli()-5_000, fixture.now.UnixMilli()-1_000, 0)
	fence := validStartedOccupancyFence()
	started, err := NewStartedRoutingLease(fence, lease, fixture.decision, fixture.now)
	if err != nil {
		t.Fatalf("NewStartedRoutingLease: %v", err)
	}
	for name, mutate := range map[string]func(*StartedRoutingLease){
		"epoch": func(value *StartedRoutingLease) { value.Occupancy.OwnerEpoch++ },
		"run":   func(value *StartedRoutingLease) { value.Occupancy.RunID = "other-run" },
		"unit":  func(value *StartedRoutingLease) { value.Occupancy.UnitID = "other-unit" },
		"digest": func(value *StartedRoutingLease) {
			value.Occupancy.DecisionDigest = "sha256:" + strings.Repeat("f", 64)
		},
		"state": func(value *StartedRoutingLease) { value.Lease.State = LeaseStatePlanned },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := started
			mutate(&candidate)
			if err := candidate.ValidateAgainst(fixture.decision, fixture.now); err == nil {
				t.Fatalf("ValidateAgainst accepted %s drift", name)
			}
		})
	}
}

func TestStartedRoutingOccupancyWireLayout(t *testing.T) {
	assertFieldOrder(t, reflect.TypeFor[StartedRoutingOccupancy](), []string{"OwnerEpoch", "RunID", "UnitID", "DecisionDigest"})
	assertFieldOrder(t, reflect.TypeFor[StartedRoutingLease](), []string{"Fence", "Lease", "Occupancy"})
}

func validStartedOccupancyFence() OwnerFence {
	fence, err := NewOwnerFence(
		RoutingOwnerScope("sha256:"+strings.Repeat("a", 64)),
		RoutingOwnerID(strings.Repeat("b", 64)),
		RoutingOwnerEpoch(1),
		RoutingOwnerRevision(1),
	)
	if err != nil {
		// archguard:ignore panic_count -- 固定测试夹具的 owner fence 构造失败时无法继续验证占用。
		panic(err)
	}
	return fence
}

func assertFieldOrder(t *testing.T, typ reflect.Type, want []string) {
	t.Helper()
	if typ.NumField() != len(want) {
		t.Fatalf("%s field count = %d, want %d", typ.Name(), typ.NumField(), len(want))
	}
	for index, name := range want {
		if got := typ.Field(index).Name; got != name {
			t.Errorf("%s field %d = %q, want %q", typ.Name(), index, got, name)
		}
	}
}
