package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// trustedSelfTestRoot 在非公共可写的包目录下创建严格工具权限可接受的测试根。
func trustedSelfTestRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(".", ".trusted-self-test-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove trusted self test root: %v", err)
		}
	})
	return root
}

func TestTrustedSelfBinaryIgnoresSameNamePATHFakeAndRejectsContentDrift(t *testing.T) {
	root := trustedSelfTestRoot(t)
	selfPath := writeTrustedSelfFixture(t, filepath.Join(root, "receipt-self", ExecutorSelfCommandName), "self-v1")
	fakePath := writeTrustedSelfFixture(t, filepath.Join(root, "global", ExecutorSelfCommandName), "global-fake")
	t.Setenv("PATH", filepath.Dir(fakePath))

	trusted, err := newTrustedSelfBinary(selfPath, "test-build")
	if err != nil {
		t.Fatal(err)
	}
	path, err := trusted.VerifiedPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != selfPath || path == fakePath {
		t.Fatalf("trusted self path = %q, want injected self %q and not PATH fake %q", path, selfPath, fakePath)
	}
	if err := os.WriteFile(selfPath, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := trusted.VerifiedPath(); err == nil || !strings.Contains(err.Error(), "content drifted") {
		t.Fatalf("trusted self drift error = %v, want content drift rejection", err)
	}
}

func TestTrustedSelfBinaryReceiptIdentityExcludesAbsolutePath(t *testing.T) {
	root := trustedSelfTestRoot(t)
	first, err := newTrustedSelfBinary(writeTrustedSelfFixture(t, filepath.Join(root, "one", ExecutorSelfCommandName), "same"), "test-build")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTrustedSelfBinary(writeTrustedSelfFixture(t, filepath.Join(root, "two", ExecutorSelfCommandName), "same"), "test-build")
	if err != nil {
		t.Fatal(err)
	}
	firstPayload, firstDigest, err := encodeLocalExecutorReceiptPayload(LocalWorkloadPassHostContext{}, "", nil, first, nil, nil, map[GateID]localExecutorProgramProof{}, map[GateID]LocalWorkloadPassEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	secondPayload, secondDigest, err := encodeLocalExecutorReceiptPayload(LocalWorkloadPassHostContext{}, "", nil, second, nil, nil, map[GateID]localExecutorProgramProof{}, map[GateID]LocalWorkloadPassEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if string(firstPayload) != string(secondPayload) || firstDigest != secondDigest {
		t.Fatalf("self identity depends on absolute path: %q != %q", firstDigest, secondDigest)
	}
	if strings.Contains(string(firstPayload), root) || !strings.Contains(string(firstPayload), `"name":"super-dolphin-gate"`) {
		t.Fatalf("self payload leaked runtime path or omitted logical name: %s", firstPayload)
	}
}

func TestProjectMapTrustedSelfChangesLocalPASSIdentity(t *testing.T) {
	root := trustedSelfTestRoot(t)
	selfPath := writeTrustedSelfFixture(t, filepath.Join(root, ExecutorSelfCommandName), "self-v1")
	host, programs := localReceiptIdentityTestInputs()
	firstSelf := mustTrustedSelfBinary(t, selfPath, "test-build-v1")
	first := mustLocalReceiptEnvironments(t, host, programs, firstSelf)
	replaceTrustedSelfFixture(t, selfPath, "self-v2")
	secondSelf := mustTrustedSelfBinary(t, selfPath, "test-build-v2")
	second := mustLocalReceiptEnvironments(t, host, programs, secondSelf)
	assertDistinctReceiptDigest(t, mustReceiptDigest(t, host, firstSelf, first), mustReceiptDigest(t, host, secondSelf, second))
	assertDistinctReceiptDigest(t, mustEnvironmentDigest(t, first[GateIDProjectMapCheck]), mustEnvironmentDigest(t, second[GateIDProjectMapCheck]))
	assertSameReceiptDigest(t, mustEnvironmentDigest(t, first[GateIDCodemapCheck]), mustEnvironmentDigest(t, second[GateIDCodemapCheck]))
}

func TestSealedReceiptBaseRunnerAuthorityPromotesSelfAndNonSelf(t *testing.T) {
	root := trustedSelfTestRoot(t)
	selfPath := writeTrustedSelfFixture(t, filepath.Join(root, ExecutorSelfCommandName), "self-v1")
	host, programs := localReceiptIdentityTestInputs()
	first := mustLocalReceiptEnvironments(t, host, programs, mustTrustedSelfBinary(t, selfPath, "test-build-v1"))
	assertSealedReceiptBaseAndFinalDigests(t, host, first)
	t.Run("self-only", func(t *testing.T) {
		assertSealedReceiptPromotion(t, host, first, []GateID{GateIDProjectMapCheck}, "receipt-self")
	})
	t.Run("nonself-only", func(t *testing.T) {
		assertSealedReceiptPromotion(t, host, first, []GateID{GateIDCodemapCheck}, "receipt-nonself")
	})
	t.Run("mixed", func(t *testing.T) {
		assertSealedReceiptPromotion(t, host, first, []GateID{GateIDProjectMapCheck, GateIDCodemapCheck}, "receipt-mixed")
	})
	t.Run("self-drift-only-misses-self", func(t *testing.T) { assertSealedReceiptSelfDriftMiss(t, selfPath, host, programs, first) })
	t.Run("mixed-batch-rolls-back-on-base-drift", func(t *testing.T) { assertSealedReceiptBaseDriftRollback(t, host, first) })
}

func assertSealedReceiptBaseAndFinalDigests(t *testing.T, host LocalWorkloadPassHostContext, environments map[GateID]LocalWorkloadPassEnvironment) {
	t.Helper()
	for _, id := range []GateID{GateIDProjectMapCheck, GateIDCodemapCheck} {
		if environments[id].BaseRunnerSemanticDigest != host.RunnerSemanticDigest {
			t.Fatalf("%s base runner digest = %q, want receipt host base %q", id, environments[id].BaseRunnerSemanticDigest, host.RunnerSemanticDigest)
		}
	}
	if environments[GateIDCodemapCheck].RunnerSemanticDigest != host.RunnerSemanticDigest || environments[GateIDProjectMapCheck].RunnerSemanticDigest == host.RunnerSemanticDigest {
		t.Fatal("sealed receipt did not keep non-self at base while deriving self final runner digest")
	}
}

func assertSealedReceiptPromotion(t *testing.T, host LocalWorkloadPassHostContext, environments map[GateID]LocalWorkloadPassEnvironment, ids []GateID, runID string) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	batch, identities := sealedReceiptPassBatch(t, host, environments, ids, runID)
	if err := store.RecordLocalWorkloadPassBatch(batch); err != nil {
		t.Fatalf("promote sealed receipt entries: %v", err)
	}
	hits, err := store.LookupLocalWorkloadPassEvidence(identities)
	if err != nil || len(hits) != len(identities) {
		t.Fatalf("lookup promoted sealed receipt entries: hits=%d want=%d err=%v", len(hits), len(identities), err)
	}
}

func assertSealedReceiptSelfDriftMiss(t *testing.T, selfPath string, host LocalWorkloadPassHostContext, programs map[GateID]ExecutorProgram, first map[GateID]LocalWorkloadPassEnvironment) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	firstBatch, _ := sealedReceiptPassBatch(t, host, first, []GateID{GateIDProjectMapCheck, GateIDCodemapCheck}, "receipt-first")
	if err := store.RecordLocalWorkloadPassBatch(firstBatch); err != nil {
		t.Fatal(err)
	}
	replaceTrustedSelfFixture(t, selfPath, "self-v2")
	second := mustLocalReceiptEnvironments(t, host, programs, mustTrustedSelfBinary(t, selfPath, "test-build-v2"))
	_, identities := sealedReceiptPassBatch(t, host, second, []GateID{GateIDProjectMapCheck, GateIDCodemapCheck}, "receipt-second")
	hits, err := store.LookupLocalWorkloadPassEvidence(identities)
	if err != nil || len(hits) != 1 || hits[0].Identity.WorkloadID != GateIDCodemapCheck {
		t.Fatalf("hits after sealed self drift = %#v, want only %q; err=%v", hits, GateIDCodemapCheck, err)
	}
}

func assertSealedReceiptBaseDriftRollback(t *testing.T, host LocalWorkloadPassHostContext, environments map[GateID]LocalWorkloadPassEnvironment) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	preexisting, _ := sealedReceiptPassBatch(t, host, environments, []GateID{GateIDCodemapCheck}, "receipt-preexisting-nonself")
	if err := store.RecordLocalWorkloadPassBatch(preexisting); err != nil {
		t.Fatalf("record preexisting sealed non-self entry: %v", err)
	}
	candidate, identities := sealedReceiptPassBatch(t, host, environments, []GateID{GateIDProjectMapCheck, GateIDCodemapCheck}, "receipt-rollback")
	if err := store.RecordLocalWorkloadPassBatch(candidate); err == nil || !strings.Contains(err.Error(), "insert local workload PASS evidence") {
		t.Fatalf("mixed sealed receipt rollback error = %v, want final evidence collision", err)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	assertLocalAuthorityRowCount(t, database, "ci_local_workload_origins", `run_id = ?`, candidate.Origin.RunID, 0)
	assertLocalAuthorityRowCount(t, database, "ci_local_workload_executions", `run_id = ? AND workload_id = ?`, candidate.Origin.RunID, candidate.Entries[0].Identity.WorkloadID, 0)
	assertLocalAuthorityRowCount(t, database, "ci_local_workload_pass_evidence", `identity_digest = ? AND local_generation = ?`, identities[0].IdentityDigest, fmt.Sprint(candidate.Origin.LocalGeneration), 0)
}

func sealedReceiptPassBatch(t *testing.T, host LocalWorkloadPassHostContext, environments map[GateID]LocalWorkloadPassEnvironment, ids []GateID, runID string) (LocalWorkloadPassBatch, []WorkloadPassIdentity) {
	t.Helper()
	entries := make([]LocalWorkloadPassEntry, 0, len(ids))
	identities := make([]WorkloadPassIdentity, 0, len(ids))
	for index, id := range ids {
		environment, ok := environments[id]
		if !ok {
			t.Fatalf("sealed receipt does not include %q", id)
		}
		environmentDigest := mustEnvironmentDigest(t, environment)
		executionDigest, err := localWorkloadExecutionDigest(string(id))
		if err != nil {
			t.Fatal(err)
		}
		identity := WorkloadPassIdentity{WorkloadID: id, ExecutionDigest: executionDigest, InputDigest: digestForWorkloadPass("sealed-input-" + string(id)), EnvironmentDigest: environmentDigest}
		identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
		started := time.UnixMilli(1700000000000 + int64(index)*100).UTC()
		log := PlainTextLog("sealed receipt workload passed")
		entries = append(entries, LocalWorkloadPassEntry{Identity: identity, Environment: environment, Execution: PlanGateExecution{ShardIdentity: "local/sealed/receipt", GateID: id, Status: ResultStatusPassed, ExitCode: 0, StartedAt: started, CompletedAt: started.Add(10 * time.Millisecond), ArgvDigest: identity.ExecutionDigest, Log: log, LogDigest: digestPlanLog(log), ExecutionProfile: ExecutionProfile{GoFlags: environment.GoFlags, CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 9, TotalMS: 10}}})
		identities = append(identities, identity)
	}
	origin := localPassTestOrigin()
	origin.RunID = runID
	origin.ToolchainClosureDigest = host.ToolchainClosureDigest
	origin.RunnerSemanticPolicy = host.RunnerSemanticPolicy
	origin.RunnerSemanticDigest = host.RunnerSemanticDigest
	var err error
	origin.HostContextDigest, err = LocalWorkloadPassHostContextDigest(entries[0].Environment)
	if err != nil {
		t.Fatal(err)
	}
	origin.ProjectionDigest, err = LocalWorkloadPassProjectionDigest(origin, entries)
	if err != nil {
		t.Fatal(err)
	}
	return LocalWorkloadPassBatch{Origin: origin, Entries: entries}, identities
}

func TestProjectMapTrustedSelfEnvironmentIdentityExcludesAbsolutePath(t *testing.T) {
	root := trustedSelfTestRoot(t)
	first := mustTrustedSelfBinary(t, writeTrustedSelfFixture(t, filepath.Join(root, "one", ExecutorSelfCommandName), "same"), "test-build")
	second := mustTrustedSelfBinary(t, writeTrustedSelfFixture(t, filepath.Join(root, "two", ExecutorSelfCommandName), "same"), "test-build")
	programs := map[GateID]ExecutorProgram{GateIDProjectMapCheck: executorPrograms[GateIDProjectMapCheck]}
	host := localReceiptTestHost(localPassTestEnvironment(false))
	firstEnvironment := mustLocalReceiptEnvironments(t, host, programs, first)[GateIDProjectMapCheck]
	secondEnvironment := mustLocalReceiptEnvironments(t, host, programs, second)[GateIDProjectMapCheck]
	assertSameReceiptDigest(t, mustEnvironmentDigest(t, firstEnvironment), mustEnvironmentDigest(t, secondEnvironment))
}

func TestProjectMapReceiptRejectsTrustedSelfDriftBeforePASSLookup(t *testing.T) {
	root := trustedSelfTestRoot(t)
	selfPath := writeTrustedSelfFixture(t, filepath.Join(root, ExecutorSelfCommandName), "self-v1")
	receipt := localReceiptWithProjectMapSelf(localPassTestEnvironment(false), mustTrustedSelfBinary(t, selfPath, "test-build"))
	replaceTrustedSelfFixture(t, selfPath, "self-v2")
	_, projectMapErr := receipt.Environment(GateIDProjectMapCheck)
	_, codemapErr := receipt.Environment(GateIDCodemapCheck)
	assertTrustedSelfDrift(t, projectMapErr)
	assertNoReceiptError(t, codemapErr)
}

func TestProjectMapReceiptAndProgramUseInjectedTrustedSelf(t *testing.T) {
	root := trustedSelfTestRoot(t)
	source := filepath.Join(root, "source")
	mustProjectMapReceiptInput(t, source)
	selfPath := writeTrustedSelfFixture(t, filepath.Join(root, "self", ExecutorSelfCommandName), "self")
	fakePath := writeTrustedSelfFixture(t, filepath.Join(root, "global", ExecutorSelfCommandName), "fake")
	t.Setenv("PATH", filepath.Dir(fakePath))
	trusted, err := newTrustedSelfBinary(selfPath, "test-build")
	if err != nil {
		t.Fatal(err)
	}
	trustedPath, err := trusted.VerifiedPath()
	if err != nil {
		t.Fatal(err)
	}
	program := executorPrograms[GateIDProjectMapCheck]
	steps, err := prepareLocalExecutorStepsWithSelf(filepath.Dir(fakePath), source, program, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].binary != trustedPath || steps[0].binary == fakePath {
		t.Fatalf("project-map resolved step = %#v, want receipt self %q not global fake %q", steps, trustedPath, fakePath)
	}
	names := localReceiptToolNames(map[GateID]ExecutorProgram{GateIDProjectMapCheck: program})
	if _, found := names[ExecutorSelfCommandName]; found {
		t.Fatal("ProjectMap self was incorrectly sent to fixed receipt tool directories")
	}
	if _, found := names["node"]; !found {
		t.Fatal("ProjectMap nested Node runtime is absent from the receipt tool closure")
	}
}

func TestLocalExecutorSessionExecuteRejectsReceiptSelfDriftBeforeAnyStep(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	step := writeTrustedSelfFixture(t, filepath.Join(root, "step"), "touch "+marker)
	session := &LocalExecutorSession{
		receipt: &driftedSelfReceipt{}, programs: map[GateID]ExecutorProgram{GateIDProjectMapCheck: {Strategy: ExecutorStrategyCommands}},
		steps: map[GateID][]resolvedStep{GateIDProjectMapCheck: {{binary: step}}},
	}
	result, err := session.Execute(context.Background(), GateIDProjectMapCheck)
	if err == nil || !strings.Contains(err.Error(), "content drifted") {
		t.Fatalf("execute error = %v, want receipt self drift rejection", err)
	}
	if result.Status == ResultStatusPassed {
		t.Fatalf("drifted receipt returned execution result %#v, want no execution/PASS", result)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drifted receipt executed a step: %v", err)
	}
}

func TestLocalExecutorSessionRequiresReceiptBoundSelfBeforeProgramPreparation(t *testing.T) {
	_, err := NewLocalExecutorSessionWithReceipt(t.TempDir(), localExecutorTestClock(), []GateID{GateIDProjectMapCheck}, LocalExecutorDependencyInputs{}, &missingSelfReceipt{})
	if err == nil || !strings.Contains(err.Error(), "trusted self binary proof is missing") {
		t.Fatalf("session error = %v, want receipt-bound self rejection", err)
	}
}

func localReceiptIdentityTestInputs() (LocalWorkloadPassHostContext, map[GateID]ExecutorProgram) {
	return localReceiptTestHost(localPassTestEnvironment(false)), map[GateID]ExecutorProgram{
		GateIDProjectMapCheck: executorPrograms[GateIDProjectMapCheck],
		GateIDCodemapCheck:    executorPrograms[GateIDCodemapCheck],
	}
}

func localReceiptTestHost(environment LocalWorkloadPassEnvironment) LocalWorkloadPassHostContext {
	return LocalWorkloadPassHostContext{
		Platform: environment.Platform, GOOS: environment.GOOS, GOARCH: environment.GOARCH,
		GOAMD64: environment.GOAMD64, GOARM64: environment.GOARM64, CGOEnabled: environment.CGOEnabled,
		GOEXPERIMENT: environment.GOEXPERIMENT, CC: environment.CC, CXX: environment.CXX, SDK: environment.SDK,
		OSBuild: environment.OSBuild, GoVersion: environment.GoVersion, ToolchainClosureDigest: environment.ToolchainClosureDigest,
		RunnerSemanticPolicy: environment.RunnerSemanticPolicy, RunnerSemanticDigest: environment.BaseRunnerSemanticDigest,
	}
}

func mustTrustedSelfBinary(t *testing.T, path, version string) TrustedSelfBinary {
	t.Helper()
	trusted, err := newTrustedSelfBinary(path, version)
	if err != nil {
		t.Fatal(err)
	}
	return trusted
}

func replaceTrustedSelfFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustLocalReceiptEnvironments(t *testing.T, host LocalWorkloadPassHostContext, programs map[GateID]ExecutorProgram, self TrustedSelfBinary) map[GateID]LocalWorkloadPassEnvironment {
	t.Helper()
	environments, err := localReceiptEnvironments(host, programs, self)
	if err != nil {
		t.Fatal(err)
	}
	return environments
}

func mustEnvironmentDigest(t *testing.T, environment LocalWorkloadPassEnvironment) string {
	t.Helper()
	digest, err := LocalWorkloadPassEnvironmentDigest(environment)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mustReceiptDigest(t *testing.T, host LocalWorkloadPassHostContext, self TrustedSelfBinary, environments map[GateID]LocalWorkloadPassEnvironment) string {
	t.Helper()
	_, digest, err := encodeLocalExecutorReceiptPayload(host, "", nil, self, nil, nil, map[GateID]localExecutorProgramProof{}, environments)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func assertDistinctReceiptDigest(t *testing.T, first, second string) {
	t.Helper()
	if first == second {
		t.Fatal("trusted self drift did not change ProjectMap local PASS identity")
	}
}

func assertSameReceiptDigest(t *testing.T, first, second string) {
	t.Helper()
	if first != second {
		t.Fatalf("trusted self path or drift changed a non-self local PASS identity: %q != %q", first, second)
	}
}

func localReceiptWithProjectMapSelf(environment LocalWorkloadPassEnvironment, self TrustedSelfBinary) *localExecutorSessionReceipt {
	return &localExecutorSessionReceipt{
		environments: map[GateID]LocalWorkloadPassEnvironment{GateIDProjectMapCheck: environment, GateIDCodemapCheck: environment},
		programs: map[GateID]localExecutorProgramProof{
			GateIDProjectMapCheck: {steps: []localExecutorStepProof{{argv: []string{ExecutorSelfCommandName}}}},
			GateIDCodemapCheck:    {steps: []localExecutorStepProof{{argv: []string{"make"}}}},
		},
		self: self,
	}
}

func assertTrustedSelfDrift(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("ProjectMap pre-lookup self drift was accepted")
	}
	if !strings.Contains(err.Error(), "content drifted") {
		t.Fatalf("ProjectMap pre-lookup self drift error = %v, want content drift rejection", err)
	}
}

func assertNoReceiptError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("non-self pre-lookup environment unexpectedly depends on drifted self: %v", err)
	}
}

func writeTrustedSelfFixture(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustProjectMapReceiptInput(t *testing.T, source string) {
	t.Helper()
	for _, path := range []string{".git", "scripts", "docs/doc/codemap/project-map"} {
		if err := os.MkdirAll(filepath.Join(source, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "scripts/codemap_policy.txt"), []byte("policy"), 0o600); err != nil {
		t.Fatal(err)
	}
}

type driftedSelfReceipt struct{}

func (receipt *driftedSelfReceipt) localExecutorSessionReceiptMarker() {
	if receipt == nil {
		return
	}
}
func (*driftedSelfReceipt) IncludesWorkload(GateID) bool { return true }
func (*driftedSelfReceipt) Environment(GateID) (LocalWorkloadPassEnvironment, error) {
	return LocalWorkloadPassEnvironment{}, nil
}
func (*driftedSelfReceipt) EnvironmentFor(id GateID) (LocalWorkloadPassEnvironment, error) {
	return (&driftedSelfReceipt{}).Environment(id)
}
func (*driftedSelfReceipt) HostContext() (LocalWorkloadPassHostContext, error) {
	return LocalWorkloadPassHostContext{}, nil
}
func (*driftedSelfReceipt) HostContextDigest() (string, error) {
	return digestForWorkloadPass("host"), nil
}
func (*driftedSelfReceipt) Digest() (string, error) { return digestForWorkloadPass("receipt"), nil }
func (*driftedSelfReceipt) TrustedGitBinary() (TrustedGitBinary, error) {
	return TrustedGitBinary{}, nil
}
func (*driftedSelfReceipt) TrustedGoBinary() (TrustedGoBinary, error) { return TrustedGoBinary{}, nil }
func (*driftedSelfReceipt) TrustedSelfBinary() (TrustedSelfBinary, error) {
	return TrustedSelfBinary{}, errors.New("trusted self binary content drifted")
}
func (*driftedSelfReceipt) localExecutorReceiptSeal() localExecutorReceiptSeal {
	return localExecutorReceiptSealed
}
func (*driftedSelfReceipt) Reverify(string) error {
	return errors.New("trusted self binary content drifted")
}

type missingSelfReceipt struct{ driftedSelfReceipt }

func (*missingSelfReceipt) Reverify(string) error { return nil }
func (*missingSelfReceipt) TrustedSelfBinary() (TrustedSelfBinary, error) {
	return TrustedSelfBinary{}, errors.New("trusted self binary proof is missing")
}
