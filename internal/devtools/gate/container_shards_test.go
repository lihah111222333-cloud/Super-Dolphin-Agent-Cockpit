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

func TestBuildContainerShardSetUsesProfileSpecificCanonicalGroups(t *testing.T) {
	if groups := canonicalContainerShardGroups(ProfileRelease); len(groups) != MaxContainerShards {
		t.Fatalf("canonical release shard groups = %d, want %d", len(groups), MaxContainerShards)
	}
	if groups := canonicalContainerShardGroups(ProfileLocalFast); len(groups) != MaxContainerShards {
		t.Fatalf("canonical local shard groups = %d, want %d", len(groups), MaxContainerShards)
	}
	for _, profile := range []Profile{ProfilePush, ProfileRemoteRequired, ProfilePromotion} {
		if groups := canonicalContainerShardGroups(profile); len(groups) != MaxContainerShards {
			t.Fatalf("canonical %s shard groups = %d, want %d", profile, len(groups), MaxContainerShards)
		}
	}
	set := testContainerShardSet(t, ProfileRelease)
	if len(set.Shards) != MaxContainerShards {
		t.Fatalf("shards = %d, want %d", len(set.Shards), MaxContainerShards)
	}
	want := [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendFullTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify,
			GateIDSQLCVerify, GateIDCodemapCheck, GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
		{GateIDBackendTestWithGuard, GateIDLSPChangedDiagnostics, GateIDBackendNilness},
		{GateIDBackendTestGuardWithRace},
	}
	normal := testContainerShardSet(t, ProfileLocalFast)
	normalWant := [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify},
		{GateIDBackendTestWithGuard, GateIDLSPChangedDiagnostics},
		{GateIDSQLCVerify, GateIDCodemapCheck, GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
	push := testContainerShardSet(t, ProfilePush)
	remote := testContainerShardSet(t, ProfileRemoteRequired)
	promotion := testContainerShardSet(t, ProfilePromotion)
	pushWant := [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify},
		{GateIDBackendTestWithGuard, GateIDLSPChangedDiagnostics},
		{GateIDSQLCVerify, GateIDCodemapCheck, GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
	assertContainerShardGateGroups(t, set, want)
	assertContainerShardGateGroups(t, normal, normalWant)
	assertContainerShardGateGroups(t, push, pushWant)
	assertContainerShardGateGroups(t, remote, pushWant)
	assertContainerShardGateGroups(t, promotion, pushWant)
	assertShardSetRejectsSelfConsistentMissingGate(t, set)
}

func TestCanonicalGateArgvDigestMatchesReceiptEncoding(t *testing.T) {
	got, err := canonicalGateArgvDigest(ProfileLocalFast, GateIDBackendTestWithGuard)
	if err != nil {
		t.Fatal(err)
	}
	const want = "sha256:20a5b5dbfb5d3b8cffd7fcb1d7a7cd463eaa94340261313bbd1d73ceeb02cc18"
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
	var mu sync.Mutex
	running, peak := 0, 0
	allStarted := make(chan struct{})
	receipts, err := RunContainerShards(context.Background(), set, func(ctx context.Context, shard ContainerShard) (ContainerShardReceipt, error) {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		if running == MaxContainerShards {
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
	if err == nil || len(receipts) != MaxContainerShards {
		t.Fatalf("RunContainerShards() receipts=%d err=%v", len(receipts), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != MaxContainerShards {
		t.Fatalf("concurrency peak = %d, want %d", peak, MaxContainerShards)
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
			log := []byte("passed " + string(id))
			argvDigest, err := canonicalGateArgvDigest(set.Profile, id)
			if err != nil {
				t.Fatal(err)
			}
			receipt.GateExecutions = append(receipt.GateExecutions, PlanGateExecution{GateID: id, Status: ResultStatusPassed, ExitCode: 0, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second), ArgvDigest: argvDigest, Log: log, LogDigest: digestPlanLog(log)})
		}
		receipts[index] = receipt
	}
	return receipts
}

func shardTestDigest(character rune) string { return "sha256:" + strings.Repeat(string(character), 64) }
