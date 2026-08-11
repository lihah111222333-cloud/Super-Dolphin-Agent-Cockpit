package gate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestLocalSchedulerExactHitSkipsAllExecution(t *testing.T) {
	store, batch, localIdentity := localPassAuthorityFixture(t)
	item := localSchedulerTestItem(t, localIdentity, localPassTestEnvironment(false), false)
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(item, LocalWorkloadTargetLocal, &counts)
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunLocalWorkloadMisses(testContext(), store, input, prepared); err != nil {
		t.Fatal(err)
	}
	assertLocalSchedulerStats(t, prepared.Stats, LocalWorkloadScheduleStats{SelectedLocal: 1, LocalHits: 1})
	if counts.materialize != 0 || counts.execute != 0 || counts.remote != 0 {
		t.Fatalf("local exact hit side effects = %#v, want zero", counts)
	}
	if prepared.Hits[0].OriginJobID != localWorkloadPassOriginJobPrefix+batch.Origin.RunID {
		t.Fatalf("local hit origin = %q, want local origin", prepared.Hits[0].OriginJobID)
	}
}

func TestLocalSchedulerHybridLookupThenAdmissionHasNoFallback(t *testing.T) {
	store, _, localHit := localPassAuthorityFixture(t)
	localMiss := localSchedulerIdentity(t, GateIDBackendTestGuardWithRace, localPassTestEnvironment(true), "hybrid-miss")
	items := []LocalWorkloadScheduleItem{
		localSchedulerTestItem(t, localHit, localPassTestEnvironment(false), false),
		localSchedulerTestItemWithRemote(t, localMiss, localPassTestEnvironment(true), localPassTestEnvironment(false), 1000),
	}
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(items[0], LocalWorkloadTargetHybrid, &counts)
	input.Items = items
	input.Receipt = localSchedulerReceiptForItems(t, items)
	input.Host.MaxDurationMS = 10
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	if counts.remote != 0 {
		t.Fatalf("Prepare invoked remote executor %d times", counts.remote)
	}
	assertLocalSchedulerStats(t, prepared.Stats, LocalWorkloadScheduleStats{SelectedLocal: 1, SelectedRemote: 1, LocalHits: 1})
	if len(prepared.Misses) != 0 || len(prepared.Remote) != 1 || prepared.Remote[0] != items[1].LocalIdentity.WorkloadID {
		t.Fatalf("hybrid split = misses=%#v remote=%#v", prepared.Misses, prepared.Remote)
	}
	if err := RunSelectedRemoteWorkloads(testContext(), input, &prepared); err != nil {
		t.Fatal(err)
	}
	if counts.remote != 1 || prepared.Stats.RemoteInvocations != 1 {
		t.Fatalf("explicit remote phase counters = %#v", counts)
	}
}

func TestLocalSchedulerAutoReusesMappedIneligibleLocalHitBeforeAdmission(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	environment := localSchedulerEnvironmentForWorkload(t, GateIDSQLCVerify)
	identity := localSchedulerIdentity(t, GateIDSQLCVerify, environment, "mapped-ineligible-hit")
	recordLocalSchedulerPass(t, store, identity, environment)
	item := localSchedulerTestItem(t, identity, environment, false)
	item.LocalEligible = false
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(item, LocalWorkloadTargetAuto, &counts)
	input.Receipt = newLocalSchedulerTestReceipt(map[GateID]LocalWorkloadPassEnvironment{GateIDSQLCVerify: environment})
	input.Host.Allowed = false
	sampled := 0
	input.SampleHost = func(context.Context) (LocalHostAdmission, error) {
		sampled++
		return LocalHostAdmission{}, errors.New("historical local hit must not sample the host")
	}
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalSchedulerStats(t, prepared.Stats, LocalWorkloadScheduleStats{SelectedLocal: 1, LocalHits: 1})
	if sampled != 0 || len(prepared.Hits) != 1 || len(prepared.Remote) != 0 || len(prepared.Misses) != 0 || counts.remote != 0 {
		t.Fatalf("mapped ineligible local hit = sampled=%d hits=%d misses=%d remote=%d counts=%#v", sampled, len(prepared.Hits), len(prepared.Misses), len(prepared.Remote), counts)
	}
}

func TestLocalSchedulerKnownUnmappedRoutesRemoteWithoutLocalLookup(t *testing.T) {
	item := LocalWorkloadScheduleItem{WorkloadID: GateIDLSPChangedDiagnostics, Resource: LocalWorkloadResource{DurationMS: 1, CPU: 1, MemoryGiB: 1}}
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(item, LocalWorkloadTargetAuto, &counts)
	sampled := 0
	input.SampleHost = func(context.Context) (LocalHostAdmission, error) {
		sampled++
		return LocalHostAdmission{}, errors.New("known unmapped workload must not sample the host")
	}
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), newWorkloadPassEvidenceStore(t, 1), input)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalSchedulerStats(t, prepared.Stats, LocalWorkloadScheduleStats{SelectedRemote: 1})
	if sampled != 0 || len(prepared.Hits) != 0 || len(prepared.Misses) != 0 || !slices.Equal(prepared.Remote, []GateID{GateIDLSPChangedDiagnostics}) {
		t.Fatalf("known unmapped route = sampled=%d hits=%d misses=%d remote=%#v", sampled, len(prepared.Hits), len(prepared.Misses), prepared.Remote)
	}
	if err := RunSelectedRemoteWorkloads(testContext(), input, &prepared); err != nil {
		t.Fatal(err)
	}
	if counts.remote != 1 || prepared.Stats.RemoteInvocations != 1 {
		t.Fatalf("known unmapped remote execution = counts=%#v stats=%#v", counts, prepared.Stats)
	}
}

func TestLocalSchedulerAutoRoutesMappedIneligibleDirectRemoteWithoutReceipt(t *testing.T) {
	item := LocalWorkloadScheduleItem{
		WorkloadID: GateIDFrontendLint,
		Resource:   LocalWorkloadResource{DurationMS: 1, CPU: 1, MemoryGiB: 1},
	}
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(item, LocalWorkloadTargetAuto, &counts)
	input.Receipt = nil
	input.SampleHost = func(context.Context) (LocalHostAdmission, error) {
		return LocalHostAdmission{}, errors.New("direct remote workload must not sample the host")
	}
	store := newWorkloadPassEvidenceStore(t, 1)
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalSchedulerStats(t, prepared.Stats, LocalWorkloadScheduleStats{SelectedRemote: 1})
	assertLocalSchedulerMappedDirectRemote(t, prepared)
	prepared, err = RunLocalWorkloadMisses(testContext(), store, input, prepared)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalSchedulerDirectRemoteNoLocalPhase(t, counts)
	if err := RunSelectedRemoteWorkloads(testContext(), input, &prepared); err != nil {
		t.Fatal(err)
	}
	assertLocalSchedulerDirectRemoteExecution(t, prepared, counts)
}

func TestLocalSchedulerExplicitLocalRejectsIneligibleWithoutExecutionSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		item LocalWorkloadScheduleItem
	}{
		{name: "mapped", item: func() LocalWorkloadScheduleItem {
			identity := localSchedulerIdentity(t, GateIDSQLCVerify, localPassTestEnvironment(false), "mapped-ineligible-miss")
			item := localSchedulerTestItem(t, identity, localPassTestEnvironment(false), false)
			item.LocalEligible = false
			return item
		}()},
		{name: "known-unmapped", item: LocalWorkloadScheduleItem{WorkloadID: GateIDLSPChangedDiagnostics, Resource: LocalWorkloadResource{DurationMS: 1, CPU: 1, MemoryGiB: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counts := localSchedulerCounters{}
			input := localSchedulerTestInput(tc.item, LocalWorkloadTargetLocal, &counts)
			input.SampleHost = func(context.Context) (LocalHostAdmission, error) {
				return LocalHostAdmission{}, errors.New("explicitly rejected workload must not sample the host")
			}
			_, err := PrepareLocalWorkloadSchedule(testContext(), newWorkloadPassEvidenceStore(t, 1), input)
			if err == nil || !strings.Contains(err.Error(), "remote fallback is forbidden") {
				t.Fatalf("explicit local error = %v, want forbidden remote fallback", err)
			}
			if counts.materialize != 0 || counts.execute != 0 || counts.remote != 0 {
				t.Fatalf("explicit local ineligible side effects = %#v", counts)
			}
		})
	}
}

func TestLocalSchedulerRejectsUnknownAndBogusIdentityBypasses(t *testing.T) {
	validIdentity := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "identity-bypass")
	for _, tc := range []struct {
		name string
		item LocalWorkloadScheduleItem
	}{
		{name: "zero-workload", item: LocalWorkloadScheduleItem{Resource: LocalWorkloadResource{DurationMS: 1, CPU: 1, MemoryGiB: 1}}},
		{name: "unknown-workload", item: LocalWorkloadScheduleItem{WorkloadID: GateID("unknown:workload"), Resource: LocalWorkloadResource{DurationMS: 1, CPU: 1, MemoryGiB: 1}}},
		{name: "eligible-without-identity", item: LocalWorkloadScheduleItem{WorkloadID: GateIDCodemapCheck, Resource: LocalWorkloadResource{DurationMS: 1, CPU: 1, MemoryGiB: 1}}},
		{name: "partial-identity", item: LocalWorkloadScheduleItem{WorkloadID: validIdentity.WorkloadID, LocalIdentity: validIdentity, Resource: LocalWorkloadResource{DurationMS: 1, CPU: 1, MemoryGiB: 1}, LocalEligible: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counts := localSchedulerCounters{}
			input := localSchedulerTestInput(tc.item, LocalWorkloadTargetAuto, &counts)
			if _, err := PrepareLocalWorkloadSchedule(testContext(), newWorkloadPassEvidenceStore(t, 1), input); err == nil {
				t.Fatal("bogus scheduler item unexpectedly bypassed validation")
			}
			if counts.materialize != 0 || counts.execute != 0 || counts.remote != 0 {
				t.Fatalf("invalid scheduler item side effects = %#v", counts)
			}
		})
	}
}

func TestLocalSchedulerAutoUsesAggregateBudgetButExplicitLocalOverridesIt(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "aggregate-first")
	second := localSchedulerIdentity(t, GateIDBackendTestGuardWithRace, localPassTestEnvironment(true), "aggregate-second")
	items := []LocalWorkloadScheduleItem{
		localSchedulerTestItemWithRemote(t, first, localPassTestEnvironment(false), localPassTestEnvironment(false), 60),
		localSchedulerTestItemWithRemote(t, second, localPassTestEnvironment(true), localPassTestEnvironment(true), 60),
	}
	counts := localSchedulerCounters{}
	auto := localSchedulerTestInput(items[0], LocalWorkloadTargetHybrid, &counts)
	auto.Items = items
	auto.Receipt = localSchedulerReceiptForItems(t, items)
	auto.Host.MaxDurationMS = 100
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, auto)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Stats.SelectedLocal != 1 || prepared.Stats.SelectedRemote != 1 || len(prepared.Misses) != 1 || len(prepared.Remote) != 1 {
		t.Fatalf("auto aggregate split = stats=%#v misses=%#v remote=%#v", prepared.Stats, prepared.Misses, prepared.Remote)
	}
	explicit := auto
	explicit.Target = LocalWorkloadTargetLocal
	prepared, err = PrepareLocalWorkloadSchedule(testContext(), store, explicit)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Stats.SelectedLocal != 2 || prepared.Stats.SelectedRemote != 0 || len(prepared.Misses) != 2 {
		t.Fatalf("explicit local aggregate override = stats=%#v misses=%#v remote=%#v", prepared.Stats, prepared.Misses, prepared.Remote)
	}
}

func TestLocalSchedulerAggregateSplitIsStableAcrossManifestPermutation(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "permutation-first")
	second := localSchedulerIdentity(t, GateIDBackendTestGuardWithRace, localPassTestEnvironment(true), "permutation-second")
	items := []LocalWorkloadScheduleItem{
		localSchedulerTestItemWithRemote(t, first, localPassTestEnvironment(false), localPassTestEnvironment(false), 60),
		localSchedulerTestItemWithRemote(t, second, localPassTestEnvironment(true), localPassTestEnvironment(true), 60),
	}
	input := localSchedulerTestInput(items[0], LocalWorkloadTargetHybrid, &localSchedulerCounters{})
	input.Items = append([]LocalWorkloadScheduleItem(nil), items...)
	input.Receipt = localSchedulerReceiptForItems(t, items)
	input.Host.MaxDurationMS = 100
	original, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Items = []LocalWorkloadScheduleItem{items[1], items[0]}
	input.Receipt = localSchedulerReceiptForItems(t, input.Items)
	permuted, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(workloadIdentityIDs(original.Misses), workloadIdentityIDs(permuted.Misses)) || !slices.Equal(original.Remote, permuted.Remote) {
		t.Fatalf("aggregate split changed after permutation: original misses=%#v remote=%#v, permuted misses=%#v remote=%#v", workloadIdentityIDs(original.Misses), original.Remote, workloadIdentityIDs(permuted.Misses), permuted.Remote)
	}
}

func TestLocalSchedulerAutoOversizedMissesUseRemoteAsWholeSet(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	long := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "auto-oversized-long")
	short := localSchedulerIdentity(t, GateIDBackendTestGuardWithRace, localPassTestEnvironment(true), "auto-oversized-short")
	items := []LocalWorkloadScheduleItem{
		localSchedulerTestItemWithRemote(t, long, localPassTestEnvironment(false), localPassTestEnvironment(false), 10*60*1000+1),
		localSchedulerTestItemWithRemote(t, short, localPassTestEnvironment(true), localPassTestEnvironment(true), 1),
	}
	input := localSchedulerTestInput(items[0], LocalWorkloadTargetAuto, &localSchedulerCounters{})
	input.Items = items
	input.Receipt = localSchedulerReceiptForItems(t, items)
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Misses) != 0 || len(prepared.Remote) != len(items) {
		t.Fatalf("auto oversized split = misses=%#v remote=%#v, want all remote", prepared.Misses, prepared.Remote)
	}
}

func TestLocalSchedulerAutoTooManyMissesUseRemoteAsWholeSet(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	items := make([]LocalWorkloadScheduleItem, 0, 65)
	for index := range 65 {
		id, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoPackage, fmt.Sprintf("./internal/devtools/gate/auto-count-%02d", index))
		if err != nil {
			t.Fatal(err)
		}
		identity := localSchedulerIdentity(t, GateID(id), localPassTestEnvironment(false), fmt.Sprintf("auto-count-%02d", index))
		items = append(items, localSchedulerTestItemWithRemote(t, identity, localPassTestEnvironment(false), localPassTestEnvironment(false), 1))
	}
	input := localSchedulerTestInput(items[0], LocalWorkloadTargetAuto, &localSchedulerCounters{})
	input.Items = items
	input.Receipt = localSchedulerReceiptForItems(t, items)
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Misses) != 0 || len(prepared.Remote) != len(items) {
		t.Fatalf("auto count split = misses=%#v remote=%d, want all remote", prepared.Misses, len(prepared.Remote))
	}
}

func TestLocalSchedulerPreparedProjectionRejectsHitMissOverlap(t *testing.T) {
	store, _, identity := localPassAuthorityFixture(t)
	input := localSchedulerTestInput(localSchedulerTestItem(t, identity, localPassTestEnvironment(false), false), LocalWorkloadTargetLocal, &localSchedulerCounters{})
	prepared := LocalWorkloadScheduleResult{
		Hits:   []WorkloadPassEvidence{{Identity: identity}},
		Misses: []WorkloadPassIdentity{identity},
	}
	if _, err := RunLocalWorkloadMisses(testContext(), store, input, prepared); err == nil || !strings.Contains(err.Error(), "appears in both") {
		t.Fatalf("hit/miss overlap error = %v", err)
	}
}

func TestLocalSchedulerPreparedProjectionRejectsIdentityDriftAndUnknownRemote(t *testing.T) {
	store, _, identity := localPassAuthorityFixture(t)
	input := localSchedulerTestInput(localSchedulerTestItem(t, identity, localPassTestEnvironment(false), false), LocalWorkloadTargetAuto, &localSchedulerCounters{})
	for name, prepared := range map[string]LocalWorkloadScheduleResult{
		"identity drift": {Misses: []WorkloadPassIdentity{{IdentityDigest: identity.IdentityDigest, WorkloadID: identity.WorkloadID, ExecutionDigest: identity.ExecutionDigest, InputDigest: "sha256:" + strings.Repeat("b", 64), EnvironmentDigest: identity.EnvironmentDigest}}},
		"unknown remote": {Remote: []GateID{"backend:unknown"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RunLocalWorkloadMisses(testContext(), store, input, prepared); err == nil {
				t.Fatal("forged prepared schedule unexpectedly accepted")
			}
		})
	}
	input.Receipt = nil
	if _, err := RunLocalWorkloadMisses(testContext(), store, input, LocalWorkloadScheduleResult{Misses: []WorkloadPassIdentity{identity}}); err == nil || !strings.Contains(err.Error(), "producer-sealed executor receipt") {
		t.Fatalf("nil receipt forged local miss error = %v, want sealed receipt rejection", err)
	}
}

type localSchedulerEnvironmentPreflightCase struct {
	name  string
	apply func(map[GateID]LocalWorkloadPassEnvironment, GateID, LocalWorkloadPassEnvironment)
}

func TestLocalSchedulerPrevalidatesEveryLocalMissBeforeSideEffects(t *testing.T) {
	cases := []localSchedulerEnvironmentPreflightCase{
		{name: "missing environment", apply: func(environments map[GateID]LocalWorkloadPassEnvironment, workloadID GateID, _ LocalWorkloadPassEnvironment) {
			delete(environments, workloadID)
		}},
		{name: "invalid environment", apply: func(environments map[GateID]LocalWorkloadPassEnvironment, workloadID GateID, environment LocalWorkloadPassEnvironment) {
			environment.Platform = "linux/amd64"
			environments[workloadID] = environment
		}},
		{name: "identity environment mismatch", apply: func(environments map[GateID]LocalWorkloadPassEnvironment, workloadID GateID, _ LocalWorkloadPassEnvironment) {
			environments[workloadID] = localPassTestEnvironment(true)
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) { runLocalSchedulerEnvironmentPreflightCase(t, testCase) })
	}
}

func runLocalSchedulerEnvironmentPreflightCase(t *testing.T, testCase localSchedulerEnvironmentPreflightCase) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	environment := localPassTestEnvironment(false)
	identity := localSchedulerIdentity(t, GateIDBackendTestWithGuard, environment, "environment-preflight-"+testCase.name)
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(localSchedulerTestItem(t, identity, environment, false), LocalWorkloadTargetLocal, &counts)
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	testCase.apply(localSchedulerTestReceiptValues(t, input), identity.WorkloadID, environment)
	result, runErr := RunLocalWorkloadMisses(testContext(), store, input, prepared)
	if runErr == nil {
		t.Fatal("invalid local MISS environment unexpectedly executed")
	}
	assertLocalSchedulerNoSideEffects(t, counts, result)
	assertLocalSchedulerNoLedgerEvidence(t, store, []WorkloadPassIdentity{identity})
}

func TestLocalSchedulerPrevalidatesTheWholeMissBatchBeforeSideEffects(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	firstEnvironment := localPassTestEnvironment(false)
	secondEnvironment := localPassTestEnvironment(false)
	first := localSchedulerIdentity(t, GateIDBackendTestWithGuard, firstEnvironment, "batch-preflight-first")
	second := localSchedulerIdentity(t, GateIDBackendTestGuardWithRace, secondEnvironment, "batch-preflight-second")
	items := []LocalWorkloadScheduleItem{localSchedulerTestItem(t, first, firstEnvironment, false), localSchedulerTestItem(t, second, secondEnvironment, false)}
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(items[0], LocalWorkloadTargetLocal, &counts)
	input.Items = items
	input.Receipt = newLocalSchedulerTestReceipt(map[GateID]LocalWorkloadPassEnvironment{
		first.WorkloadID:  firstEnvironment,
		second.WorkloadID: secondEnvironment,
	})
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	localSchedulerTestReceiptValues(t, input)[second.WorkloadID] = LocalWorkloadPassEnvironment{Platform: "linux/amd64", GOOS: "linux", GOARCH: "amd64", CGOEnabled: "0", GoFlags: secondEnvironment.GoFlags, ToolchainClosureDigest: secondEnvironment.ToolchainClosureDigest, RunnerSemanticPolicy: secondEnvironment.RunnerSemanticPolicy, BaseRunnerSemanticDigest: secondEnvironment.BaseRunnerSemanticDigest, RunnerSemanticDigest: secondEnvironment.RunnerSemanticDigest}
	result, runErr := RunLocalWorkloadMisses(testContext(), store, input, prepared)
	if runErr == nil {
		t.Fatal("invalid later local MISS environment unexpectedly executed")
	}
	assertLocalSchedulerNoSideEffects(t, counts, result)
	assertLocalSchedulerNoLedgerEvidence(t, store, []WorkloadPassIdentity{first, second})
}

func TestLocalSchedulerReceiptReverifyDriftDoesNotExecuteOrPromote(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	environment := localPassTestEnvironment(false)
	identity := localSchedulerIdentity(t, GateIDBackendTestWithGuard, environment, "receipt-drift")
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(localSchedulerTestItem(t, identity, environment, false), LocalWorkloadTargetLocal, &counts)
	receipt := input.Receipt.(*localSchedulerTestReceipt)
	receipt.reverifyErr = errors.New("verified receipt drifted")
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := RunLocalWorkloadMisses(testContext(), store, input, prepared)
	if runErr == nil || !strings.Contains(runErr.Error(), "reverify executor receipt") {
		t.Fatalf("receipt drift run error = %v", runErr)
	}
	if counts.execute != 0 || result.Stats.LocalExecuted != 0 || len(result.Evidence) != 0 {
		t.Fatalf("receipt drift side effects = counts=%#v stats=%#v evidence=%d", counts, result.Stats, len(result.Evidence))
	}
	hits, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("receipt drift promoted evidence = %d", len(hits))
	}
}
