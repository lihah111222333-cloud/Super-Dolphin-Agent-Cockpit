package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildContainerShardSetUsesProfileSpecificCanonicalGroups(t *testing.T) {
	if groups := canonicalContainerShardGroups(ProfileRelease, legacyContainerShardCount); len(groups) != legacyContainerShardCount {
		t.Fatalf("canonical release shard groups = %d, want %d", len(groups), legacyContainerShardCount)
	}
	if groups := canonicalContainerShardGroups(ProfileLocalFast, legacyContainerShardCount); len(groups) != legacyContainerShardCount {
		t.Fatalf("canonical local shard groups = %d, want %d", len(groups), legacyContainerShardCount)
	}
	for _, profile := range []Profile{ProfilePush, ProfileRemoteRequired, ProfilePromotion} {
		if groups := canonicalContainerShardGroups(profile, legacyContainerShardCount); len(groups) != legacyContainerShardCount {
			t.Fatalf("canonical %s shard groups = %d, want %d", profile, len(groups), legacyContainerShardCount)
		}
	}
	set := testContainerShardSet(t, ProfileRelease)
	if len(set.Shards) != legacyContainerShardCount {
		t.Fatalf("shards = %d, want %d", len(set.Shards), legacyContainerShardCount)
	}
	want := [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendPreflight, GateIDFrontendE2E, GateIDFrontendFullTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify,
			GateIDSQLCVerify, GateIDCodemapCheck, GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
		{GateIDBackendTestWithGuard, GateIDBackendNilness},
		{GateIDBackendTestGuardWithRace},
	}
	normal := testContainerShardSet(t, ProfileLocalFast)
	normalWant := [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify},
		{GateIDBackendTestWithGuard},
		{GateIDSQLCVerify, GateIDCodemapCheck, GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
	push := testContainerShardSet(t, ProfilePush)
	remote := testContainerShardSet(t, ProfileRemoteRequired)
	promotion := testContainerShardSet(t, ProfilePromotion)
	pushWant := [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendLint},
		{GateIDBackendTestWithGuard},
		{GateIDSQLCVerify, GateIDCodemapCheck, GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
	remoteWant := [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify},
		{GateIDBackendTestWithGuard},
		{GateIDFrontendPreflight, GateIDSQLCVerify, GateIDCodemapCheck, GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
	assertContainerShardGateGroups(t, set, want)
	assertContainerShardGateGroups(t, normal, normalWant)
	assertContainerShardGateGroups(t, push, pushWant)
	assertContainerShardGateGroups(t, remote, remoteWant)
	assertContainerShardGateGroups(t, promotion, remoteWant)
	assertShardSetRejectsSelfConsistentMissingGate(t, set)
}

func TestBuildContainerShardSetWithCountBindsRequestedShardCount(t *testing.T) {
	plan := mustBuildPlan(t, ProfileLocalFast)
	for _, count := range []uint8{1, 2, 3, 5} {
		t.Run(fmt.Sprintf("count-%d", count), func(t *testing.T) {
			assertContainerShardCountBinding(t, plan, count)
		})
	}
}

func assertContainerShardCountBinding(t *testing.T, plan GatePlan, count uint8) {
	t.Helper()
	set, err := BuildContainerShardSetWithCount(plan, shardTestDigest('a'), shardTestDigest('b'), count)
	if err != nil {
		t.Fatal(err)
	}
	if set.ShardsPerJob != count || len(set.Shards) != int(count) {
		t.Fatalf("shards_per_job=%d shards=%d, want %d", set.ShardsPerJob, len(set.Shards), count)
	}
	for _, shard := range set.Shards {
		if shard.ShardsPerJob != count {
			t.Fatalf("shard %d shards_per_job=%d, want %d", shard.Index, shard.ShardsPerJob, count)
		}
		if count >= 2 && slices.Contains(shard.GateIDs, GateIDBackendTestGuardWithRace) && len(shard.GateIDs) != 1 {
			t.Fatalf("count=%d did not isolate the race gate: %#v", count, shard.GateIDs)
		}
	}
}

func TestBuildContainerShardSetWithCountRejectsInvalidCount(t *testing.T) {
	plan := mustBuildPlan(t, ProfileLocalFast)
	for _, count := range []uint8{0, 65, uint8(len(plan.Gates) + 1)} {
		if _, err := BuildContainerShardSetWithCount(plan, shardTestDigest('a'), shardTestDigest('b'), count); err == nil {
			t.Fatalf("BuildContainerShardSetWithCount(count=%d) succeeded", count)
		}
	}
}

func TestBuildContainerShardSetWithCountKeepsReleaseRaceIsolated(t *testing.T) {
	plan := mustBuildPlan(t, ProfileRelease)
	set, err := BuildContainerShardSetWithCount(plan, shardTestDigest('a'), shardTestDigest('b'), 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, shard := range set.Shards {
		if slices.Contains(shard.GateIDs, GateIDBackendTestGuardWithRace) && len(shard.GateIDs) != 1 {
			t.Fatalf("release race gate was not isolated: %#v", shard.GateIDs)
		}
	}
}

func TestBuildContainerShardSetFromWorkloadPlanBindsFrozenLPTIdentity(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileRelease)
	workloadPlan := testWorkloadExecutionPlan(t, gatePlan, 5)
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
		set.ShardsPerJob != uint8(len(workloadPlan.Shards)) ||
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
		testWorkloadExecutionPlan(t, gatePlan, 3),
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

	static := testContainerShardSet(t, ProfileLocalFast)
	static.Shards[0].WorkloadPlanDigest = shardTestDigest('c')
	if err := static.Validate(); err == nil {
		t.Fatal("Validate() accepted workload fields on schema v2 shard")
	}
}

func TestContainerShardSetValidateStoredAcceptsIntactHistoricalGrouping(t *testing.T) {
	plan := mustBuildPlan(t, ProfileLocalFast)
	all := allProfiles()
	plan.Gates = append(plan.Gates, newGateSpec(GateIDLSPChangedDiagnostics, all, all))
	digest, err := plan.digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.PlanDigest = digest
	set := ContainerShardSet{
		Profile: plan.Profile, PlanDigest: plan.PlanDigest, SourceTreeSHA: plan.Source.SourceTreeSHA,
		AcceptedManifestDigest: testDigest, AcceptedConfigDigest: testDigest, ShardsPerJob: legacyContainerShardCount,
	}
	groups := canonicalContainerShardGroups(ProfileLocalFast, legacyContainerShardCount)
	groups[1] = append(groups[1], GateIDLSPChangedDiagnostics)
	if err := appendContainerShards(&set, groups); err != nil {
		t.Fatal(err)
	}

	if err := set.ValidateStored(plan); err != nil {
		t.Fatalf("ValidateStored() error = %v", err)
	}
	if err := set.Validate(); err == nil {
		t.Fatal("Validate() accepted historical shard grouping as current")
	}
}

func testWorkloadExecutionPlan(t *testing.T, gatePlan GatePlan, maxShards int) WorkloadExecutionPlan {
	t.Helper()
	catalog, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	plan, err := BuildWorkloadExecutionPlan(
		gatePlan,
		catalog,
		DurationLedgerSnapshot{Generation: 11, Ledger: fastDurationLedger(catalog)},
		PlanningContext{
			Platform: "linux/amd64", Runner: "runner-digest",
			Toolchain: "go1.26-node22", MaxShards: maxShards,
			TargetDurationMS: FullCITargetDurationMS,
		},
	)
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlan() error = %v", err)
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

func assertContainerShardGateGroups(t *testing.T, set ContainerShardSet, want [][]GateID) {
	t.Helper()
	seen := make(map[GateID]bool)
	for index, shard := range set.Shards {
		if !slices.Equal(shard.GateIDs, want[index]) || shard.IdentityDigest == "" {
			t.Fatalf("shard %d = %#v, want gates %v", index, shard, want[index])
		}
		claimTestShardGateIDs(t, seen, shard.GateIDs)
	}
}

func claimTestShardGateIDs(t *testing.T, seen map[GateID]bool, gateIDs []GateID) {
	t.Helper()
	for _, id := range gateIDs {
		if seen[id] || id == GateIDReleaseLayeredCheck {
			t.Fatalf("invalid coverage for %q", id)
		}
		seen[id] = true
	}
}

func assertShardSetRejectsSelfConsistentMissingGate(t *testing.T, set ContainerShardSet) {
	t.Helper()
	forged := set
	forged.Shards = append([]ContainerShard(nil), set.Shards...)
	forged.Shards[0].GateIDs = forged.Shards[0].GateIDs[1:]
	identity, err := containerShardIdentityDigest(forged.Shards[0])
	if err != nil {
		t.Fatal(err)
	}
	forged.Shards[0].IdentityDigest = identity
	if err := forged.Validate(); err == nil {
		t.Fatal("self-consistent shard with missing gate passed validation")
	}
}

func TestRunContainerShardsPeaksAtThreeAndCancelsCompanions(t *testing.T) {
	set := testContainerShardSet(t, ProfileLocalFast)
	shardCount := len(set.Shards)
	var mu sync.Mutex
	running, peak := 0, 0
	allStarted := make(chan struct{})
	receipts, err := RunContainerShards(context.Background(), set, func(ctx context.Context, shard ContainerShard) (ContainerShardReceipt, error) {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		if running == shardCount {
			close(allStarted)
		}
		mu.Unlock()
		defer func() { mu.Lock(); running--; mu.Unlock() }()
		<-allStarted
		if shard.Index == 0 {
			return ContainerShardReceipt{Shard: shard}, errors.New("injected shard failure")
		}
		<-ctx.Done()
		return ContainerShardReceipt{Shard: shard}, ctx.Err()
	})
	if err == nil || len(receipts) != shardCount {
		t.Fatalf("RunContainerShards() receipts=%d err=%v", len(receipts), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != shardCount {
		t.Fatalf("concurrency peak = %d, want %d", peak, shardCount)
	}
}

func TestAggregateContainerShardsRejectsTamperAndRequiresTrustedReleaseAggregation(t *testing.T) {
	set := testContainerShardSet(t, ProfileRelease)
	receipts := successfulShardReceipts(t, set)
	results, err := AggregateContainerShards(set, receipts)
	if err != nil {
		t.Fatal(err)
	}
	request, observed, attestation := releaseAggregationEvidence(t, set, results)
	assertReleaseAttestationRejectsTampering(t, request, observed, attestation)
	assertAggregateContainerShardsRejectsTamperedReceipts(t, set, receipts)
}

func releaseAggregationEvidence(t *testing.T, set ContainerShardSet, results []PlanGateExecution) (executorPlanRequest, map[GateID]PlanGateExecution, PlanGateExecution) {
	t.Helper()
	if got := results[len(results)-1].GateID; got != GateIDReleaseLayeredCheck {
		t.Fatalf("final gate = %q", got)
	}
	wantOrder := requiredGateIDs(ProfileRelease)
	for index, id := range wantOrder {
		if results[index].GateID != id {
			t.Fatalf("aggregate gate %d = %q, want canonical %q", index, results[index].GateID, id)
		}
	}
	attestation := results[len(results)-1]
	if !attestation.CompletedAt.After(attestation.StartedAt) {
		t.Fatal("release attestation completion did not follow aggregation start")
	}
	observed := make(map[GateID]PlanGateExecution, len(results)-1)
	for _, result := range results[:len(results)-1] {
		observed[result.GateID] = result
	}
	return executorPlanRequest{profile: set.Profile, planDigest: set.PlanDigest, gateIDs: requiredGateIDs(set.Profile)}, observed, attestation
}

func assertReleaseAttestationRejectsTampering(t *testing.T, request executorPlanRequest, observed map[GateID]PlanGateExecution, attestation PlanGateExecution) {
	t.Helper()
	attestationTampering := []PlanGateExecution{attestation, attestation}
	attestationTampering[0].LogDigest = shardTestDigest('e')
	attestationTampering[1].Log = bytes.Replace(attestation.Log, []byte("prerequisite_digest=sha256:"), []byte("prerequisite_digest=sha256:f"), 1)
	attestationTampering[1].LogDigest = digestPlanLog(attestationTampering[1].Log)
	for _, tamperedAttestation := range attestationTampering {
		if err := validateReleaseLayerAttestation(request, observed, tamperedAttestation); err == nil {
			t.Fatal("tampered release attestation digest evidence was accepted")
		}
	}
}

func assertAggregateContainerShardsRejectsTamperedReceipts(t *testing.T, set ContainerShardSet, receipts []ContainerShardReceipt) {
	t.Helper()
	tests := []struct {
		name   string
		mutate func([]ContainerShardReceipt)
	}{
		{"duplicate", func(values []ContainerShardReceipt) { values[1].Shard = values[0].Shard }},
		{"source", func(values []ContainerShardReceipt) { values[0].Shard.SourceTreeSHA = "source-tree-drift" }},
		{"image", func(values []ContainerShardReceipt) { values[0].Shard.AcceptedConfigDigest = shardTestDigest('c') }},
		{"resource", func(values []ContainerShardReceipt) { values[0].ResourceWitness.PidsLimit++ }},
		{"removal", func(values []ContainerShardReceipt) { values[0].Removed = false }},
		{"passed exit after deadline", func(values []ContainerShardReceipt) {
			values[0].ExitedAt = values[0].Deadline.Add(time.Nanosecond)
			values[0].CompletedAt = values[0].ExitedAt
		}},
		{"gate log", func(values []ContainerShardReceipt) { values[1].GateExecutions[0].Log = []byte("tampered") }},
		{"gate status", func(values []ContainerShardReceipt) { values[1].GateExecutions[0].Status = ResultStatusFailed }},
		{"gate order", func(values []ContainerShardReceipt) {
			values[1].GateExecutions[0], values[1].GateExecutions[1] = values[1].GateExecutions[1], values[1].GateExecutions[0]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := cloneShardReceipts(receipts)
			test.mutate(copy)
			if _, err := AggregateContainerShards(set, copy); err == nil {
				t.Fatal("tampered shard receipts were accepted")
			}
		})
	}
}

func TestValidateShardTimelineDistinguishesExecutionExitFromEvidenceCompletion(t *testing.T) {
	startedAt := time.Date(2026, 7, 20, 19, 10, 23, 849521000, time.UTC)
	deadline := startedAt.Add(10 * time.Minute)
	valid := ContainerShardReceipt{Status: ResultStatusTimeout, StartedAt: startedAt, ExitedAt: deadline, CompletedAt: deadline.Add(431666 * time.Microsecond), Deadline: deadline}
	if err := validateShardTimeline(valid); err != nil {
		t.Fatalf("timeout with post-deadline evidence completion rejected: %v", err)
	}
	for name, receipt := range map[string]ContainerShardReceipt{
		"passed with cleanup after deadline": {Status: ResultStatusPassed, StartedAt: startedAt, ExitedAt: deadline.Add(-time.Nanosecond), CompletedAt: deadline.Add(time.Second), Deadline: deadline},
		"failed before deadline":             {Status: ResultStatusFailed, StartedAt: startedAt, ExitedAt: deadline.Add(-time.Second), CompletedAt: deadline.Add(time.Second), Deadline: deadline},
		"cancelled after deadline":           {Status: ResultStatusCancelled, StartedAt: startedAt, ExitedAt: deadline.Add(time.Second), CompletedAt: deadline.Add(2 * time.Second), Deadline: deadline},
		"infra failed after deadline":        {Status: ResultStatusInfraFailed, StartedAt: startedAt, ExitedAt: deadline.Add(time.Second), CompletedAt: deadline.Add(2 * time.Second), Deadline: deadline},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateShardTimeline(receipt); err != nil {
				t.Fatalf("valid terminal timeline rejected: %v", err)
			}
		})
	}
	tests := []struct {
		name   string
		mutate func(*ContainerShardReceipt)
	}{
		{name: "timeout before deadline", mutate: func(receipt *ContainerShardReceipt) { receipt.ExitedAt = deadline.Add(-time.Nanosecond) }},
		{name: "passed exit after deadline", mutate: func(receipt *ContainerShardReceipt) {
			receipt.Status = ResultStatusPassed
			receipt.ExitedAt = deadline.Add(time.Nanosecond)
		}},
		{name: "failed after deadline", mutate: func(receipt *ContainerShardReceipt) {
			receipt.Status = ResultStatusFailed
			receipt.ExitedAt = deadline.Add(time.Nanosecond)
		}},
		{name: "zero start", mutate: func(receipt *ContainerShardReceipt) { receipt.StartedAt = time.Time{} }},
		{name: "zero exit", mutate: func(receipt *ContainerShardReceipt) { receipt.ExitedAt = time.Time{} }},
		{name: "zero completion", mutate: func(receipt *ContainerShardReceipt) { receipt.CompletedAt = time.Time{} }},
		{name: "deadline before start", mutate: func(receipt *ContainerShardReceipt) { receipt.Deadline = startedAt }},
		{name: "exit before start", mutate: func(receipt *ContainerShardReceipt) { receipt.ExitedAt = startedAt.Add(-time.Nanosecond) }},
		{name: "completion before exit", mutate: func(receipt *ContainerShardReceipt) { receipt.CompletedAt = receipt.ExitedAt.Add(-time.Nanosecond) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := valid
			test.mutate(&receipt)
			if err := validateShardTimeline(receipt); err == nil {
				t.Fatal("invalid shard timeline was accepted")
			}
		})
	}
}

func TestAggregateContainerShardFailureEvidenceIsCanonicalAndFailClosed(t *testing.T) {
	set := testContainerShardSet(t, ProfileLocalFast)
	receipts := successfulShardReceipts(t, set)
	receipts[0].Status = ResultStatusTimeout
	receipts[0].ExitedAt = receipts[0].Deadline
	receipts[0].CompletedAt = receipts[0].Deadline.Add(431666 * time.Microsecond)
	receipts[0].GateExecutions[0].Status = ResultStatusTimeout
	receipts[0].GateExecutions[0].ExitCode = -1
	for index := 1; index < len(receipts); index++ {
		receipts[index].Status = ResultStatusCancelled
		for gateIndex := range receipts[index].GateExecutions {
			receipts[index].GateExecutions[gateIndex].Status = ResultStatusCancelled
			receipts[index].GateExecutions[gateIndex].ExitCode = -1
		}
	}
	ordered, err := AggregateContainerShardFailureEvidence(set, receipts)
	if err != nil {
		t.Fatal(err)
	}
	want := requiredGateIDs(set.Profile)
	if len(ordered) != len(want) {
		t.Fatalf("failure evidence count=%d want=%d", len(ordered), len(want))
	}
	for index, gateID := range want {
		if ordered[index].GateID != gateID || ordered[index].GateID == GateIDReleaseLayeredCheck {
			t.Fatalf("failure evidence[%d]=%#v want gate=%q", index, ordered[index], gateID)
		}
	}

	tests := []struct {
		name   string
		mutate func([]ContainerShardReceipt)
	}{
		{name: "missing gate", mutate: func(values []ContainerShardReceipt) {
			values[1].GateExecutions = values[1].GateExecutions[:len(values[1].GateExecutions)-1]
		}},
		{name: "wrong identity", mutate: func(values []ContainerShardReceipt) {
			values[1].Shard = values[0].Shard
		}},
		{name: "duplicate gate", mutate: func(values []ContainerShardReceipt) {
			values[1].GateExecutions[0].GateID = values[0].GateExecutions[0].GateID
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := cloneShardReceipts(receipts)
			test.mutate(copy)
			if _, err := AggregateContainerShardFailureEvidence(set, copy); err == nil {
				t.Fatal("invalid failure shard evidence was accepted")
			}
		})
	}
}

func cloneShardReceipts(receipts []ContainerShardReceipt) []ContainerShardReceipt {
	cloned := append([]ContainerShardReceipt(nil), receipts...)
	for index := range cloned {
		cloned[index].Shard.GateIDs = slices.Clone(receipts[index].Shard.GateIDs)
		cloned[index].GateExecutions = append([]PlanGateExecution(nil), receipts[index].GateExecutions...)
		for gateIndex := range cloned[index].GateExecutions {
			cloned[index].GateExecutions[gateIndex].Log = slices.Clone(receipts[index].GateExecutions[gateIndex].Log)
		}
	}
	return cloned
}

func testContainerShardSet(t *testing.T, profile Profile) ContainerShardSet {
	t.Helper()
	plan, err := BuildGatePlan(profile, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	set, err := BuildContainerShardSet(plan, shardTestDigest('a'), shardTestDigest('b'))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func successfulShardReceipts(t *testing.T, set ContainerShardSet) []ContainerShardReceipt {
	t.Helper()
	startedAt := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(30 * time.Minute)
	witness := ContainerResourceWitness{SchemaVersion: ContainerResourceWitnessSchemaVersion, NanoCPUs: containerShardCPUNanos, MemoryBytes: containerShardMemoryBytes, PidsLimit: containerShardPIDs}
	receipts := make([]ContainerShardReceipt, len(set.Shards))
	for index, shard := range set.Shards {
		receipt := ContainerShardReceipt{Shard: shard, Status: ResultStatusPassed, ContainerID: "0123456789ab", ResourceWitness: witness,
			ResourceWitnessDigest: containerShardWitnessDigest(witness), Removed: true, RemovalProofDigest: shardTestDigest('d'), StartedAt: startedAt, ExitedAt: startedAt.Add(30 * time.Second), CompletedAt: startedAt.Add(time.Minute), Deadline: deadline}
		receipt.Container = ContainerEvidence{ContainerID: receipt.ContainerID, NetworkID: "network-" + string(rune('0'+index)),
			HostConfigDigest: shardTestDigest('c'), ResourceWitness: witness, ResourceWitnessDigest: receipt.ResourceWitnessDigest,
			NetworkPolicyDigest: shardTestDigest('e'), Removed: true, NetworkRemoved: true}
		for _, id := range shard.GateIDs {
			argvDigest, err := canonicalGateArgvDigest(set.Profile, id)
			if err != nil {
				t.Fatal(err)
			}
			receipt.GateExecutions = append(receipt.GateExecutions, PlanGateExecution{GateID: id, Status: ResultStatusPassed, ExitCode: 0, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second), ArgvDigest: argvDigest, LogDigest: digestPlanLog(nil)})
		}
		receipts[index] = receipt
	}
	return receipts
}

func shardTestDigest(character rune) string { return "sha256:" + strings.Repeat(string(character), 64) }
