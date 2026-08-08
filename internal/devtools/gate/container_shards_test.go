package gate

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildContainerShardSetFromWorkloadPlanBindsFrozenLPTIdentity(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileRelease)
	workloadPlan := testWorkloadExecutionPlan(t, gatePlan)
	set, err := BuildContainerShardSetFromWorkloadPlan(
		gatePlan,
		workloadPlan,
		shardTestDigest('a'),
		shardTestDigest('b'),
	)
	if err != nil {
		t.Fatalf("BuildContainerShardSetFromWorkloadPlan() error = %v", err)
	}
	assertWorkloadContainerShardSetIdentity(t, set, workloadPlan)
	if err := set.ValidateStored(gatePlan); err != nil {
		t.Fatalf("ValidateStored() error = %v", err)
	}

	workloadPlan.Catalog.Workloads[0].CommandDigest = strings.Repeat("f", 64)
	if err := set.Validate(); err != nil {
		t.Fatalf("set retained caller alias after construction: %v", err)
	}
}

func assertWorkloadContainerShardSetIdentity(
	t *testing.T,
	set ContainerShardSet,
	workloadPlan WorkloadExecutionPlan,
) {
	t.Helper()
	if len(set.Shards) != len(workloadPlan.Shards) ||
		set.ShardsPerJob != len(workloadPlan.Shards) ||
		set.WorkloadPlanDigest != workloadPlan.PlanDigest {
		t.Fatalf("workload shard set identity = %#v", set)
	}
	for index, shard := range set.Shards {
		if shard.SchemaVersion != workloadContainerShardSchemaVersion ||
			shard.WorkloadPlanDigest != workloadPlan.PlanDigest ||
			shard.CatalogDigest != workloadPlan.CatalogDigest ||
			shard.LedgerGeneration != workloadPlan.LedgerGeneration ||
			shard.EstimatedDurationMS != workloadPlan.Shards[index].EstimatedDurationMS {
			t.Fatalf("workload shard %d identity = %#v", index, shard)
		}
	}
}

func TestWorkloadContainerShardSetRejectsSelfConsistentShardDrift(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileLocalFast)
	set, err := BuildContainerShardSetFromWorkloadPlan(
		gatePlan,
		testWorkloadExecutionPlan(t, gatePlan),
		shardTestDigest('a'),
		shardTestDigest('b'),
	)
	if err != nil {
		t.Fatal(err)
	}

	tampered := set
	tampered.Shards = append([]ContainerShard(nil), set.Shards...)
	tampered.Shards[0].EstimatedDurationMS++
	identity, err := containerShardIdentityDigest(tampered.Shards[0])
	if err != nil {
		t.Fatal(err)
	}
	tampered.Shards[0].IdentityDigest = identity
	if err := tampered.Validate(); err == nil {
		t.Fatal("Validate() accepted self-consistent estimated duration drift")
	}

}

func testWorkloadExecutionPlan(t *testing.T, gatePlan GatePlan) WorkloadExecutionPlan {
	t.Helper()
	var catalog WorkloadCatalog
	var err error
	if slices.Contains(requiredGateIDs(gatePlan.Profile), GateIDBackendNilness) {
		catalog, err = BuildExpandedWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
			GoPackages: []string{"./internal/alpha", "./internal/beta"},
		})
	} else {
		catalog, err = BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	}
	if err != nil {
		t.Fatalf("workload catalog: %v", err)
	}
	plan, err := BuildWorkloadExecutionPlanForWorkloads(
		gatePlan,
		catalog,
		DurationLedgerSnapshot{Generation: 11, Ledger: fastDurationLedger(catalog)},
		testLinuxPlanningContext(),
		allShardableWorkloadIDs(catalog),
	)
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlanForWorkloads() error = %v", err)
	}
	return plan
}

func TestCanonicalGateArgvDigestMatchesReceiptEncoding(t *testing.T) {
	got, err := canonicalGateArgvDigest(ProfileLocalFast, GateIDBackendTestWithGuard)
	if err != nil {
		t.Fatal(err)
	}
	const want = "sha256:61d69caed0b49003af9064b9de6a6b26c55279f7c70e68b42f21f175b2ec838b"
	if got != want {
		t.Fatalf("canonical gate argv digest = %q, want %q", got, want)
	}
}

func shardTestDigest(character rune) string { return "sha256:" + strings.Repeat(string(character), 64) }
