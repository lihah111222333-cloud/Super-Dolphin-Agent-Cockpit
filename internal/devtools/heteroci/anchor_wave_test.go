package heteroci

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestBuildAnchorWavePlanDeterministicAcrossInputOrder(t *testing.T) {
	now := snapshotTestNow()
	manifest := anchorWaveLinuxManifest(3)
	snapshot := anchorWaveSnapshot(t, manifest, nil)
	units := anchorWaveUnits(6, RuntimeUbuntuContainer, PlatformLinuxAMD64)

	want := anchorWaveBuild(t, manifest, snapshot, units, now)
	repeat := anchorWaveBuild(t, manifest, snapshot, units, now)
	if !reflect.DeepEqual(repeat, want) {
		t.Fatalf("repeated identical inputs changed plan:\nwant=%#v\nrepeat=%#v", want, repeat)
	}
	shuffledManifest := manifest
	shuffledManifest.Nodes[0], shuffledManifest.Nodes[1] = shuffledManifest.Nodes[1], shuffledManifest.Nodes[0]
	shuffledSnapshot := snapshot
	shuffledSnapshot.Nodes[0], shuffledSnapshot.Nodes[1] = shuffledSnapshot.Nodes[1], shuffledSnapshot.Nodes[0]
	shuffledUnits := append([]ExecutionUnit(nil), units...)
	shuffledUnits[0], shuffledUnits[5] = shuffledUnits[5], shuffledUnits[0]
	shuffledUnits[1], shuffledUnits[4] = shuffledUnits[4], shuffledUnits[1]
	got := anchorWaveBuild(t, shuffledManifest, shuffledSnapshot, shuffledUnits, now)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reordered inputs changed plan:\nwant=%#v\ngot=%#v", want, got)
	}
	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want plan: %v", err)
	}
	gotBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got plan: %v", err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("reordered inputs changed plan bytes:\nwant=%s\ngot=%s", wantBytes, gotBytes)
	}
}

func TestBuildAnchorWavePlanUsesTwoLinuxNodes(t *testing.T) {
	now := snapshotTestNow()
	manifest := anchorWaveLinuxManifest(4)
	snapshot := anchorWaveSnapshot(t, manifest, nil)
	units := anchorWaveUnits(8, RuntimeUbuntuContainer, PlatformLinuxAMD64)
	plan := anchorWaveBuild(t, manifest, snapshot, units, now)
	if len(plan.NodeShards) != 2 {
		t.Fatalf("linux node shard count = %d, want 2", len(plan.NodeShards))
	}
	if len(plan.UnassignedUnitIDs) != 0 {
		t.Fatalf("unexpected unassigned units: %v", plan.UnassignedUnitIDs)
	}
	anchorWaveAssertCoverage(t, plan, units)
}

func TestBuildAnchorWavePlanHonorsTwoDimensionalResourcesAndSerialMakespan(t *testing.T) {
	tests := []struct {
		name       string
		capacity   ResourceCapacity
		unitCPU    int64
		unitMemory int64
	}{
		{
			name:       "memory bottleneck",
			capacity:   ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 * 1024 * 1024 * 1024, MaxConcurrency: 4},
			unitCPU:    500,
			unitMemory: 5 * 1024 * 1024 * 1024,
		},
		{
			name:       "cpu bottleneck",
			capacity:   ResourceCapacity{CPUMilli: 1_000, MemoryBytes: 8 * 1024 * 1024 * 1024, MaxConcurrency: 4},
			unitCPU:    600,
			unitMemory: 1 * 1024 * 1024 * 1024,
		},
		{
			name:       "concurrency bottleneck",
			capacity:   ResourceCapacity{CPUMilli: 8_000, MemoryBytes: 8 * 1024 * 1024 * 1024, MaxConcurrency: 1},
			unitCPU:    500,
			unitMemory: 1 * 1024 * 1024 * 1024,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := snapshotTestNow()
			manifest := anchorWaveLinuxManifest(1)
			nodeID := manifest.Nodes[0].NodeID
			capacities := map[string]ResourceCapacity{nodeID: tt.capacity}
			snapshot := anchorWaveSnapshot(t, manifest, capacities)
			snapshot.Nodes[1].Status = NodeStatusOffline
			snapshot.Nodes[1].ProbeBootID = ""
			snapshot.Nodes[1].ActiveCapabilities = []ActiveCapabilitySnapshot{}
			snapshot.Nodes[1].Total = ResourceCapacity{}
			snapshot.Nodes[1].Available = ResourceCapacity{}
			units := anchorWaveUnits(4, RuntimeUbuntuContainer, PlatformLinuxAMD64)
			for index := range units {
				units[index].EstimatedDurationMS = int64((index + 1) * 100)
				units[index].CPUMilli = tt.unitCPU
				units[index].MemoryBytes = tt.unitMemory
			}
			plan := anchorWaveBuild(t, manifest, snapshot, units, now)
			if len(plan.NodeShards) != 1 || len(plan.NodeShards[0].Waves) != len(units) {
				t.Fatalf("waves = %#v, want one serial wave per unit", plan.NodeShards)
			}
			if plan.NodeShards[0].EstimatedMakespanMS != 1_000 {
				t.Fatalf("makespan = %d, want sum of wave maxima 1000", plan.NodeShards[0].EstimatedMakespanMS)
			}
			anchorWaveAssertWaveBounds(t, plan, snapshot)
		})
	}
}

func TestBuildAnchorWavePlanReturnsTypedCapacityBlocker(t *testing.T) {
	now := snapshotTestNow()
	manifest := anchorWaveUnassignedManifest()
	capacities := map[string]ResourceCapacity{
		"anchor-linux-saturated": {CPUMilli: 2_000, MemoryBytes: 2 * 1024 * 1024 * 1024, MaxConcurrency: 2},
		"anchor-linux-2":         {CPUMilli: 1_000, MemoryBytes: 1 * 1024 * 1024 * 1024, MaxConcurrency: 1},
		"anchor-windows":         {CPUMilli: 2_000, MemoryBytes: 2 * 1024 * 1024 * 1024, MaxConcurrency: 1},
	}
	snapshot := anchorWaveSnapshot(t, manifest, capacities)
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].NodeID == "anchor-linux-saturated" {
			snapshot.Nodes[index].Available = ResourceCapacity{}
		}
	}
	units := []ExecutionUnit{anchorWaveUnit("saturated-ubuntu", RuntimeUbuntuContainer, PlatformLinuxAMD64)}
	snapshot.Nodes[0].Available = ResourceCapacity{}
	snapshot.Nodes[1].Available = ResourceCapacity{}
	plan, err := BuildAnchorWavePlan(manifest, snapshot, units, now, snapshotTestHeartbeatTTL)
	if err == nil || len(plan.UnassignedUnitIDs) != 1 || plan.UnassignedUnitIDs[0] != "saturated-ubuntu" {
		t.Fatalf("BuildAnchorWavePlan() = plan:%#v err:%v, want replayable unassigned plan and blocker", plan, err)
	}
	var blocker *CapacityBlockerError
	if !errors.As(err, &blocker) || len(blocker.Blockers) != 1 || blocker.Blockers[0].UnitID != "saturated-ubuntu" {
		t.Fatalf("BuildAnchorWavePlan error = %T %v, want typed CapacityBlocker", err, err)
	}
	if blocker.ValidateAgainstPlan(plan) != nil {
		t.Fatalf("capacity blocker plan digest does not replay against plan: %v", blocker)
	}
}

func TestBuildAnchorWavePlanRejectsInvalidInputAndArithmeticOverflowWithZeroPlan(t *testing.T) {
	now := snapshotTestNow()
	validManifest := anchorWaveLinuxManifest(1)
	validSnapshot := anchorWaveSnapshot(t, validManifest, nil)
	validUnits := anchorWaveUnits(1, RuntimeUbuntuContainer, PlatformLinuxAMD64)
	tests := []struct {
		name     string
		manifest Manifest
		snapshot SnapshotSet
		units    []ExecutionUnit
	}{
		{name: "invalid manifest", manifest: Manifest{}, snapshot: SnapshotSet{}, units: validUnits},
		{name: "nil units", manifest: validManifest, snapshot: validSnapshot, units: nil},
		{name: "invalid unit", manifest: validManifest, snapshot: validSnapshot, units: []ExecutionUnit{{UnitID: "bad", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := BuildAnchorWavePlan(tt.manifest, tt.snapshot, tt.units, now, snapshotTestHeartbeatTTL)
			if err == nil {
				t.Fatal("BuildAnchorWavePlan() unexpectedly succeeded")
			}
			if !reflect.DeepEqual(plan, PackingPlan{}) {
				t.Fatalf("error plan = %#v, want zero PackingPlan", plan)
			}
		})
	}
}

func anchorWaveBuild(t *testing.T, manifest Manifest, snapshot SnapshotSet, units []ExecutionUnit, now time.Time) PackingPlan {
	t.Helper()
	plan, err := BuildAnchorWavePlan(manifest, snapshot, units, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("BuildAnchorWavePlan() error = %v", err)
	}
	if err := plan.ValidateAgainst(manifest, snapshot, units, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("PackingPlan.ValidateAgainst() error = %v", err)
	}
	return plan
}

func anchorWaveManifest(nodes ...NodeConfig) Manifest {
	return Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Authority:     AuthorityLocalNonAuthoritative,
		Nodes:         nodes,
	}
}

func anchorWaveLinuxManifest(_ int) Manifest {
	return anchorWaveManifest(
		anchorWaveNodeWithID("anchor-linux-1", 0, PlatformLinuxAMD64, anchorWaveUbuntuCapability()),
		anchorWaveNodeWithID("anchor-linux-2", 1, PlatformLinuxAMD64, anchorWaveUbuntuCapability()),
	)
}

func anchorWaveRoutingManifest() Manifest {
	return anchorWaveManifest(
		anchorWaveNodeWithID("anchor-linux", 0, PlatformLinuxAMD64, anchorWaveUbuntuCapability()),
		anchorWaveNodeWithID("anchor-linux-2", 1, PlatformLinuxAMD64, anchorWaveUbuntuCapability()),
	)
}

func anchorWaveUnassignedManifest() Manifest {
	return anchorWaveManifest(
		anchorWaveNodeWithID("anchor-linux-saturated", 0, PlatformLinuxAMD64, anchorWaveUbuntuCapability()),
		anchorWaveNodeWithID("anchor-linux-2", 1, PlatformLinuxAMD64, anchorWaveUbuntuCapability()),
	)
}

func anchorWaveNodeWithID(nodeID string, index int, platform Platform, capabilities ...CapabilityConfig) NodeConfig {
	return NodeConfig{
		NodeID:        nodeID,
		Endpoint:      "https://" + nodeID + ".invalid/runner",
		ProbeIdentity: fmt.Sprintf("sha256:%064x", 0x900+index),
		Platform:      platform,
		Capabilities:  capabilities,
	}
}

func anchorWaveUbuntuCapability() CapabilityConfig {
	return CapabilityConfig{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: snapshotTestImageDigest}
}

func anchorWaveSnapshot(t *testing.T, manifest Manifest, capacities map[string]ResourceCapacity) SnapshotSet {
	t.Helper()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("anchor wave manifest invalid: %v", err)
	}
	snapshot := snapshotForManifest(manifest, snapshotTestNow())
	for index := range snapshot.Nodes {
		if capacity, ok := capacities[snapshot.Nodes[index].NodeID]; ok {
			snapshot.Nodes[index].Total = capacity
			snapshot.Nodes[index].Available = capacity
		}
	}
	return snapshot
}

func anchorWaveUnits(count int, runtime RuntimeKind, target Platform) []ExecutionUnit {
	units := make([]ExecutionUnit, count)
	for index := range units {
		units[index] = anchorWaveUnit(fmt.Sprintf("anchor-unit-%02d", index+1), runtime, target)
	}
	return units
}

func anchorWaveUnit(id string, runtime RuntimeKind, target Platform) ExecutionUnit {
	return ExecutionUnit{UnitID: id, TargetPlatform: target, RequiredRuntime: runtime, EstimatedDurationMS: 100, CPUMilli: 500, MemoryBytes: 256 * 1024 * 1024}
}

func anchorWaveAssertCoverage(t *testing.T, plan PackingPlan, units []ExecutionUnit) {
	t.Helper()
	seen := make(map[string]int, len(units))
	for _, shard := range plan.NodeShards {
		for _, wave := range shard.Waves {
			for _, unitID := range wave.UnitIDs {
				seen[unitID]++
			}
		}
	}
	for _, unitID := range plan.UnassignedUnitIDs {
		seen[unitID]++
	}
	for _, unit := range units {
		if seen[unit.UnitID] != 1 {
			t.Errorf("unit %q appears %d times in plan", unit.UnitID, seen[unit.UnitID])
		}
	}
}

func anchorWaveAssertWaveBounds(t *testing.T, plan PackingPlan, snapshot SnapshotSet) {
	t.Helper()
	capacities := make(map[string]ResourceCapacity, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		capacities[node.NodeID] = node.Available
	}
	for _, shard := range plan.NodeShards {
		capacity, ok := capacities[shard.NodeID]
		if !ok {
			t.Fatalf("plan references unknown node %q", shard.NodeID)
		}
		var makespan int64
		for _, wave := range shard.Waves {
			anchorWaveAssertWave(t, shard.NodeID, wave, capacity)
			if makespan > math.MaxInt64-wave.EstimatedDurationMS {
				t.Fatalf("test overflow while adding makespan")
			}
			makespan += wave.EstimatedDurationMS
		}
		if shard.EstimatedMakespanMS != makespan {
			t.Errorf("node %q makespan=%d, sum of wave maxima=%d", shard.NodeID, shard.EstimatedMakespanMS, makespan)
		}
	}
}

func anchorWaveAssertWave(t *testing.T, nodeID string, wave ExecutionWave, capacity ResourceCapacity) {
	t.Helper()
	if wave.Concurrency != len(wave.UnitIDs) {
		t.Errorf("node %q wave %d concurrency=%d, unit count=%d", nodeID, wave.Wave, wave.Concurrency, len(wave.UnitIDs))
	}
	if wave.CPUMilli > capacity.CPUMilli || wave.MemoryBytes > capacity.MemoryBytes || wave.Concurrency > capacity.MaxConcurrency {
		t.Errorf("node %q wave %d exceeds capacity: wave=%#v capacity=%#v", nodeID, wave.Wave, wave, capacity)
	}
}
