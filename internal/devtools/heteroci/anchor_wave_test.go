package heteroci

import (
	"bytes"
	"encoding/json"
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
	shuffledManifest.Nodes[0], shuffledManifest.Nodes[2] = shuffledManifest.Nodes[2], shuffledManifest.Nodes[0]
	shuffledSnapshot := snapshot
	shuffledSnapshot.Nodes[0], shuffledSnapshot.Nodes[2] = shuffledSnapshot.Nodes[2], shuffledSnapshot.Nodes[0]
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

func TestBuildAnchorWavePlanSupportsDynamicNodeCounts(t *testing.T) {
	now := snapshotTestNow()
	for _, nodeCount := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("nodes_%d", nodeCount), func(t *testing.T) {
			manifest := anchorWaveLinuxManifest(nodeCount)
			snapshot := anchorWaveSnapshot(t, manifest, nil)
			units := anchorWaveUnits(nodeCount*2, RuntimeUbuntuContainer, PlatformLinuxAMD64)
			plan := anchorWaveBuild(t, manifest, snapshot, units, now)
			if len(plan.NodeShards) != nodeCount {
				t.Fatalf("node shard count = %d, want %d", len(plan.NodeShards), nodeCount)
			}
			if len(plan.UnassignedUnitIDs) != 0 {
				t.Fatalf("unexpected unassigned units: %v", plan.UnassignedUnitIDs)
			}
			anchorWaveAssertCoverage(t, plan, units)
		})
	}
}

func TestBuildAnchorWavePlanWindowsUsesEqualCapacityShardsAndSafeConcurrency(t *testing.T) {
	now := snapshotTestNow()
	manifest := anchorWaveWindowsManifest(2)
	capacities := map[string]ResourceCapacity{
		"anchor-windows-1": {CPUMilli: 4_000, MemoryBytes: 8 * 1024 * 1024 * 1024, MaxConcurrency: 2},
		"anchor-windows-2": {CPUMilli: 4_000, MemoryBytes: 8 * 1024 * 1024 * 1024, MaxConcurrency: 2},
	}
	snapshot := anchorWaveSnapshot(t, manifest, capacities)
	units := anchorWaveUnits(8, RuntimeWindowsNative, PlatformWindowsAMD64)
	plan := anchorWaveBuild(t, manifest, snapshot, units, now)
	if len(plan.NodeShards) != 2 {
		t.Fatalf("Windows node shard count = %d, want 2", len(plan.NodeShards))
	}
	counts := anchorWaveNodeUnitCounts(plan)
	if difference(counts["anchor-windows-1"], counts["anchor-windows-2"]) > 1 {
		t.Fatalf("equal-capacity Windows nodes received imbalanced units: %v", counts)
	}
	anchorWaveAssertWaveBounds(t, plan, snapshot)
}

func TestBuildAnchorWavePlanUsesConcurrencyWeightForUnequalNodes(t *testing.T) {
	now := snapshotTestNow()
	manifest := anchorWaveWindowsManifest(2)
	capacities := map[string]ResourceCapacity{
		"anchor-windows-1": {CPUMilli: 4_000, MemoryBytes: 8 * 1024 * 1024 * 1024, MaxConcurrency: 4},
		"anchor-windows-2": {CPUMilli: 1_000, MemoryBytes: 2 * 1024 * 1024 * 1024, MaxConcurrency: 1},
	}
	snapshot := anchorWaveSnapshot(t, manifest, capacities)
	units := anchorWaveUnits(10, RuntimeWindowsNative, PlatformWindowsAMD64)
	for index := range units {
		units[index].CPUMilli = 500
		units[index].MemoryBytes = 256 * 1024 * 1024
	}
	plan := anchorWaveBuild(t, manifest, snapshot, units, now)
	counts := anchorWaveNodeUnitCounts(plan)
	if counts["anchor-windows-1"] != 8 || counts["anchor-windows-2"] != 2 {
		t.Fatalf("4:1 concurrency capacity was not used proportionally: %v", counts)
	}
	anchorWaveAssertWaveBounds(t, plan, snapshot)
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
			snapshot := anchorWaveSnapshot(t, manifest, map[string]ResourceCapacity{nodeID: tt.capacity})
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

func TestBuildAnchorWavePlanRoutesQualifiedRuntimesToExactHosts(t *testing.T) {
	now := snapshotTestNow()
	manifest := anchorWaveRoutingManifest()
	snapshot := anchorWaveSnapshot(t, manifest, nil)
	units := []ExecutionUnit{
		anchorWaveUnit("ubuntu", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		anchorWaveUnit("darling", RuntimeDarlingCompatRuntime, PlatformDarwinAMD64),
		anchorWaveUnit("windows", RuntimeWindowsNative, PlatformWindowsAMD64),
		anchorWaveUnit("darwin-amd64", RuntimeDarwinNative, PlatformDarwinAMD64),
		anchorWaveUnit("darwin-arm64", RuntimeDarwinNative, PlatformDarwinARM64),
	}
	plan := anchorWaveBuild(t, manifest, snapshot, units, now)
	assigned := anchorWaveUnitNodes(plan)
	want := map[string]string{
		"ubuntu":       "anchor-linux",
		"darling":      "anchor-linux",
		"windows":      "anchor-windows",
		"darwin-amd64": "anchor-darwin-amd64",
		"darwin-arm64": "anchor-darwin-arm64",
	}
	if !reflect.DeepEqual(assigned, want) {
		t.Fatalf("runtime routing = %v, want %v", assigned, want)
	}
}

func TestBuildAnchorWavePlanMarksSaturatedOversizeAndUnqualifiedUnitsUnassigned(t *testing.T) {
	now := snapshotTestNow()
	manifest := anchorWaveUnassignedManifest()
	capacities := map[string]ResourceCapacity{
		"anchor-linux-saturated": {CPUMilli: 2_000, MemoryBytes: 2 * 1024 * 1024 * 1024, MaxConcurrency: 2},
		"anchor-linux-darling":   {CPUMilli: 1_000, MemoryBytes: 1 * 1024 * 1024 * 1024, MaxConcurrency: 1},
		"anchor-windows":         {CPUMilli: 2_000, MemoryBytes: 2 * 1024 * 1024 * 1024, MaxConcurrency: 1},
	}
	snapshot := anchorWaveSnapshot(t, manifest, capacities)
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].NodeID == "anchor-linux-saturated" {
			snapshot.Nodes[index].Available = ResourceCapacity{}
		}
	}
	units := []ExecutionUnit{
		anchorWaveUnit("saturated-ubuntu", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		anchorWaveUnit("oversize-darling", RuntimeDarlingCompatRuntime, PlatformDarwinAMD64),
		anchorWaveUnit("unqualified-docker-osx", RuntimeDockerOSXExperimental, PlatformDarwinAMD64),
		anchorWaveUnit("windows-valid", RuntimeWindowsNative, PlatformWindowsAMD64),
	}
	units[1].CPUMilli = 2_000
	units[1].MemoryBytes = 2 * 1024 * 1024 * 1024
	plan := anchorWaveBuild(t, manifest, snapshot, units, now)
	assigned := anchorWaveUnitNodes(plan)
	if assigned["windows-valid"] != "anchor-windows" {
		t.Fatalf("Windows valid unit assignment = %q", assigned["windows-valid"])
	}
	wantUnassigned := []string{"oversize-darling", "saturated-ubuntu", "unqualified-docker-osx"}
	if !reflect.DeepEqual(plan.UnassignedUnitIDs, wantUnassigned) {
		t.Fatalf("unassigned IDs = %v, want canonical %v", plan.UnassignedUnitIDs, wantUnassigned)
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
		{name: "duration overflow", manifest: validManifest, snapshot: anchorWaveSnapshot(t, validManifest, map[string]ResourceCapacity{validManifest.Nodes[0].NodeID: {CPUMilli: 2, MemoryBytes: 2, MaxConcurrency: 1}}), units: []ExecutionUnit{
			{UnitID: "overflow-1", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: math.MaxInt64, CPUMilli: 1, MemoryBytes: 1},
			{UnitID: "overflow-2", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: math.MaxInt64, CPUMilli: 1, MemoryBytes: 1},
		}},
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
		CloudOverflow: CloudOverflowConfig{},
		Nodes:         nodes,
	}
}

func anchorWaveLinuxManifest(nodeCount int) Manifest {
	nodes := make([]NodeConfig, nodeCount)
	for index := range nodes {
		nodes[index] = anchorWaveNode(index, PlatformLinuxAMD64, anchorWaveUbuntuCapability())
	}
	return anchorWaveManifest(nodes...)
}

func anchorWaveWindowsManifest(nodeCount int) Manifest {
	nodes := make([]NodeConfig, nodeCount)
	for index := range nodes {
		nodes[index] = anchorWaveNodeWithID(fmt.Sprintf("anchor-windows-%d", index+1), index, PlatformWindowsAMD64, testWindowsCapability())
	}
	return anchorWaveManifest(nodes...)
}

func anchorWaveRoutingManifest() Manifest {
	return anchorWaveManifest(
		anchorWaveNodeWithID("anchor-linux", 0, PlatformLinuxAMD64, anchorWaveUbuntuCapability(), testDarlingCapability()),
		anchorWaveNodeWithID("anchor-windows", 1, PlatformWindowsAMD64, testWindowsCapability()),
		anchorWaveNodeWithID("anchor-darwin-amd64", 2, PlatformDarwinAMD64, testDarwinNativeCapability()),
		anchorWaveNodeWithID("anchor-darwin-arm64", 3, PlatformDarwinARM64, testDarwinNativeCapability()),
	)
}

func anchorWaveUnassignedManifest() Manifest {
	return anchorWaveManifest(
		anchorWaveNodeWithID("anchor-linux-saturated", 0, PlatformLinuxAMD64, anchorWaveUbuntuCapability()),
		anchorWaveNodeWithID("anchor-linux-darling", 1, PlatformLinuxAMD64, testDarlingCapability()),
		anchorWaveNodeWithID("anchor-windows", 2, PlatformWindowsAMD64, testWindowsCapability()),
	)
}

func anchorWaveNode(index int, platform Platform, capabilities ...CapabilityConfig) NodeConfig {
	return anchorWaveNodeWithID(fmt.Sprintf("anchor-node-%d", index+1), index, platform, capabilities...)
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

func anchorWaveNodeUnitCounts(plan PackingPlan) map[string]int {
	counts := make(map[string]int, len(plan.NodeShards))
	for _, shard := range plan.NodeShards {
		for _, wave := range shard.Waves {
			counts[shard.NodeID] += len(wave.UnitIDs)
		}
	}
	return counts
}

func anchorWaveUnitNodes(plan PackingPlan) map[string]string {
	assigned := make(map[string]string)
	for _, shard := range plan.NodeShards {
		for _, wave := range shard.Waves {
			for _, unitID := range wave.UnitIDs {
				assigned[unitID] = shard.NodeID
			}
		}
	}
	return assigned
}

func difference(left, right int) int {
	if left > right {
		return left - right
	}
	return right - left
}
