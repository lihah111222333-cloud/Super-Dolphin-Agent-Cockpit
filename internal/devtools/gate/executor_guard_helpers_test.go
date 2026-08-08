package gate

import (
	"slices"
	"strings"
	"testing"
)

func assertRaceSensitiveRegistries(t *testing.T, patterns, prefixes []string) {
	t.Helper()
	if len(patterns) == 0 || len(patterns) != len(prefixes) {
		t.Fatalf("race registry lengths = patterns:%d prefixes:%d", len(patterns), len(prefixes))
	}
	for index, prefix := range prefixes {
		assertRaceRegistryEntry(t, patterns[index], prefix, index)
	}
	if slices.Contains(patterns, "./cmd/...") || slices.Contains(patterns, "./cmd/agent-terminal/...") {
		t.Fatalf("race registry includes agent-terminal through an unbounded command pattern: %v", patterns)
	}
	if !slices.Contains(patterns, "./internal/devtools/remoteci/...") {
		t.Fatalf("race registry omits the remote CI coordinator: %v", patterns)
	}
}

func assertRaceRegistryEntry(t *testing.T, pattern, prefix string, index int) {
	t.Helper()
	exact := "./" + strings.TrimSuffix(prefix, "/")
	recursive := "./" + prefix + "..."
	if prefix == "" || !strings.HasSuffix(prefix, "/") || (pattern != exact && pattern != recursive) {
		t.Fatalf("race registry entry %d = pattern:%q prefix:%q", index, pattern, prefix)
	}
}

func assertContainerShardExecutorSubset(t *testing.T, plan GatePlan) {
	t.Helper()
	workloadPlan := testWorkloadExecutionPlan(t, plan)
	shards, err := BuildContainerShardSetFromWorkloadPlan(plan, workloadPlan, shardTestDigest('a'), shardTestDigest('b'))
	if err != nil {
		t.Fatal(err)
	}
	argv := testShardManifestArgv(t, plan, shards.Shards[0], workloadPlan)
	assertStandaloneWorkerArgvPrefix(t, argv)
	parsed, err := parseExecutorPlanCommand(argv[2:])
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.shard || parsed.manifestPath != ExecutorShardExecutionManifestPath || parsed.manifestDigest == "" {
		t.Fatalf("parse shard argv = %#v, %v", parsed, err)
	}
	if err := validateContainerShardGateIDs(plan.Profile, shards.Shards[0].GateIDs); err != nil {
		t.Fatalf("manifest gate IDs 被拒绝: %v", err)
	}
	bad := slices.Clone(argv[2:])
	bad[8] = "forged-gate"
	if _, err := parseExecutorPlanCommand(bad); err == nil {
		t.Fatal("parser accepted a forged manifest digest")
	}
	assertDynamicShardSubset(t, plan, shards.Shards[0].GateIDs)
}

func testShardManifestArgv(t *testing.T, plan GatePlan, shard ContainerShard, workloadPlan WorkloadExecutionPlan) []string {
	t.Helper()
	allowed := make(map[GateID]struct{}, len(shard.GateIDs))
	for _, id := range shard.GateIDs {
		allowed[id] = struct{}{}
	}
	groups := make([]CompileGroup, 0, len(workloadPlan.CompileGroups))
	for _, group := range workloadPlan.CompileGroups {
		members := 0
		for _, id := range group.WorkloadIDs {
			if _, ok := allowed[id]; ok {
				members++
			}
		}
		if members == 0 {
			continue
		}
		if members != len(group.WorkloadIDs) {
			t.Fatalf("compile group %q crosses the test shard", group.GroupID)
		}
		groups = append(groups, group)
	}
	manifest := ShardExecutionManifest{
		SchemaVersion: ShardExecutionManifestSchemaVersion,
		Profile:       plan.Profile,
		PlanDigest:    plan.PlanDigest,
		ShardIdentity: shard.IdentityDigest,
		SourceTreeSHA: plan.Source.SourceTreeSHA,
		GateIDs:       slices.Clone(shard.GateIDs),
		CompileGroups: groups,
	}
	_, digest, err := EncodeShardExecutionManifest(manifest)
	if err != nil {
		t.Fatalf("encode test shard manifest: %v", err)
	}
	return []string{containerGateBinary, containerWorkerNamespace, "run-shard", "--profile", string(plan.Profile),
		"--plan-digest", plan.PlanDigest, "--manifest-path", ExecutorShardExecutionManifestPath,
		"--manifest-digest", digest}
}

func assertDynamicShardSubset(t *testing.T, plan GatePlan, gates []GateID) {
	t.Helper()
	subset := gates[1:]
	if len(subset) == 0 {
		t.Fatal("test requires a non-empty dynamic subset")
	}
	if err := validateContainerShardGateIDs(plan.Profile, subset); err != nil {
		t.Fatalf("manifest gate validation rejected dynamic LPT subset %v: %v", subset, err)
	}
	for _, forged := range [][]GateID{append(slices.Clone(gates), GateIDReleaseLayeredCheck), append(slices.Clone(gates), gates[0])} {
		if err := validateContainerShardGateIDs(plan.Profile, forged); err == nil {
			t.Fatalf("manifest gate validation accepted forged shard gates %v", forged)
		}
	}
}
