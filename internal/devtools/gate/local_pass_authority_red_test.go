package gate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLocalPassNamespaceKeyIsExplicitAndDistinct(t *testing.T) {
	identity := "sha256:" + strings.Repeat("a", 64)
	remote := NewWorkloadPassKey(WorkloadPassNamespaceRemote, identity)
	local := NewWorkloadPassKey(WorkloadPassNamespaceLocal, identity)
	if remote.String() != "remote:"+identity {
		t.Fatalf("remote key = %q, want remote prefix", remote)
	}
	if local.String() != "local:"+identity {
		t.Fatalf("local key = %q, want local prefix", local)
	}
	if remote.String() == local.String() {
		t.Fatal("local and remote PASS keys must not collide")
	}
	if _, err := ParseWorkloadPassKey(remote.String()); err != nil {
		t.Fatalf("parse remote key: %v", err)
	}
	if _, err := ParseWorkloadPassKey(local.String()); err != nil {
		t.Fatalf("parse local key: %v", err)
	}
}

func TestLocalPassEnvironmentDigestHasIndependentDomain(t *testing.T) {
	local := LocalWorkloadPassEnvironment{
		Platform: "darwin/arm64", GOOS: "darwin", GOARCH: "arm64", CGOEnabled: "0",
		GoFlags: "", ToolchainClosureDigest: "sha256:" + strings.Repeat("b", 64),
		RunnerSemanticPolicy:     LocalWorkloadRunnerSemanticPolicy,
		BaseRunnerSemanticDigest: "sha256:" + strings.Repeat("c", 64),
		RunnerSemanticDigest:     "sha256:" + strings.Repeat("c", 64),
	}
	localDigest, err := LocalWorkloadPassEnvironmentDigest(local)
	if err != nil {
		t.Fatalf("local digest: %v", err)
	}
	if !strings.HasPrefix(localDigest, "sha256:") {
		t.Fatalf("local digest = %q, want sha256 digest", localDigest)
	}
}

func TestLocalPassEnvironmentDigestRejectsMissingClosure(t *testing.T) {
	_, err := LocalWorkloadPassEnvironmentDigest(LocalWorkloadPassEnvironment{
		Platform: "darwin/arm64", GOOS: "darwin", GOARCH: "arm64", CGOEnabled: "0",
		RunnerSemanticPolicy:     LocalWorkloadRunnerSemanticPolicy,
		BaseRunnerSemanticDigest: "sha256:" + strings.Repeat("c", 64),
		RunnerSemanticDigest:     "sha256:" + strings.Repeat("c", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "toolchain closure") {
		t.Fatalf("missing closure error = %v, want fail-fast closure validation", err)
	}
}

func TestLocalPassEnvironmentDigestBindsRunnerSemanticDigest(t *testing.T) {
	base := LocalWorkloadPassEnvironment{
		Platform: "darwin/arm64", GOOS: "darwin", GOARCH: "arm64", CGOEnabled: "0",
		ToolchainClosureDigest:   "sha256:" + strings.Repeat("b", 64),
		RunnerSemanticPolicy:     LocalWorkloadRunnerSemanticPolicy,
		BaseRunnerSemanticDigest: "sha256:" + strings.Repeat("c", 64),
		RunnerSemanticDigest:     "sha256:" + strings.Repeat("c", 64),
	}
	first, err := LocalWorkloadPassEnvironmentDigest(base)
	if err != nil {
		t.Fatalf("base digest: %v", err)
	}
	base.RunnerSemanticDigest = "sha256:" + strings.Repeat("d", 64)
	second, err := LocalWorkloadPassEnvironmentDigest(base)
	if err != nil {
		t.Fatalf("changed runner digest: %v", err)
	}
	if first == second {
		t.Fatal("runner semantic closure drift must change local environment digest")
	}
}

func TestLocalPassProjectionDigestUsesCanonicalUnixMillis(t *testing.T) {
	zone := time.FixedZone("local-test", 9*60*60)
	first := localPassTestOrigin()
	first.StartedAt = time.UnixMilli(1700000000123).In(zone).Add(400 * time.Microsecond)
	first.CompletedAt = first.StartedAt.Add(10 * time.Millisecond)
	second := first
	second.StartedAt = time.UnixMilli(first.StartedAt.UnixMilli()).UTC()
	second.CompletedAt = time.UnixMilli(first.CompletedAt.UnixMilli()).UTC()
	firstDigest, err := LocalWorkloadPassProjectionDigest(first, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := LocalWorkloadPassProjectionDigest(second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("projection digest drifted across equivalent Unix milliseconds: %q != %q", firstDigest, secondDigest)
	}
}

func TestLocalPassLookupRejectsOriginExecutionColumnTamper(t *testing.T) {
	store, batch, identity := localPassAuthorityFixture(t)
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_local_workload_executions SET completed_at_unix_ms = completed_at_unix_ms + 1 WHERE run_id = ?`, batch.Origin.RunID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if _, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil || !strings.Contains(err.Error(), "timing columns diverge") {
		t.Fatalf("origin execution column tamper error = %v, want strict mismatch", err)
	}
}

func TestLocalPassLookupRejectsHostAdmissionAuditTamper(t *testing.T) {
	store, _, identity := localPassAuthorityFixture(t)
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_local_workload_origins SET cpu_busy_average_percent = 71 WHERE run_id = ?`, "local-fixture-run"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if _, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil || !strings.Contains(err.Error(), "CPU busy average exceeds") {
		t.Fatalf("host admission audit tamper error = %v, want strict rejection", err)
	}
}

func TestLocalPassLookupRejectsEvidenceOriginExecutionDivergence(t *testing.T) {
	store, batch, identity := localPassAuthorityFixture(t)
	database := openWorkloadPassDatabase(t, store)
	var encoded string
	if err := database.QueryRow(`SELECT origin_execution_json FROM ci_local_workload_pass_evidence WHERE identity_digest = ? AND local_generation = '1'`, identity.IdentityDigest).Scan(&encoded); err != nil {
		database.Close()
		t.Fatal(err)
	}
	var forged PlanGateExecution
	if err := json.Unmarshal([]byte(encoded), &forged); err != nil {
		database.Close()
		t.Fatal(err)
	}
	forged.CompletedAt = forged.CompletedAt.Add(time.Millisecond)
	evidence := WorkloadPassEvidence{Identity: identity, OriginJobID: localWorkloadPassOriginJobPrefix + batch.Origin.RunID, OriginAcceptedGeneration: batch.Origin.LocalGeneration, OriginSourceTreeSHA: batch.Origin.SourceTreeSHA, OriginReceiptSetSHA256: batch.Origin.ProjectionDigest, OriginExecution: forged}
	var err error
	evidence.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	forgedJSON, err := json.Marshal(forged)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE ci_local_workload_pass_evidence SET origin_execution_json = ?, evidence_sha256 = ? WHERE identity_digest = ? AND local_generation = '1'`, string(forgedJSON), evidence.EvidenceSHA256, identity.IdentityDigest); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if _, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil || !strings.Contains(err.Error(), "diverges from origin execution") {
		t.Fatalf("origin/evidence execution divergence error = %v, want strict mismatch", err)
	}
}

func localPassAuthorityFixture(t *testing.T) (*DurationLedgerStore, LocalWorkloadPassBatch, WorkloadPassIdentity) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	workloadID := GateIDBackendTestWithGuard
	environment := localPassTestEnvironment(false)
	environmentDigest, err := LocalWorkloadPassEnvironmentDigest(environment)
	if err != nil {
		t.Fatal(err)
	}
	executionDigest, err := localWorkloadExecutionDigest(string(workloadID))
	if err != nil {
		t.Fatal(err)
	}
	identity := WorkloadPassIdentity{WorkloadID: workloadID, ExecutionDigest: executionDigest, InputDigest: digestForWorkloadPass("local-input"), EnvironmentDigest: environmentDigest}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	log := PlainTextLog("local canonical workload passed")
	execution := PlanGateExecution{ShardIdentity: "local/canonical/fixture", GateID: workloadID, Status: ResultStatusPassed, ExitCode: 0, StartedAt: now.Add(time.Millisecond), CompletedAt: now.Add(11 * time.Millisecond), ArgvDigest: identity.ExecutionDigest, Log: log, LogDigest: digestPlanLog(log), ExecutionProfile: ExecutionProfile{GoFlags: CanonicalGoFlags(false), CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 9, TotalMS: 10}}
	entry := LocalWorkloadPassEntry{Identity: identity, Environment: environment, Execution: execution}
	origin := localPassTestOrigin()
	origin.HostContextDigest, err = LocalWorkloadPassHostContextDigest(environment)
	if err != nil {
		t.Fatal(err)
	}
	origin.RunID = "local-fixture-run"
	origin.StartedAt = now
	origin.CompletedAt = now.Add(time.Second)
	origin.ProjectionDigest, err = LocalWorkloadPassProjectionDigest(origin, []LocalWorkloadPassEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	batch := LocalWorkloadPassBatch{Origin: origin, Entries: []LocalWorkloadPassEntry{entry}}
	if err := store.RecordLocalWorkloadPassBatch(batch); err != nil {
		t.Fatalf("record local PASS fixture: %v", err)
	}
	return store, batch, identity
}

func localPassTestOrigin() LocalWorkloadPassOrigin {
	return LocalWorkloadPassOrigin{RunID: "local-test-run", LocalGeneration: 1, SourceTreeSHA: strings.Repeat("a", 40), CatalogDigest: digestForWorkloadPass("local-catalog"), HostContextDigest: digestForWorkloadPass("local-host-context"), ToolchainClosureDigest: digestForWorkloadPass("local-toolchain"), RunnerSemanticPolicy: LocalWorkloadRunnerSemanticPolicy, RunnerSemanticDigest: digestForWorkloadPass("local-runner"), CPUWindowStart: time.UnixMilli(1699999970000).UTC(), CPUWindowEnd: time.UnixMilli(1700000000000).UTC(), CPUSampleCount: 7, CPUBusyAveragePercent: 20, AvailableCPU: 8, AvailableMemoryGiB: 16, Status: ResultStatusPassed, CleanupComplete: true, StartedAt: time.UnixMilli(1700000000000).UTC(), CompletedAt: time.UnixMilli(1700000001000).UTC()}
}

func localPassTestEnvironment(race bool) LocalWorkloadPassEnvironment {
	flags := CanonicalGoFlags(false)
	if race {
		flags = CanonicalGoFlags(true)
	}
	return LocalWorkloadPassEnvironment{Platform: "darwin/arm64", GOOS: "darwin", GOARCH: "arm64", CGOEnabled: "0", GoFlags: flags, ToolchainClosureDigest: digestForWorkloadPass("local-toolchain"), RunnerSemanticPolicy: LocalWorkloadRunnerSemanticPolicy, BaseRunnerSemanticDigest: digestForWorkloadPass("local-runner"), RunnerSemanticDigest: digestForWorkloadPass("local-runner")}
}
