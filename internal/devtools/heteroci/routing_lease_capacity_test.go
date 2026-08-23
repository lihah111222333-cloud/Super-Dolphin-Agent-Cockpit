package heteroci

import (
	"math"
	"reflect"
	"testing"
)

func TestRoutingNodeCapacityReservationsUsesComponentwiseWavePeaksAndCanonicalIdentity(t *testing.T) {
	plan := PackingPlan{
		NodeShards: []NodeShard{
			{
				NodeID: "node-z",
				Waves: []ExecutionWave{
					{Wave: 0, UnitIDs: []string{"z-memory"}, CPUMilli: 100, MemoryBytes: 900, Concurrency: 1, EstimatedDurationMS: 10},
					{Wave: 1, UnitIDs: []string{"z-cpu"}, CPUMilli: 800, MemoryBytes: 200, Concurrency: 3, EstimatedDurationMS: 20},
				},
			},
			{
				NodeID: "node-a",
				Waves: []ExecutionWave{
					{Wave: 0, UnitIDs: []string{"a-cpu"}, CPUMilli: 300, MemoryBytes: 300, Concurrency: 2, EstimatedDurationMS: 10},
					{Wave: 1, UnitIDs: []string{"a-memory"}, CPUMilli: 200, MemoryBytes: 700, Concurrency: 1, EstimatedDurationMS: 20},
				},
			},
		},
		UnassignedUnitIDs: []string{"eci-overflow"},
	}
	snapshot := SnapshotSet{Nodes: []NodeSnapshot{
		{NodeID: "node-z", ProbeIdentity: "probe-z", ProbeBootID: "boot-z", Status: NodeStatusLive},
		{NodeID: "node-a", ProbeIdentity: "probe-a", ProbeBootID: "boot-a", Status: NodeStatusLive},
	}}

	got, err := routingNodeCapacityReservations(plan, snapshot)
	if err != nil {
		t.Fatalf("routingNodeCapacityReservations() error = %v", err)
	}
	want := []nodeCapacityReservation{
		{
			key:      nodeReservationKey{nodeID: "node-a", probeIdentity: "probe-a", probeBootID: "boot-a"},
			capacity: ResourceCapacity{CPUMilli: 300, MemoryBytes: 700, MaxConcurrency: 2},
		},
		{
			key:      nodeReservationKey{nodeID: "node-z", probeIdentity: "probe-z", probeBootID: "boot-z"},
			capacity: ResourceCapacity{CPUMilli: 800, MemoryBytes: 900, MaxConcurrency: 3},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reservations = %#v, want %#v", got, want)
	}
}

func TestRoutingNodeCapacityReservationsIgnoresUnassignedOverflowUnits(t *testing.T) {
	plan := PackingPlan{NodeShards: []NodeShard{}, UnassignedUnitIDs: []string{"eci-1", "eci-2"}}
	got, err := routingNodeCapacityReservations(plan, SnapshotSet{})
	if err != nil {
		t.Fatalf("routingNodeCapacityReservations() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("reservations for unassigned units = %#v, want empty", got)
	}
}

func TestRoutingNodeCapacityReservationsRejectsMissingOfflineEmptyAndNonPositiveWaves(t *testing.T) {
	validSnapshot := SnapshotSet{Nodes: []NodeSnapshot{{
		NodeID: "node-a", ProbeIdentity: "probe-a", ProbeBootID: "boot-a", Status: NodeStatusLive,
	}}}
	validWave := ExecutionWave{Wave: 0, UnitIDs: []string{"unit"}, CPUMilli: 100, MemoryBytes: 200, Concurrency: 1, EstimatedDurationMS: 10}
	tests := []struct {
		name     string
		plan     PackingPlan
		snapshot SnapshotSet
	}{
		{
			name:     "missing node",
			plan:     PackingPlan{NodeShards: []NodeShard{{NodeID: "node-missing", Waves: []ExecutionWave{validWave}}}},
			snapshot: validSnapshot,
		},
		{
			name:     "offline node",
			plan:     PackingPlan{NodeShards: []NodeShard{{NodeID: "node-a", Waves: []ExecutionWave{validWave}}}},
			snapshot: SnapshotSet{Nodes: []NodeSnapshot{{NodeID: "node-a", ProbeIdentity: "probe-a", ProbeBootID: "boot-a", Status: NodeStatusOffline}}},
		},
		{
			name:     "empty waves",
			plan:     PackingPlan{NodeShards: []NodeShard{{NodeID: "node-a"}}},
			snapshot: validSnapshot,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := routingNodeCapacityReservations(tt.plan, tt.snapshot); err == nil {
				t.Fatalf("routingNodeCapacityReservations() unexpectedly succeeded")
			}
		})
	}

	for _, field := range []struct {
		name   string
		mutate func(*ExecutionWave)
	}{
		{name: "zero cpu", mutate: func(wave *ExecutionWave) { wave.CPUMilli = 0 }},
		{name: "negative cpu", mutate: func(wave *ExecutionWave) { wave.CPUMilli = -1 }},
		{name: "zero memory", mutate: func(wave *ExecutionWave) { wave.MemoryBytes = 0 }},
		{name: "negative memory", mutate: func(wave *ExecutionWave) { wave.MemoryBytes = -1 }},
		{name: "zero concurrency", mutate: func(wave *ExecutionWave) { wave.Concurrency = 0 }},
		{name: "negative concurrency", mutate: func(wave *ExecutionWave) { wave.Concurrency = -1 }},
	} {
		t.Run(field.name, func(t *testing.T) {
			wave := validWave
			field.mutate(&wave)
			plan := PackingPlan{NodeShards: []NodeShard{{NodeID: "node-a", Waves: []ExecutionWave{wave}}}}
			if _, err := routingNodeCapacityReservations(plan, validSnapshot); err == nil {
				t.Fatalf("routingNodeCapacityReservations() unexpectedly accepted %#v", wave)
			}
		})
	}
}

func TestCheckedAddReservationAcceptsZeroCurrentAndExactBoundary(t *testing.T) {
	limit := ResourceCapacity{CPUMilli: 400, MemoryBytes: 600, MaxConcurrency: 4}
	got, err := checkedAddReservation(
		ResourceCapacity{CPUMilli: 100, MemoryBytes: 200, MaxConcurrency: 1},
		ResourceCapacity{CPUMilli: 300, MemoryBytes: 400, MaxConcurrency: 3},
		limit,
	)
	if err != nil {
		t.Fatalf("checkedAddReservation() exact boundary error = %v", err)
	}
	if !reflect.DeepEqual(got, limit) {
		t.Fatalf("exact-boundary sum = %#v, want %#v", got, limit)
	}

	got, err = checkedAddReservation(ResourceCapacity{}, ResourceCapacity{CPUMilli: 1, MemoryBytes: 2, MaxConcurrency: 1}, limit)
	if err != nil {
		t.Fatalf("checkedAddReservation() zero current error = %v", err)
	}
	if !reflect.DeepEqual(got, ResourceCapacity{CPUMilli: 1, MemoryBytes: 2, MaxConcurrency: 1}) {
		t.Fatalf("zero-current sum = %#v", got)
	}
}

func TestCheckedAddReservationRejectsIndependentResourceLimitExcess(t *testing.T) {
	tests := []struct {
		name      string
		current   ResourceCapacity
		requested ResourceCapacity
		limit     ResourceCapacity
	}{
		{
			name:      "cpu",
			current:   ResourceCapacity{CPUMilli: 5, MemoryBytes: 5, MaxConcurrency: 1},
			requested: ResourceCapacity{CPUMilli: 6, MemoryBytes: 1, MaxConcurrency: 1},
			limit:     ResourceCapacity{CPUMilli: 10, MemoryBytes: 10, MaxConcurrency: 10},
		},
		{
			name:      "memory",
			current:   ResourceCapacity{CPUMilli: 5, MemoryBytes: 5, MaxConcurrency: 1},
			requested: ResourceCapacity{CPUMilli: 1, MemoryBytes: 6, MaxConcurrency: 1},
			limit:     ResourceCapacity{CPUMilli: 10, MemoryBytes: 10, MaxConcurrency: 10},
		},
		{
			name:      "concurrency",
			current:   ResourceCapacity{CPUMilli: 5, MemoryBytes: 5, MaxConcurrency: 5},
			requested: ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 6},
			limit:     ResourceCapacity{CPUMilli: 10, MemoryBytes: 10, MaxConcurrency: 10},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := checkedAddReservation(tt.current, tt.requested, tt.limit); err == nil {
				t.Fatalf("checkedAddReservation() unexpectedly accepted %s excess", tt.name)
			}
		})
	}
}

func TestCheckedAddReservationRejectsOverflowAndNegativeValues(t *testing.T) {
	overflowCases := []struct {
		name      string
		current   ResourceCapacity
		requested ResourceCapacity
		limit     ResourceCapacity
	}{
		{
			name:      "cpu overflow",
			current:   ResourceCapacity{CPUMilli: math.MaxInt64, MemoryBytes: 1, MaxConcurrency: 1},
			requested: ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
			limit:     ResourceCapacity{CPUMilli: math.MaxInt64, MemoryBytes: 10, MaxConcurrency: 10},
		},
		{
			name:      "memory overflow",
			current:   ResourceCapacity{CPUMilli: 1, MemoryBytes: math.MaxInt64, MaxConcurrency: 1},
			requested: ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
			limit:     ResourceCapacity{CPUMilli: 10, MemoryBytes: math.MaxInt64, MaxConcurrency: 10},
		},
		{
			name:      "concurrency overflow",
			current:   ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: math.MaxInt},
			requested: ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
			limit:     ResourceCapacity{CPUMilli: 10, MemoryBytes: 10, MaxConcurrency: math.MaxInt},
		},
	}
	for _, tt := range overflowCases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := checkedAddReservation(tt.current, tt.requested, tt.limit); err == nil {
				t.Fatalf("checkedAddReservation() unexpectedly accepted overflow")
			}
		})
	}

	negativeCases := []struct {
		name      string
		current   ResourceCapacity
		requested ResourceCapacity
		limit     ResourceCapacity
	}{
		{
			name:      "negative current",
			current:   ResourceCapacity{CPUMilli: -1, MemoryBytes: 1, MaxConcurrency: 1},
			requested: ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
			limit:     ResourceCapacity{CPUMilli: 10, MemoryBytes: 10, MaxConcurrency: 10},
		},
		{
			name:      "negative requested",
			current:   ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
			requested: ResourceCapacity{CPUMilli: 1, MemoryBytes: -1, MaxConcurrency: 1},
			limit:     ResourceCapacity{CPUMilli: 10, MemoryBytes: 10, MaxConcurrency: 10},
		},
		{
			name:      "negative limit",
			current:   ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
			requested: ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
			limit:     ResourceCapacity{CPUMilli: 10, MemoryBytes: 10, MaxConcurrency: -1},
		},
	}
	for _, tt := range negativeCases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := checkedAddReservation(tt.current, tt.requested, tt.limit); err == nil {
				t.Fatalf("checkedAddReservation() unexpectedly accepted negative value")
			}
		})
	}
}
