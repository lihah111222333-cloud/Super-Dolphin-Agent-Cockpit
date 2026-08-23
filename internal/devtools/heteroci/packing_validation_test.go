package heteroci

import (
	"testing"
	"time"
)

const packingValidationMaxInt64 = int64(1<<63 - 1)

type packingValidationFixture struct {
	now          time.Time
	heartbeatTTL time.Duration
	manifest     Manifest
	snapshot     SnapshotSet
	units        []ExecutionUnit
	plan         PackingPlan
}

type packingValidationMutation struct {
	name    string
	fixture packingValidationFixture
	mutate  func(*packingValidationFixture)
}

func TestPackingPlanValidateAgainstAcceptsCanonicalMixedRuntimePlacement(t *testing.T) {
	fixture := newPackingValidationMixedFixture(t)
	assertPackingPlanValid(t, fixture)

	differentWaves := clonePackingValidationFixture(fixture)
	differentWaves.plan.NodeShards[0].Waves = []ExecutionWave{
		packingValidationWave(0, []string{"unit-a-ubuntu"}, differentWaves.units),
		packingValidationWave(1, []string{"unit-b-wine"}, differentWaves.units),
	}
	differentWaves.plan.NodeShards[0].EstimatedMakespanMS = 2_000
	assertPackingPlanValid(t, differentWaves)
}

func TestPackingPlanValidateAgainstRejectsHeaderAndNilOrEmptySlices(t *testing.T) {
	fixture := newPackingValidationMixedFixture(t)
	mutations := []packingValidationMutation{
		{name: "schema empty", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.SchemaVersion = "" }},
		{name: "schema old", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.SchemaVersion = "heteroci-packing-plan/v0" }},
		{name: "schema unknown", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.SchemaVersion = "heteroci-packing-plan/v99" }},
		{name: "authority empty", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.Authority = "" }},
		{name: "authority remote", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.Authority = "REMOTE" }},
		{name: "capture zero", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.SnapshotCapturedAtUnixMilli = 0 }},
		{name: "capture drift", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.SnapshotCapturedAtUnixMilli++ }},
		{name: "capture future", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.plan.SnapshotCapturedAtUnixMilli = f.now.Add(time.Second).UnixMilli()
		}},
		{name: "node shards nil", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards = nil }},
		{name: "node shards empty", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards = []NodeShard{} }},
		{name: "execution units nil", fixture: fixture, mutate: func(f *packingValidationFixture) { f.units = nil }},
		{name: "waves nil", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves = nil }},
		{name: "waves empty", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves = []ExecutionWave{} }},
		{name: "unit ids nil", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves[0].UnitIDs = nil }},
		{name: "unit ids empty", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves[0].UnitIDs = []string{} }},
		{name: "unassigned ids nil", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.UnassignedUnitIDs = nil }},
	}
	runPackingValidationMutations(t, mutations)
}

func TestPackingPlanValidateAgainstRejectsNodeWaveAndUnitCanonicalDrift(t *testing.T) {
	oneNode := newPackingValidationMixedFixture(t)
	twoNodes := newPackingValidationTwoNodeFixture(t)
	twoWaves := clonePackingValidationFixture(oneNode)
	twoWaves.plan.NodeShards[0].Waves = []ExecutionWave{
		packingValidationWave(0, []string{"unit-a-ubuntu"}, twoWaves.units),
		packingValidationWave(1, []string{"unit-b-wine"}, twoWaves.units),
	}
	twoWaves.plan.NodeShards[0].EstimatedMakespanMS = 2_000

	mutations := []packingValidationMutation{
		{name: "node shard order", fixture: twoNodes, mutate: func(f *packingValidationFixture) {
			f.plan.NodeShards[0], f.plan.NodeShards[1] = f.plan.NodeShards[1], f.plan.NodeShards[0]
		}},
		{name: "duplicate node shard", fixture: twoNodes, mutate: func(f *packingValidationFixture) {
			f.plan.NodeShards = append(f.plan.NodeShards, cloneNodeShard(f.plan.NodeShards[0]))
		}},
		{name: "unknown node", fixture: oneNode, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].NodeID = "unknown-node" }},
		{name: "wave gap", fixture: twoWaves, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves[0].Wave = 1 }},
		{name: "wave order", fixture: twoWaves, mutate: func(f *packingValidationFixture) {
			f.plan.NodeShards[0].Waves[0], f.plan.NodeShards[0].Waves[1] = f.plan.NodeShards[0].Waves[1], f.plan.NodeShards[0].Waves[0]
		}},
		{name: "unit id order", fixture: oneNode, mutate: func(f *packingValidationFixture) {
			f.plan.NodeShards[0].Waves[0].UnitIDs = []string{"unit-b-wine", "unit-a-ubuntu"}
		}},
		{name: "duplicate unit id", fixture: oneNode, mutate: func(f *packingValidationFixture) {
			f.plan.NodeShards[0].Waves[0].UnitIDs = []string{"unit-a-ubuntu", "unit-a-ubuntu"}
		}},
	}
	runPackingValidationMutations(t, mutations)
}

func TestPackingPlanValidateAgainstRejectsRecomputedAndCapacityDrift(t *testing.T) {
	fixture := newPackingValidationMixedFixture(t)
	mutations := []packingValidationMutation{
		{name: "concurrency drift", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves[0].Concurrency = 1 }},
		{name: "cpu drift", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves[0].CPUMilli++ }},
		{name: "memory drift", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves[0].MemoryBytes++ }},
		{name: "duration drift", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].Waves[0].EstimatedDurationMS++ }},
		{name: "makespan drift", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.NodeShards[0].EstimatedMakespanMS++ }},
		{name: "cpu over available", fixture: fixture, mutate: packingValidationMakeCPUOverAvailable},
		{name: "memory over available", fixture: fixture, mutate: packingValidationMakeMemoryOverAvailable},
		{name: "concurrency over available", fixture: fixture, mutate: packingValidationMakeConcurrencyOverAvailable},
	}
	runPackingValidationMutations(t, mutations)
}

func TestPackingPlanValidateAgainstRejectsRuntimeMismatchAndOfflineNode(t *testing.T) {
	fixture := newPackingValidationMixedFixture(t)
	mutations := []packingValidationMutation{
		{name: "runtime node mismatch", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.units[0].RequiredRuntime = RuntimeUbuntuWineContainer
			f.units[0].TargetPlatform = PlatformWindowsAMD64
		}},
		{name: "active runtime missing", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.snapshot.Nodes[0].ActiveCapabilities = []ActiveCapabilitySnapshot{{Runtime: RuntimeUbuntuContainer, ImageDigest: testImageDigest}}
		}},
		{name: "offline node", fixture: fixture, mutate: packingValidationMarkNodeOffline},
	}
	runPackingValidationMutations(t, mutations)
}

func TestPackingPlanValidateAgainstRejectsAssignmentSetDrift(t *testing.T) {
	fixture := newPackingValidationMixedFixture(t)
	mutations := []packingValidationMutation{
		{name: "missing assigned unit", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.plan.NodeShards[0].Waves[0].UnitIDs = []string{"unit-a-ubuntu"}
		}},
		{name: "unknown assigned unit", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.plan.NodeShards[0].Waves[0].UnitIDs = []string{"unit-a-ubuntu", "unit-b-wine", "unit-z-extra"}
		}},
		{name: "assigned unit duplicate", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.plan.NodeShards[0].Waves[0].UnitIDs = []string{"unit-a-ubuntu", "unit-b-wine", "unit-b-wine"}
		}},
		{name: "assigned and unassigned duplicate", fixture: fixture, mutate: func(f *packingValidationFixture) { f.plan.UnassignedUnitIDs = []string{"unit-a-ubuntu"} }},
	}
	runPackingValidationMutations(t, mutations)
}

func TestPackingPlanValidateAgainstAcceptsOnlyTrueUnassignedUnits(t *testing.T) {
	fixture := newPackingValidationUnassignedFixture(t)
	assertPackingPlanValid(t, fixture)

	mutations := []packingValidationMutation{
		{name: "unassigned noncanonical", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.plan.UnassignedUnitIDs[0], f.plan.UnassignedUnitIDs[1] = f.plan.UnassignedUnitIDs[1], f.plan.UnassignedUnitIDs[0]
		}},
		{name: "unassigned duplicate", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.plan.UnassignedUnitIDs = append(f.plan.UnassignedUnitIDs, f.plan.UnassignedUnitIDs[0])
		}},
		{name: "unassigned unknown", fixture: fixture, mutate: func(f *packingValidationFixture) {
			f.plan.UnassignedUnitIDs = []string{"unit-a-oversize", "unit-b-no-capability", "unit-z-extra"}
		}},
	}
	runPackingValidationMutations(t, mutations)

	fitClaim := newPackingValidationMixedFixture(t)
	fitClaim.plan.NodeShards[0].Waves[0] = packingValidationWave(0, []string{"unit-b-wine"}, fitClaim.units)
	fitClaim.plan.UnassignedUnitIDs = []string{"unit-a-ubuntu"}
	assertPackingPlanInvalid(t, fitClaim)
}

func TestPackingPlanValidateAgainstRejectsCheckedArithmeticOverflow(t *testing.T) {
	mutations := []packingValidationMutation{
		{name: "cpu sum overflow", fixture: newPackingValidationOverflowFixture(t, true, false), mutate: func(f *packingValidationFixture) {}},
		{name: "memory sum overflow", fixture: newPackingValidationOverflowFixture(t, false, true), mutate: func(f *packingValidationFixture) {}},
		{name: "makespan sum overflow", fixture: newPackingValidationMakespanOverflowFixture(t), mutate: func(f *packingValidationFixture) {}},
	}
	runPackingValidationMutations(t, mutations)
}

func newPackingValidationMixedFixture(t *testing.T) packingValidationFixture {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	manifest := snapshotTestManifest(4)
	snapshot := snapshotForManifest(manifest, now)
	if err := manifest.Validate(); err != nil {
		t.Fatalf("mixed runtime manifest is invalid: %v", err)
	}
	if err := snapshot.ValidateAgainst(manifest, now, time.Minute); err != nil {
		t.Fatalf("mixed runtime snapshot is invalid: %v", err)
	}
	units := []ExecutionUnit{
		executionUnitTestUnit("unit-a-ubuntu", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		executionUnitTestUnit("unit-b-wine", RuntimeUbuntuContainer, PlatformLinuxAMD64),
	}
	plan := PackingPlan{
		SchemaVersion:               PackingPlanSchemaVersion,
		Authority:                   AuthorityLocalNonAuthoritative,
		SnapshotCapturedAtUnixMilli: snapshot.CapturedAtUnixMilli,
		NodeShards: []NodeShard{{
			NodeID:              manifest.Nodes[0].NodeID,
			Waves:               []ExecutionWave{packingValidationWave(0, []string{"unit-a-ubuntu", "unit-b-wine"}, units)},
			EstimatedMakespanMS: 1_000,
		}},
		UnassignedUnitIDs: []string{},
	}
	return packingValidationFixture{now: now, heartbeatTTL: time.Minute, manifest: manifest, snapshot: snapshot, units: units, plan: plan}
}

func newPackingValidationTwoNodeFixture(t *testing.T) packingValidationFixture {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	manifest := testManifest(2)
	snapshot := snapshotForManifest(manifest, now)
	units := []ExecutionUnit{
		executionUnitTestUnit("unit-a", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		executionUnitTestUnit("unit-b", RuntimeUbuntuContainer, PlatformLinuxAMD64),
	}
	plan := PackingPlan{
		SchemaVersion:               PackingPlanSchemaVersion,
		Authority:                   AuthorityLocalNonAuthoritative,
		SnapshotCapturedAtUnixMilli: snapshot.CapturedAtUnixMilli,
		NodeShards: []NodeShard{
			{NodeID: manifest.Nodes[0].NodeID, Waves: []ExecutionWave{packingValidationWave(0, []string{"unit-a"}, units)}, EstimatedMakespanMS: 1_000},
			{NodeID: manifest.Nodes[1].NodeID, Waves: []ExecutionWave{packingValidationWave(0, []string{"unit-b"}, units)}, EstimatedMakespanMS: 1_000},
		},
		UnassignedUnitIDs: []string{},
	}
	return packingValidationFixture{now: now, heartbeatTTL: time.Minute, manifest: manifest, snapshot: snapshot, units: units, plan: plan}
}

func newPackingValidationUnassignedFixture(t *testing.T) packingValidationFixture {
	t.Helper()
	fixture := newPackingValidationMixedFixture(t)
	fixture.units = []ExecutionUnit{
		executionUnitTestUnit("unit-a-oversize", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		executionUnitTestUnit("unit-b-no-capability", RuntimeUbuntuWineContainer, PlatformWindowsAMD64),
	}
	fixture.units[0].CPUMilli = fixture.snapshot.Nodes[0].Available.CPUMilli + 1
	fixture.units[1].CPUMilli = fixture.snapshot.Nodes[1].Available.CPUMilli + 1
	fixture.plan.NodeShards = []NodeShard{}
	fixture.plan.UnassignedUnitIDs = []string{"unit-a-oversize", "unit-b-no-capability"}
	return fixture
}

func newPackingValidationOverflowFixture(t *testing.T, cpuOverflow, memoryOverflow bool) packingValidationFixture {
	fixture := newPackingValidationMixedFixture(t)
	fixture.units = []ExecutionUnit{
		executionUnitTestUnit("unit-a", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		executionUnitTestUnit("unit-b", RuntimeUbuntuContainer, PlatformLinuxAMD64),
	}
	if cpuOverflow {
		fixture.units[0].CPUMilli = packingValidationMaxInt64
		fixture.units[1].CPUMilli = packingValidationMaxInt64
	}
	if memoryOverflow {
		fixture.units[0].MemoryBytes = packingValidationMaxInt64
		fixture.units[1].MemoryBytes = packingValidationMaxInt64
	}
	wave := packingValidationWave(0, []string{"unit-a", "unit-b"}, fixture.units)
	if cpuOverflow {
		wave.CPUMilli = 1
	}
	if memoryOverflow {
		wave.MemoryBytes = 1
	}
	fixture.plan.NodeShards[0].Waves = []ExecutionWave{wave}
	fixture.plan.NodeShards[0].EstimatedMakespanMS = wave.EstimatedDurationMS
	return fixture
}

func newPackingValidationMakespanOverflowFixture(t *testing.T) packingValidationFixture {
	fixture := newPackingValidationMixedFixture(t)
	fixture.units = []ExecutionUnit{
		executionUnitTestUnit("unit-a", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		executionUnitTestUnit("unit-b", RuntimeUbuntuContainer, PlatformLinuxAMD64),
	}
	fixture.units[0].EstimatedDurationMS = packingValidationMaxInt64
	fixture.units[1].EstimatedDurationMS = packingValidationMaxInt64
	fixture.plan.NodeShards[0].Waves = []ExecutionWave{
		packingValidationWave(0, []string{"unit-a"}, fixture.units),
		packingValidationWave(1, []string{"unit-b"}, fixture.units),
	}
	fixture.plan.NodeShards[0].EstimatedMakespanMS = packingValidationMaxInt64
	return fixture
}

func packingValidationWave(wave int, ids []string, units []ExecutionUnit) ExecutionWave {
	result := ExecutionWave{Wave: wave, UnitIDs: append([]string(nil), ids...)}
	for _, id := range ids {
		for _, unit := range units {
			if unit.UnitID != id {
				continue
			}
			result.CPUMilli += unit.CPUMilli
			result.MemoryBytes += unit.MemoryBytes
			result.Concurrency++
			if unit.EstimatedDurationMS > result.EstimatedDurationMS {
				result.EstimatedDurationMS = unit.EstimatedDurationMS
			}
		}
	}
	return result
}

func clonePackingValidationFixture(source packingValidationFixture) packingValidationFixture {
	clone := source
	clone.manifest.Nodes = append([]NodeConfig(nil), source.manifest.Nodes...)
	for index := range clone.manifest.Nodes {
		clone.manifest.Nodes[index].Capabilities = append([]CapabilityConfig(nil), source.manifest.Nodes[index].Capabilities...)
	}
	clone.snapshot = cloneSnapshotSet(source.snapshot)
	clone.units = append([]ExecutionUnit(nil), source.units...)
	clone.plan = clonePackingPlan(source.plan)
	return clone
}

func clonePackingPlan(source PackingPlan) PackingPlan {
	clone := source
	if source.NodeShards != nil {
		clone.NodeShards = make([]NodeShard, len(source.NodeShards))
		copy(clone.NodeShards, source.NodeShards)
	}
	for index := range clone.NodeShards {
		clone.NodeShards[index].Waves = append([]ExecutionWave(nil), source.NodeShards[index].Waves...)
		for waveIndex := range clone.NodeShards[index].Waves {
			clone.NodeShards[index].Waves[waveIndex].UnitIDs = append([]string(nil), source.NodeShards[index].Waves[waveIndex].UnitIDs...)
		}
	}
	if source.UnassignedUnitIDs != nil {
		clone.UnassignedUnitIDs = make([]string, len(source.UnassignedUnitIDs))
		copy(clone.UnassignedUnitIDs, source.UnassignedUnitIDs)
	}
	return clone
}

func cloneNodeShard(source NodeShard) NodeShard {
	clone := source
	clone.Waves = append([]ExecutionWave(nil), source.Waves...)
	for index := range clone.Waves {
		clone.Waves[index].UnitIDs = append([]string(nil), source.Waves[index].UnitIDs...)
	}
	return clone
}

func runPackingValidationMutations(t *testing.T, mutations []packingValidationMutation) {
	t.Helper()
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := clonePackingValidationFixture(mutation.fixture)
			mutation.mutate(&candidate)
			assertPackingPlanInvalid(t, candidate)
		})
	}
}

func assertPackingPlanValid(t *testing.T, fixture packingValidationFixture) {
	t.Helper()
	if err := fixture.plan.ValidateAgainst(fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.heartbeatTTL); err != nil {
		t.Fatalf("PackingPlan.ValidateAgainst() unexpected error: %v", err)
	}
}

func assertPackingPlanInvalid(t *testing.T, fixture packingValidationFixture) {
	t.Helper()
	if err := fixture.plan.ValidateAgainst(fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.heartbeatTTL); err == nil {
		t.Fatal("PackingPlan.ValidateAgainst() unexpectedly accepted invalid plan")
	}
}

func packingValidationMakeCPUOverAvailable(fixture *packingValidationFixture) {
	fixture.units[0].CPUMilli = fixture.snapshot.Nodes[0].Available.CPUMilli + 1
	wave := &fixture.plan.NodeShards[0].Waves[0]
	wave.CPUMilli = fixture.units[0].CPUMilli + fixture.units[1].CPUMilli
}

func packingValidationMakeMemoryOverAvailable(fixture *packingValidationFixture) {
	fixture.units[0].MemoryBytes = fixture.snapshot.Nodes[0].Available.MemoryBytes + 1
	wave := &fixture.plan.NodeShards[0].Waves[0]
	wave.MemoryBytes = fixture.units[0].MemoryBytes + fixture.units[1].MemoryBytes
}

func packingValidationMakeConcurrencyOverAvailable(fixture *packingValidationFixture) {
	for index := range 3 {
		id := string(rune('c' + index))
		fixture.units = append(fixture.units, executionUnitTestUnit("unit-"+id, RuntimeUbuntuContainer, PlatformLinuxAMD64))
	}
	ids := []string{"unit-a-ubuntu", "unit-b-wine", "unit-c", "unit-d", "unit-e"}
	fixture.plan.NodeShards[0].Waves[0] = packingValidationWave(0, ids, fixture.units)
}

func packingValidationMarkNodeOffline(fixture *packingValidationFixture) {
	node := &fixture.snapshot.Nodes[0]
	node.Status = NodeStatusOffline
	node.ProbeBootID = ""
	node.Available = ResourceCapacity{}
	node.ActiveCapabilities = []ActiveCapabilitySnapshot{}
}
