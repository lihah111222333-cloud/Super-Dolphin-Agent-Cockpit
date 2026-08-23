package heteroci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPackingPlanCanonicalDigestIsStableAndMatchesJSONOracle(t *testing.T) {
	plan := packingDigestPlan()
	want := packingDigestJSONOracle(t, plan)

	first, err := plan.CanonicalDigest()
	if err != nil {
		t.Fatalf("first CanonicalDigest() error = %v", err)
	}
	second, err := plan.CanonicalDigest()
	if err != nil {
		t.Fatalf("second CanonicalDigest() error = %v", err)
	}
	if first != second {
		t.Fatalf("repeated CanonicalDigest() values differ: first=%q second=%q", first, second)
	}
	if first != want {
		t.Fatalf("CanonicalDigest() = %q, want independent json.Marshal SHA-256 oracle %q", first, want)
	}
	assertPackingDigestFormat(t, first)
}

func TestPackingPlanCanonicalDigestChangesForEveryPlanFieldAndOrderMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PackingPlan)
	}{
		{name: "schema root", mutate: func(plan *PackingPlan) { plan.SchemaVersion = "heteroci-packing-plan/v1" }},
		{name: "authority root", mutate: func(plan *PackingPlan) { plan.Authority = "REMOTE" }},
		{name: "capture root", mutate: func(plan *PackingPlan) { plan.SnapshotCapturedAtUnixMilli++ }},
		{name: "unassigned root value", mutate: func(plan *PackingPlan) { plan.UnassignedUnitIDs[0] = "unit-z-mutated" }},
		{name: "unassigned order", mutate: func(plan *PackingPlan) {
			plan.UnassignedUnitIDs[0], plan.UnassignedUnitIDs[1] = plan.UnassignedUnitIDs[1], plan.UnassignedUnitIDs[0]
		}},
		{name: "shard node", mutate: func(plan *PackingPlan) { plan.NodeShards[0].NodeID = "node-a-mutated" }},
		{name: "shard order", mutate: func(plan *PackingPlan) {
			plan.NodeShards[0], plan.NodeShards[1] = plan.NodeShards[1], plan.NodeShards[0]
		}},
		{name: "shard resource makespan", mutate: func(plan *PackingPlan) { plan.NodeShards[0].EstimatedMakespanMS++ }},
		{name: "wave number", mutate: func(plan *PackingPlan) { plan.NodeShards[0].Waves[0].Wave++ }},
		{name: "wave order", mutate: func(plan *PackingPlan) {
			plan.NodeShards[0].Waves[0], plan.NodeShards[0].Waves[1] = plan.NodeShards[0].Waves[1], plan.NodeShards[0].Waves[0]
		}},
		{name: "unit value", mutate: func(plan *PackingPlan) { plan.NodeShards[0].Waves[0].UnitIDs[0] = "unit-a-mutated" }},
		{name: "unit order", mutate: func(plan *PackingPlan) {
			plan.NodeShards[0].Waves[0].UnitIDs[0], plan.NodeShards[0].Waves[0].UnitIDs[1] = plan.NodeShards[0].Waves[0].UnitIDs[1], plan.NodeShards[0].Waves[0].UnitIDs[0]
		}},
		{name: "cpu resource", mutate: func(plan *PackingPlan) { plan.NodeShards[0].Waves[0].CPUMilli++ }},
		{name: "memory resource", mutate: func(plan *PackingPlan) { plan.NodeShards[0].Waves[0].MemoryBytes++ }},
		{name: "concurrency resource", mutate: func(plan *PackingPlan) { plan.NodeShards[0].Waves[0].Concurrency++ }},
		{name: "duration resource", mutate: func(plan *PackingPlan) { plan.NodeShards[0].Waves[0].EstimatedDurationMS++ }},
	}

	base := packingDigestPlan()
	baseDigest := packingDigestValue(t, base)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := clonePackingDigestPlan(base)
			tt.mutate(&candidate)
			got := packingDigestValue(t, candidate)
			if got == baseDigest {
				t.Fatalf("digest stayed unchanged after %s mutation: %q", tt.name, got)
			}
			if want := packingDigestJSONOracle(t, candidate); got != want {
				t.Fatalf("digest after %s mutation = %q, want independent oracle %q", tt.name, got, want)
			}
		})
	}
}

func TestPackingPlanCanonicalDigestUsesEquivalentStructureAndDoesNotMutate(t *testing.T) {
	first := packingDigestPlan()
	second := packingDigestPlan()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("independently constructed equivalent plans are not deeply equal")
	}
	firstBefore := clonePackingDigestPlan(first)
	secondBefore := clonePackingDigestPlan(second)

	firstDigest := packingDigestValue(t, first)
	secondDigest := packingDigestValue(t, second)
	if firstDigest != secondDigest {
		t.Fatalf("equivalent plans have different digests: first=%q second=%q", firstDigest, secondDigest)
	}
	if !reflect.DeepEqual(first, firstBefore) || !reflect.DeepEqual(second, secondBefore) {
		t.Fatal("CanonicalDigest mutated an input plan")
	}

	copyOfFirst := clonePackingDigestPlan(first)
	copyOfFirst.NodeShards[0].Waves[0].UnitIDs[0] = "copy-only-mutation"
	if first.NodeShards[0].Waves[0].UnitIDs[0] == "copy-only-mutation" {
		t.Fatal("deep copy shares nested UnitIDs storage with the source plan")
	}
}

func TestPackingPlanCanonicalDigestDoesNotNormalizeOrderBeforeValidation(t *testing.T) {
	fixture := newPackingValidationTwoNodeFixture(t)
	canonicalDigest := packingDigestValue(t, fixture.plan)
	shuffled := clonePackingDigestPlan(fixture.plan)
	shuffled.NodeShards[0], shuffled.NodeShards[1] = shuffled.NodeShards[1], shuffled.NodeShards[0]

	shuffledDigest := packingDigestValue(t, shuffled)
	if shuffledDigest == canonicalDigest {
		t.Fatalf("shuffled noncanonical plan was normalized into canonical digest %q", shuffledDigest)
	}
	if err := shuffled.ValidateAgainst(fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.heartbeatTTL); err == nil {
		t.Fatal("ValidateAgainst unexpectedly accepted shuffled noncanonical node_shards")
	}
}

func packingDigestPlan() PackingPlan {
	return PackingPlan{
		SchemaVersion:               PackingPlanSchemaVersion,
		Authority:                   AuthorityLocalNonAuthoritative,
		SnapshotCapturedAtUnixMilli: 1_700_000_000_123,
		NodeShards: []NodeShard{
			{
				NodeID: "node-a",
				Waves: []ExecutionWave{
					{Wave: 0, UnitIDs: []string{"unit-a", "unit-b"}, CPUMilli: 1_500, MemoryBytes: 512, Concurrency: 2, EstimatedDurationMS: 300},
					{Wave: 1, UnitIDs: []string{"unit-c"}, CPUMilli: 700, MemoryBytes: 256, Concurrency: 1, EstimatedDurationMS: 200},
				},
				EstimatedMakespanMS: 500,
			},
			{
				NodeID:              "node-b",
				Waves:               []ExecutionWave{{Wave: 0, UnitIDs: []string{"unit-d"}, CPUMilli: 900, MemoryBytes: 384, Concurrency: 1, EstimatedDurationMS: 400}},
				EstimatedMakespanMS: 400,
			},
		},
		UnassignedUnitIDs: []string{"unit-y", "unit-z"},
	}
}

func packingDigestJSONOracle(t *testing.T, plan PackingPlan) string {
	t.Helper()
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("json.Marshal(PackingPlan) error = %v", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func packingDigestValue(t *testing.T, plan PackingPlan) string {
	t.Helper()
	digest, err := plan.CanonicalDigest()
	if err != nil {
		t.Fatalf("PackingPlan.CanonicalDigest() error = %v", err)
	}
	return digest
}

func assertPackingDigestFormat(t *testing.T, digest string) {
	t.Helper()
	if !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("digest = %q, want sha256: prefix", digest)
	}
	hexPart := strings.TrimPrefix(digest, "sha256:")
	if len(hexPart) != sha256.Size*2 {
		t.Fatalf("digest hex length = %d, want %d", len(hexPart), sha256.Size*2)
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		t.Fatalf("digest hex = %q is invalid: %v", hexPart, err)
	}
	if hexPart != strings.ToLower(hexPart) {
		t.Fatalf("digest hex = %q is not lowercase", hexPart)
	}
}

func clonePackingDigestPlan(source PackingPlan) PackingPlan {
	clone := source
	if source.NodeShards != nil {
		clone.NodeShards = make([]NodeShard, len(source.NodeShards))
		copy(clone.NodeShards, source.NodeShards)
	}
	for shardIndex := range clone.NodeShards {
		clone.NodeShards[shardIndex].Waves = append([]ExecutionWave(nil), source.NodeShards[shardIndex].Waves...)
		for waveIndex := range clone.NodeShards[shardIndex].Waves {
			clone.NodeShards[shardIndex].Waves[waveIndex].UnitIDs = append([]string(nil), source.NodeShards[shardIndex].Waves[waveIndex].UnitIDs...)
		}
	}
	if source.UnassignedUnitIDs != nil {
		clone.UnassignedUnitIDs = append([]string(nil), source.UnassignedUnitIDs...)
	}
	return clone
}
