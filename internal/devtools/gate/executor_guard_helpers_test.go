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
	shards, err := BuildContainerShardSetFromWorkloadPlan(plan, testWorkloadExecutionPlan(t, plan), shardTestDigest('a'), shardTestDigest('b'))
	if err != nil {
		t.Fatal(err)
	}
	argv, err := ContainerShardExecutorArgv(plan, shards.Shards[0].GateIDs)
	if err != nil {
		t.Fatal(err)
	}
	assertStandaloneWorkerArgvPrefix(t, argv)
	parsed, err := parseExecutorPlanCommand(argv[2:])
	if err != nil || !parsed.shard || !slices.Equal(parsed.gateIDs, shards.Shards[0].GateIDs) {
		t.Fatalf("parse shard argv = %#v, %v", parsed, err)
	}
	bad := slices.Clone(argv[2:])
	bad[6] = "forged-gate"
	if _, err := parseExecutorPlanCommand(bad); err == nil {
		t.Fatal("parser accepted a forged shard gate list")
	}
	assertDynamicShardSubset(t, plan, shards.Shards[0].GateIDs)
}

func assertDynamicShardSubset(t *testing.T, plan GatePlan, gates []GateID) {
	t.Helper()
	subset := gates[1:]
	if len(subset) == 0 {
		t.Fatal("test requires a non-empty dynamic subset")
	}
	if _, err := ContainerShardExecutorArgv(plan, subset); err != nil {
		t.Fatalf("ContainerShardExecutorArgv rejected dynamic LPT subset %v: %v", subset, err)
	}
	for _, forged := range [][]GateID{append(slices.Clone(gates), GateIDReleaseLayeredCheck), append(slices.Clone(gates), gates[0])} {
		if _, err := ContainerShardExecutorArgv(plan, forged); err == nil {
			t.Fatalf("ContainerShardExecutorArgv accepted forged shard gates %v", forged)
		}
	}
}
