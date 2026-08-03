package gate

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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
			Toolchain:        "go1.26-node22",
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
		{"duplicate", func(values []ContainerShardReceipt) {
			values[0].GateExecutions = append(values[0].GateExecutions, values[0].GateExecutions[0])
		}},
		{"source", func(values []ContainerShardReceipt) { values[0].Shard.SourceTreeSHA = "source-tree-drift" }},
		{"image", func(values []ContainerShardReceipt) { values[0].Shard.AcceptedConfigDigest = shardTestDigest('c') }},
		{"resource", func(values []ContainerShardReceipt) { values[0].ResourceWitness.PidsLimit++ }},
		{"removal", func(values []ContainerShardReceipt) { values[0].Removed = false }},
		{"passed exit after deadline", func(values []ContainerShardReceipt) {
			values[0].ExitedAt = values[0].Deadline.Add(time.Nanosecond)
			values[0].CompletedAt = values[0].ExitedAt
		}},
		{"gate log", func(values []ContainerShardReceipt) { values[0].GateExecutions[0].Log = []byte("tampered") }},
		{"gate status", func(values []ContainerShardReceipt) { values[0].GateExecutions[0].Status = ResultStatusFailed }},
		{"gate order", func(values []ContainerShardReceipt) {
			values[0].GateExecutions[0], values[0].GateExecutions[1] = values[0].GateExecutions[1], values[0].GateExecutions[0]
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
	receipts := failureEvidenceReceipts(t, set)
	ordered, err := AggregateContainerShardFailureEvidence(set, receipts)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalFailureEvidence(t, set, ordered)
	assertFailureEvidenceRejectsMutations(t, set, receipts)
}

func failureEvidenceReceipts(t *testing.T, set ContainerShardSet) []ContainerShardReceipt {
	t.Helper()
	receipts := successfulShardReceipts(t, set)
	const timeoutGateID = GateIDBackendTestWithGuard
	timeoutReceipt := shardReceiptForGate(t, receipts, timeoutGateID)
	timeoutReceipt.Status = ResultStatusTimeout
	timeoutReceipt.ExitedAt = timeoutReceipt.Deadline
	timeoutReceipt.CompletedAt = timeoutReceipt.Deadline.Add(431666 * time.Microsecond)
	timeoutExecution := shardGateExecutionForGate(t, timeoutReceipt, timeoutGateID)
	timeoutExecution.Status = ResultStatusTimeout
	timeoutExecution.ExitCode = -1
	for index := range receipts {
		if receipts[index].Shard.IdentityDigest == timeoutReceipt.Shard.IdentityDigest {
			continue
		}
		receipts[index].Status = ResultStatusCancelled
		for gateIndex := range receipts[index].GateExecutions {
			receipts[index].GateExecutions[gateIndex].Status = ResultStatusCancelled
			receipts[index].GateExecutions[gateIndex].ExitCode = -1
		}
	}
	return receipts
}

func assertCanonicalFailureEvidence(t *testing.T, set ContainerShardSet, ordered []PlanGateExecution) {
	t.Helper()
	want := requiredGateIDs(set.Profile)
	if len(ordered) != len(want) {
		t.Fatalf("failure evidence count=%d want=%d", len(ordered), len(want))
	}
	for index, gateID := range want {
		if ordered[index].GateID != gateID || ordered[index].GateID == GateIDReleaseLayeredCheck {
			t.Fatalf("failure evidence[%d]=%#v want gate=%q", index, ordered[index], gateID)
		}
	}
}

func assertFailureEvidenceRejectsMutations(t *testing.T, set ContainerShardSet, receipts []ContainerShardReceipt) {
	t.Helper()
	const timeoutGateID = GateIDBackendTestWithGuard
	tests := []struct {
		name   string
		mutate func([]ContainerShardReceipt)
	}{
		{name: "missing gate", mutate: func(values []ContainerShardReceipt) {
			removeShardGateExecution(t, values, timeoutGateID)
		}},
		{name: "wrong identity", mutate: func(values []ContainerShardReceipt) {
			shardReceiptForGate(t, values, timeoutGateID).Shard.IdentityDigest = shardTestDigest('f')
		}},
		{name: "duplicate gate", mutate: func(values []ContainerShardReceipt) {
			shardGateExecutionForGate(t, shardReceiptForGate(t, values, GateIDSQLCVerify), GateIDSQLCVerify).GateID = timeoutGateID
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

func shardReceiptForGate(t *testing.T, receipts []ContainerShardReceipt, gateID GateID) *ContainerShardReceipt {
	t.Helper()
	for index := range receipts {
		if slices.Contains(receipts[index].Shard.GateIDs, gateID) {
			return &receipts[index]
		}
	}
	t.Fatalf("gate %q does not belong to any shard receipt", gateID)
	return nil
}

func shardGateExecutionForGate(t *testing.T, receipt *ContainerShardReceipt, gateID GateID) *PlanGateExecution {
	t.Helper()
	for index := range receipt.GateExecutions {
		if receipt.GateExecutions[index].GateID == gateID {
			return &receipt.GateExecutions[index]
		}
	}
	t.Fatalf("gate %q does not belong to shard receipt %q", gateID, receipt.Shard.IdentityDigest)
	return nil
}

func removeShardGateExecution(t *testing.T, receipts []ContainerShardReceipt, gateID GateID) {
	t.Helper()
	receipt := shardReceiptForGate(t, receipts, gateID)
	filtered := make([]PlanGateExecution, 0, len(receipt.GateExecutions)-1)
	removed := false
	for _, execution := range receipt.GateExecutions {
		if execution.GateID == gateID {
			if removed {
				t.Fatalf("gate %q appears more than once in shard receipt", gateID)
			}
			removed = true
			continue
		}
		filtered = append(filtered, execution)
	}
	if !removed {
		t.Fatalf("gate %q does not belong to shard receipt %q", gateID, receipt.Shard.IdentityDigest)
	}
	receipt.GateExecutions = filtered
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
	set, err := BuildContainerShardSetFromWorkloadPlan(plan, testWorkloadExecutionPlan(t, plan), shardTestDigest('a'), shardTestDigest('b'))
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
