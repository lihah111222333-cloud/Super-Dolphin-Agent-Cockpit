package gate

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func assertLocalSchedulerMappedDirectRemote(t *testing.T, prepared LocalWorkloadScheduleResult) {
	t.Helper()
	if len(prepared.Hits) != 0 || len(prepared.Misses) != 0 || !slices.Equal(prepared.Remote, []GateID{GateIDFrontendLint}) {
		t.Fatalf("mapped direct-remote schedule = %#v", prepared)
	}
}

func assertLocalSchedulerDirectRemoteNoLocalPhase(t *testing.T, counts localSchedulerCounters) {
	t.Helper()
	if counts.materialize != 0 || counts.execute != 0 || counts.remote != 0 {
		t.Fatalf("all direct-remote local phase side effects = %#v, want zero", counts)
	}
}

func assertLocalSchedulerDirectRemoteExecution(t *testing.T, prepared LocalWorkloadScheduleResult, counts localSchedulerCounters) {
	t.Helper()
	if counts.remote != 1 || prepared.Stats.RemoteInvocations != 1 {
		t.Fatalf("mapped direct-remote execution = counts=%#v stats=%#v", counts, prepared.Stats)
	}
}

func assertLocalSchedulerNoSideEffects(t *testing.T, counts localSchedulerCounters, result LocalWorkloadScheduleResult) {
	t.Helper()
	if !slices.Equal([]int{counts.materialize, counts.restore, counts.verify, counts.execute, counts.remote}, []int{0, 0, 0, 0, 0}) {
		t.Fatalf("local scheduler side effects = %#v, want zero", counts)
	}
	if result.Stats.LocalExecuted != 0 || len(result.Evidence) != 0 {
		t.Fatalf("local scheduler result = %#v, want no execution/evidence", result)
	}
}

func assertLocalSchedulerNoLedgerEvidence(t *testing.T, store *DurationLedgerStore, identities []WorkloadPassIdentity) {
	t.Helper()
	evidence, err := store.LookupLocalWorkloadPassEvidence(identities)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 0 {
		t.Fatalf("local scheduler wrote ledger evidence = %d", len(evidence))
	}
}

func workloadIdentityIDs(identities []WorkloadPassIdentity) []GateID {
	ids := make([]GateID, 0, len(identities))
	for _, identity := range identities {
		ids = append(ids, identity.WorkloadID)
	}
	return ids
}

func TestLocalSchedulerMissExecutesOnlyExplicitIDsAndPromotesGreenEntries(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "miss-first")
	second := localSchedulerIdentity(t, GateIDBackendTestGuardWithRace, localPassTestEnvironment(true), "miss-second")
	items := []LocalWorkloadScheduleItem{
		localSchedulerTestItemWithRemote(t, first, localPassTestEnvironment(false), localPassTestEnvironment(false), 1),
		localSchedulerTestItemWithRemote(t, second, localPassTestEnvironment(true), localPassTestEnvironment(true), 1),
	}
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(items[0], LocalWorkloadTargetLocal, &counts)
	input.Items = items
	input.Receipt = newLocalSchedulerTestReceipt(map[GateID]LocalWorkloadPassEnvironment{first.WorkloadID: localPassTestEnvironment(false), second.WorkloadID: localPassTestEnvironment(true)})
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Misses) != 2 {
		t.Fatalf("local misses = %d, want 2", len(prepared.Misses))
	}
	counts.executeIDs = nil
	counts.failID = second.WorkloadID
	result, runErr := RunLocalWorkloadMisses(testContext(), store, input, prepared)
	assertLocalSchedulerMissOutcome(t, store, result, runErr, first, second, counts)
}

func TestLocalSchedulerReceiptDriftRejectsBeforeExecuteOrLocalPass(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	identity := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "sealed-cache-drift")
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(localSchedulerTestItem(t, identity, localPassTestEnvironment(false), false), LocalWorkloadTargetLocal, &counts)
	receipt := &localSchedulerTestReceipt{environments: map[GateID]LocalWorkloadPassEnvironment{identity.WorkloadID: localPassTestEnvironment(false)}, reverifyErr: errors.New("sealed Go module cache drifted"), sealed: true}
	input.Receipt = receipt
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunLocalWorkloadMisses(testContext(), store, input, prepared)
	if err == nil || !strings.Contains(err.Error(), "sealed Go module cache drifted") {
		t.Fatalf("run error = %v, want receipt drift rejection", err)
	}
	if counts.execute != 0 || result.Stats.LocalExecuted != 0 || len(result.Evidence) != 0 {
		t.Fatalf("receipt drift executed or promoted local PASS: counts=%#v result=%#v", counts, result)
	}
	assertLocalSchedulerNoLedgerEvidence(t, store, []WorkloadPassIdentity{identity})
}

func TestLocalSchedulerRestoresExactTreeBetweenWorkloads(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "restore-first")
	second := localSchedulerIdentity(t, GateIDBackendTestGuardWithRace, localPassTestEnvironment(true), "restore-second")
	items := []LocalWorkloadScheduleItem{
		localSchedulerTestItemWithRemote(t, first, localPassTestEnvironment(false), localPassTestEnvironment(false), 1),
		localSchedulerTestItemWithRemote(t, second, localPassTestEnvironment(true), localPassTestEnvironment(true), 1),
	}
	input := localSchedulerTestInput(items[0], LocalWorkloadTargetLocal, &localSchedulerCounters{})
	input.Items = items
	input.Receipt = newLocalSchedulerTestReceipt(map[GateID]LocalWorkloadPassEnvironment{
		first.WorkloadID:  localPassTestEnvironment(false),
		second.WorkloadID: localPassTestEnvironment(true),
	})
	root := t.TempDir()
	marker := filepath.Join(root, "generated-by-previous-workload")
	proof := localSchedulerTreeProof{marker: marker}
	input.Materialize = func(_ context.Context, tree string) (LocalMaterializedTree, error) {
		return LocalMaterializedTree{
			Root:          root,
			SourceTreeSHA: tree,
			Restore:       proof.restore,
			Verify:        proof.verify,
			Cleanup:       func() error { return nil },
		}, nil
	}
	input.Execute = func(ctx context.Context, _ string, id GateID) (PlanGateExecution, error) {
		return executeLocalSchedulerMarkerWorkload(ctx, id, marker)
	}
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := RunLocalWorkloadMisses(testContext(), store, input, prepared)
	if runErr != nil {
		t.Fatal(runErr)
	}
	if len(result.Evidence) != 2 || result.Stats.LocalExecuted != 2 {
		t.Fatalf("restored batch result = evidence=%d stats=%#v", len(result.Evidence), result.Stats)
	}
	if proof.restoreCalls != 3 || proof.verifyCalls != 3 {
		t.Fatalf("exact-tree reset/verify calls = %d/%d, want 3/3", proof.restoreCalls, proof.verifyCalls)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("generated state remains after batch, stat error=%v", err)
	}
}

func TestLocalSchedulerCleanupFailureDoesNotPromoteEvidence(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	identity := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "cleanup-failure")
	counts := localSchedulerCounters{}
	input := localSchedulerTestInput(localSchedulerTestItem(t, identity, localPassTestEnvironment(false), false), LocalWorkloadTargetLocal, &counts)
	input.Materialize = func(_ context.Context, tree string) (LocalMaterializedTree, error) {
		return LocalMaterializedTree{
			Root:          filepath.Join("/tmp", "local-cleanup-failure"),
			SourceTreeSHA: tree,
			Restore:       func() error { return nil },
			Verify:        func() error { return nil },
			Cleanup:       func() error { return errors.New("cleanup failed") },
		}, nil
	}
	prepared, err := PrepareLocalWorkloadSchedule(testContext(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := RunLocalWorkloadMisses(testContext(), store, input, prepared)
	if runErr == nil || !strings.Contains(runErr.Error(), "PASS promotion is forbidden") {
		t.Fatalf("cleanup failure = %v", runErr)
	}
	if len(result.Evidence) != 0 {
		t.Fatalf("cleanup failure produced evidence = %d", len(result.Evidence))
	}
	hits, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("cleanup failure persisted local evidence = %d", len(hits))
	}
}

type localSchedulerTreeProof struct {
	marker       string
	restoreCalls int
	verifyCalls  int
}

func (proof *localSchedulerTreeProof) restore() error {
	proof.restoreCalls++
	if err := os.Remove(proof.marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (proof *localSchedulerTreeProof) verify() error {
	proof.verifyCalls++
	if _, err := os.Stat(proof.marker); err == nil {
		return errors.New("previous workload generated state leaked")
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func executeLocalSchedulerMarkerWorkload(_ context.Context, id GateID, marker string) (PlanGateExecution, error) {
	if _, err := os.Stat(marker); err == nil {
		return PlanGateExecution{}, errors.New("executor observed previous workload state")
	} else if !os.IsNotExist(err) {
		return PlanGateExecution{}, err
	}
	if err := os.WriteFile(marker, []byte(id), 0o600); err != nil {
		return PlanGateExecution{}, err
	}
	return schedulerExecution(id, true, id == GateIDBackendTestGuardWithRace), nil
}

func assertLocalSchedulerMissOutcome(t *testing.T, store *DurationLedgerStore, result LocalWorkloadScheduleResult, runErr error, first, second WorkloadPassIdentity, counts localSchedulerCounters) {
	t.Helper()
	if runErr == nil {
		t.Fatal("failed local workload unexpectedly returned nil")
	}
	if !slices.Equal(counts.executeIDs, []GateID{first.WorkloadID, second.WorkloadID}) {
		t.Fatalf("executed IDs = %#v, want explicit misses only", counts.executeIDs)
	}
	if counts.materialize != 1 || counts.remote != 0 || len(result.Evidence) != 1 {
		t.Fatalf("local miss counters/evidence = %#v/%d err=%v", counts, len(result.Evidence), runErr)
	}
	hits, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{first})
	if err != nil || len(hits) != 1 {
		t.Fatalf("green local evidence = %d err=%v", len(hits), err)
	}
	failed, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{second})
	if err != nil || len(failed) != 0 {
		t.Fatalf("failed local evidence = %d err=%v, want MISS", len(failed), err)
	}
}

func localSchedulerEnvironmentForWorkload(t *testing.T, workloadID GateID) LocalWorkloadPassEnvironment {
	t.Helper()
	_, program, err := executorProgramForWorkload(workloadID)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := ExecutorProgramGoFlags(program)
	if err != nil {
		t.Fatal(err)
	}
	environment := localPassTestEnvironment(false)
	environment.GoFlags = flags
	return environment
}

func recordLocalSchedulerPass(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity, environment LocalWorkloadPassEnvironment) {
	t.Helper()
	_, program, err := executorProgramForWorkload(identity.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := ExecutorProgramGoFlags(program)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	log := PlainTextLog("local scheduler mapped-ineligible PASS")
	execution := PlanGateExecution{ShardIdentity: "local/scheduler/fixture", GateID: identity.WorkloadID, Status: ResultStatusPassed, ExitCode: 0, StartedAt: now.Add(time.Millisecond), CompletedAt: now.Add(11 * time.Millisecond), ArgvDigest: identity.ExecutionDigest, Log: log, LogDigest: digestPlanLog(log), ExecutionProfile: ExecutionProfile{GoFlags: flags, CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 9, TotalMS: 10}}
	entry := LocalWorkloadPassEntry{Identity: identity, Environment: environment, Execution: execution}
	origin := localPassTestOrigin()
	origin.HostContextDigest, err = LocalWorkloadPassHostContextDigest(environment)
	if err != nil {
		t.Fatal(err)
	}
	origin.RunID = "local-scheduler-ineligible-fixture"
	origin.StartedAt = now
	origin.CompletedAt = now.Add(time.Second)
	origin.ProjectionDigest, err = LocalWorkloadPassProjectionDigest(origin, []LocalWorkloadPassEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLocalWorkloadPassBatch(LocalWorkloadPassBatch{Origin: origin, Entries: []LocalWorkloadPassEntry{entry}}); err != nil {
		t.Fatal(err)
	}
}

type localSchedulerCounters struct {
	materialize int
	restore     int
	verify      int
	execute     int
	remote      int
	executeIDs  []GateID
	failID      GateID
}

func localSchedulerTestInput(item LocalWorkloadScheduleItem, target LocalWorkloadScheduleTarget, counts *localSchedulerCounters) LocalWorkloadSchedulerInput {
	environment := localPassTestEnvironment(false)
	hostContext, _ := LocalWorkloadPassHostContextDigest(environment)
	origin := localPassTestOrigin()
	origin.HostContextDigest = hostContext
	now := time.Date(2026, time.August, 11, 13, 0, 0, 0, time.UTC)
	host := LocalHostAdmission{Allowed: true, AvailableCPU: 8, AvailableMemoryGiB: 16, CPUWindowStart: now.Add(-30 * time.Second), CPUWindowEnd: now, CPUSampleCount: 7, CPUBusyAveragePercent: 20}
	return LocalWorkloadSchedulerInput{Target: target, Items: []LocalWorkloadScheduleItem{item}, Host: host, SourceTreeSHA: strings.Repeat("a", 40), LocalGeneration: 1, Origin: origin, RunID: "scheduler-run", Now: func() time.Time { return now }, Materialize: func(_ context.Context, tree string) (LocalMaterializedTree, error) {
		counts.materialize++
		return LocalMaterializedTree{
			Root:          filepath.Join("/tmp", "local-exact-tree"),
			SourceTreeSHA: tree,
			Restore:       func() error { counts.restore++; return nil },
			Verify:        func() error { counts.verify++; return nil },
			Cleanup:       func() error { return nil },
		}, nil
	}, Execute: func(_ context.Context, _ string, id GateID) (PlanGateExecution, error) {
		counts.execute++
		counts.executeIDs = append(counts.executeIDs, id)
		if id == counts.failID {
			return schedulerExecution(id, false, id == GateIDBackendTestGuardWithRace), errors.New("canonical local workload failed")
		}
		return schedulerExecution(id, true, id == GateIDBackendTestGuardWithRace), nil
	}, Receipt: newLocalSchedulerTestReceipt(map[GateID]LocalWorkloadPassEnvironment{item.LocalIdentity.WorkloadID: environment}), RemoteExecute: func(_ context.Context, _ []GateID) error { counts.remote++; return nil }}
}

type localSchedulerTestReceipt struct {
	environments map[GateID]LocalWorkloadPassEnvironment
	reverifyErr  error
	sealed       bool
}

func newLocalSchedulerTestReceipt(environments map[GateID]LocalWorkloadPassEnvironment) LocalExecutorSessionReceipt {
	copy := make(map[GateID]LocalWorkloadPassEnvironment, len(environments))
	maps.Copy(copy, environments)
	return &localSchedulerTestReceipt{environments: copy, sealed: true}
}

func TestLocalExecutorSessionReceiptIncludesWorkloadDoesNotUseEnvironmentErrors(t *testing.T) {
	workloadID := GateIDFrontendLint
	receipt := newLocalSchedulerTestReceipt(map[GateID]LocalWorkloadPassEnvironment{workloadID: localPassTestEnvironment(false)})
	if !LocalExecutorSessionReceiptIncludesWorkload(receipt, workloadID) {
		t.Fatal("sealed receipt did not report its explicit workload binding")
	}
	if LocalExecutorSessionReceiptIncludesWorkload(receipt, GateIDCodemapCheck) {
		t.Fatal("receipt reported an unbound workload as present")
	}
	if LocalExecutorSessionReceiptIncludesWorkload(&localSchedulerTestReceipt{environments: map[GateID]LocalWorkloadPassEnvironment{workloadID: localPassTestEnvironment(false)}}, workloadID) {
		t.Fatal("unsealed receipt unexpectedly reported a workload binding")
	}
}

func (receipt *localSchedulerTestReceipt) localExecutorSessionReceiptMarker() {
	if receipt == nil {
		return
	}
}

func (receipt *localSchedulerTestReceipt) localExecutorReceiptSeal() localExecutorReceiptSeal {
	if !receipt.sealed {
		return 0
	}
	return localExecutorReceiptSealed
}

func (receipt *localSchedulerTestReceipt) IncludesWorkload(id GateID) bool {
	if receipt == nil {
		return false
	}
	_, ok := receipt.environments[id]
	return ok
}

func (receipt *localSchedulerTestReceipt) Environment(id GateID) (LocalWorkloadPassEnvironment, error) {
	environment, ok := receipt.environments[id]
	if !ok {
		return LocalWorkloadPassEnvironment{}, errors.New("test receipt environment missing")
	}
	return environment, nil
}

func (receipt *localSchedulerTestReceipt) EnvironmentFor(id GateID) (LocalWorkloadPassEnvironment, error) {
	return receipt.Environment(id)
}

func (receipt *localSchedulerTestReceipt) HostContext() (LocalWorkloadPassHostContext, error) {
	for _, environment := range receipt.environments {
		return LocalWorkloadPassHostContext{Platform: environment.Platform, GOOS: environment.GOOS, GOARCH: environment.GOARCH, CGOEnabled: environment.CGOEnabled, ToolchainClosureDigest: environment.ToolchainClosureDigest, RunnerSemanticPolicy: environment.RunnerSemanticPolicy, RunnerSemanticDigest: environment.BaseRunnerSemanticDigest}, nil
	}
	return LocalWorkloadPassHostContext{}, errors.New("test receipt host context missing")
}

func (receipt *localSchedulerTestReceipt) HostContextDigest() (string, error) {
	for _, environment := range receipt.environments {
		return LocalWorkloadPassHostContextDigest(environment)
	}
	return "", errors.New("test receipt host context missing")
}

func (receipt *localSchedulerTestReceipt) Digest() (string, error) {
	return digestForWorkloadPass("test-receipt"), nil
}

func (receipt *localSchedulerTestReceipt) TrustedGitBinary() (TrustedGitBinary, error) {
	return TrustedGitBinary{}, errors.New("test receipt does not bind a trusted git binary")
}

func (receipt *localSchedulerTestReceipt) TrustedGoBinary() (TrustedGoBinary, error) {
	return TrustedGoBinary{}, errors.New("test receipt does not bind a trusted Go binary")
}

func (receipt *localSchedulerTestReceipt) TrustedSelfBinary() (TrustedSelfBinary, error) {
	return TrustedSelfBinary{}, errors.New("test receipt does not bind a trusted self binary")
}

func (receipt *localSchedulerTestReceipt) Reverify(string) error { return receipt.reverifyErr }

func localSchedulerTestReceiptValues(t *testing.T, input LocalWorkloadSchedulerInput) map[GateID]LocalWorkloadPassEnvironment {
	t.Helper()
	receipt, ok := input.Receipt.(*localSchedulerTestReceipt)
	if !ok {
		t.Fatal("local scheduler test receipt has unexpected type")
	}
	return receipt.environments
}

func localSchedulerReceiptForItems(t *testing.T, items []LocalWorkloadScheduleItem) LocalExecutorSessionReceipt {
	t.Helper()
	environments := make(map[GateID]LocalWorkloadPassEnvironment, len(items))
	for _, item := range items {
		environments[item.LocalIdentity.WorkloadID] = localPassTestEnvironment(strings.Contains(string(item.LocalIdentity.WorkloadID), "race"))
	}
	return newLocalSchedulerTestReceipt(environments)
}

func TestLocalSchedulerRejectsUnsealedReceiptBeforeLookup(t *testing.T) {
	environment := localPassTestEnvironment(false)
	identity := localSchedulerIdentity(t, GateIDBackendTestWithGuard, environment, "unsealed")
	item := localSchedulerTestItem(t, identity, environment, false)
	input := localSchedulerTestInput(item, LocalWorkloadTargetLocal, &localSchedulerCounters{})
	input.Receipt = &localSchedulerTestReceipt{environments: map[GateID]LocalWorkloadPassEnvironment{identity.WorkloadID: environment}}
	if _, err := PrepareLocalWorkloadSchedule(testContext(), newWorkloadPassEvidenceStore(t, 1), input); err == nil || !strings.Contains(err.Error(), "producer-sealed") {
		t.Fatalf("unsealed receipt error = %v, want producer-sealed rejection", err)
	}
}

func localSchedulerTestItem(t *testing.T, local WorkloadPassIdentity, environment LocalWorkloadPassEnvironment, _ bool) LocalWorkloadScheduleItem {
	return localSchedulerTestItemWithRemote(t, local, environment, environment, 1)
}

func localSchedulerTestItemWithRemote(t *testing.T, local WorkloadPassIdentity, _, _ LocalWorkloadPassEnvironment, duration int64) LocalWorkloadScheduleItem {
	t.Helper()
	return LocalWorkloadScheduleItem{WorkloadID: local.WorkloadID, LocalKey: NewWorkloadPassKey(WorkloadPassNamespaceLocal, local.IdentityDigest), LocalIdentity: local, Resource: LocalWorkloadResource{DurationMS: duration, CPU: 1, MemoryGiB: 1}, LocalEligible: true}
}

func localSchedulerIdentity(t *testing.T, id GateID, environment LocalWorkloadPassEnvironment, suffix string) WorkloadPassIdentity {
	digest, err := LocalWorkloadPassEnvironmentDigest(environment)
	if err != nil {
		t.Fatal(err)
	}
	executionDigest, err := localWorkloadExecutionDigest(string(id))
	if err != nil {
		t.Fatal(err)
	}
	identity := WorkloadPassIdentity{WorkloadID: id, ExecutionDigest: executionDigest, InputDigest: digestForWorkloadPass("input-" + suffix), EnvironmentDigest: digest}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	return identity
}

func schedulerExecution(id GateID, passed, race bool) PlanGateExecution {
	now := time.Date(2026, time.August, 11, 13, 0, 0, 0, time.UTC)
	status, exitCode := ResultStatusFailed, 1
	if passed {
		status, exitCode = ResultStatusPassed, 0
	}
	log := PlainTextLog("scheduler execution")
	flags := CanonicalGoFlags(race)
	argvDigest, _ := localWorkloadExecutionDigest(string(id))
	return PlanGateExecution{ShardIdentity: "local/scheduler", GateID: id, Status: status, ExitCode: exitCode, StartedAt: now.Add(time.Millisecond), CompletedAt: now.Add(11 * time.Millisecond), ArgvDigest: argvDigest, Log: log, LogDigest: digestPlanLog(log), ExecutionProfile: ExecutionProfile{GoFlags: flags, CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 9, TotalMS: 10}}
}

func assertLocalSchedulerStats(t *testing.T, got, want LocalWorkloadScheduleStats) {
	t.Helper()
	if got.SelectedLocal != want.SelectedLocal || got.SelectedRemote != want.SelectedRemote || got.LocalHits != want.LocalHits || got.LocalMisses != want.LocalMisses || got.LocalExecuted != want.LocalExecuted || got.RemoteInvocations != want.RemoteInvocations {
		t.Fatalf("scheduler stats = %#v, want %#v", got, want)
	}
}

func testContext() context.Context { return context.Background() }
