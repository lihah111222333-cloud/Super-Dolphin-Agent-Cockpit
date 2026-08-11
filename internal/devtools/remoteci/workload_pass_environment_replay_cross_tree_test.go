package remoteci

import (
	"context"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCoordinatorPrepareReplaysLegacyEnvironmentAcrossWorktrees(t *testing.T) {
	originRoot, originInput := coordinatorReuseFixture(t)
	originResult := runCoordinatorFreshWorkloads(t, originInput)
	seedCoordinatorLegacyEnvironmentEvidence(t, originInput, &originResult)

	candidateRoot := cloneCrossWorktreeCandidate(t, originRoot)
	runCoordinatorGit(t, candidateRoot, "commit", "--quiet", "--allow-empty", "-m", "precise environment replay candidate")
	candidateInput := crossWorktreeCandidateInput(t, originInput, candidateRoot)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	const candidateJobID = "job-fedcba9876543210fedcba99"
	coordinator.newID = func() (string, error) { return candidateJobID, nil }
	if originResult.JobID == candidateJobID {
		t.Fatalf("environment replay target job ID must differ from origin job %q", originResult.JobID)
	}
	prepared, err := coordinator.Prepare(context.Background(), candidateInput)
	if err != nil {
		t.Fatalf("legacy environment replay Prepare() error = %v", err)
	}
	assertLegacyEnvironmentReplayPrepared(t, prepared, originInput.Tree)
	result, err := coordinator.RunPrepared(context.Background(), prepared)
	if err != nil || result.Status != gate.ResultStatusPassed || !result.CleanupComplete {
		t.Fatalf("legacy environment replay RunPrepared() result=%#v error=%v", result, err)
	}
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)
}

func assertLegacyEnvironmentReplayPrepared(t *testing.T, prepared *PreparedRun, originTree string) {
	t.Helper()
	if !prepared.AllReused() || len(prepared.reuse.cacheMisses) != 0 {
		t.Fatalf("legacy environment replay reuse = all=%t reused=%d misses=%d", prepared.AllReused(), len(prepared.reuse.reusedWorkloads), len(prepared.reuse.cacheMisses))
	}
	if len(prepared.reuse.environmentReplayProofs) != len(prepared.reuse.reusedWorkloads) {
		t.Fatalf("environment replay proofs = %d, reused evidence = %d", len(prepared.reuse.environmentReplayProofs), len(prepared.reuse.reusedWorkloads))
	}
	for _, evidence := range prepared.reuse.reusedWorkloads {
		if evidence.OriginSourceTreeSHA != originTree {
			t.Fatalf("environment replay %q origin tree = %q, want %q", evidence.Identity.WorkloadID, evidence.OriginSourceTreeSHA, originTree)
		}
		if prepared.reuse.environmentReplayProofs[string(evidence.Identity.WorkloadID)] == "" {
			t.Fatalf("environment replay %q has no persisted proof", evidence.Identity.WorkloadID)
		}
	}
}

func TestCoordinatorPrepareLegacyEnvironmentReplayRejectsObservableInputChange(t *testing.T) {
	originRoot, originInput := coordinatorReuseFixture(t)
	originResult := runCoordinatorFreshWorkloads(t, originInput)
	seedCoordinatorLegacyEnvironmentEvidence(t, originInput, &originResult)

	candidateRoot := cloneCrossWorktreeCandidate(t, originRoot)
	writeCoordinatorFixture(t, candidateRoot, "internal/fixture/fixture.go", "package fixture\n\nfunc Value() int { return 2 }\n")
	runCoordinatorGit(t, candidateRoot, "add", "internal/fixture/fixture.go")
	runCoordinatorGit(t, candidateRoot, "commit", "--quiet", "-m", "reject changed observable input")
	prepared, _, _ := prepareCrossWorktreeCandidate(t, crossWorktreeCandidateInput(t, originInput, candidateRoot))
	assertChangedFixtureWorkloadMissedSourceReplay(t, prepared)
	for _, identity := range prepared.reuse.identities {
		_, targetKind, target, targeted, err := gate.ParseWorkloadID(string(identity.WorkloadID))
		if err != nil || !targeted || targetKind != gate.WorkloadTargetGoPackage || target != "./internal/fixture" {
			continue
		}
		if _, proof := prepared.reuse.environmentReplayProofs[string(identity.WorkloadID)]; proof {
			t.Fatalf("observable input change produced environment replay proof for %q", identity.WorkloadID)
		}
	}
}

func TestCoordinatorPrepareLegacyEnvironmentReplayRejectsWorkerClosureChange(t *testing.T) {
	originRoot, originInput := coordinatorReuseFixture(t)
	originResult := runCoordinatorFreshWorkloads(t, originInput)
	seedCoordinatorLegacyEnvironmentEvidence(t, originInput, &originResult)

	candidateRoot := cloneCrossWorktreeCandidate(t, originRoot)
	writeCoordinatorFixture(t, candidateRoot, "internal/devtools/remoteci/coordinator_request.go", "package remoteci\n\nfunc createRequest() {}\nfunc remoteShardBootstrapSH() string { return \"\" }\nfunc remoteWorkerEnvironment() { return nil }\nfunc remoteWorkerSupervisorCommand() {}\n")
	runCoordinatorGit(t, candidateRoot, "add", "internal/devtools/remoteci/coordinator_request.go")
	runCoordinatorGit(t, candidateRoot, "commit", "--quiet", "-m", "reject worker closure change")
	prepared, _, _ := prepareCrossWorktreeCandidate(t, crossWorktreeCandidateInput(t, originInput, candidateRoot))
	if len(prepared.reuse.environmentReplayProofs) != 0 {
		t.Fatalf("worker closure change produced %d environment replay proofs", len(prepared.reuse.environmentReplayProofs))
	}
}

func seedCoordinatorLegacyEnvironmentEvidence(t *testing.T, input RunInput, result *RunResult) {
	t.Helper()
	snapshot, err := loadRemoteGitTreeSnapshot(context.Background(), input.RepositoryRoot, input.Tree)
	if err != nil {
		t.Fatalf("load legacy environment source tree: %v", err)
	}
	legacyDigest, err := snapshot.workerExecutionContractDigestLegacyV4(context.Background())
	if err != nil {
		t.Fatalf("derive legacy worker execution digest: %v", err)
	}
	for index := range result.WorkloadPassIdentities {
		identity := &result.WorkloadPassIdentities[index]
		goFlags, err := remoteWorkloadGoFlags(string(identity.WorkloadID))
		if err != nil {
			t.Fatalf("derive legacy workload %q GoFlags: %v", identity.WorkloadID, err)
		}
		legacyInput := input
		legacyInput.WorkerExecutionSemanticDigest = legacyDigest
		environment, err := remoteWorkloadEnvironmentDigestForGoFlags(legacyInput, 10*time.Minute, testRemoteResourcePolicy(), goFlags)
		if err != nil {
			t.Fatalf("derive legacy workload %q environment: %v", identity.WorkloadID, err)
		}
		identity.EnvironmentDigest = environment
		identity.IdentityDigest, err = gate.WorkloadPassIdentitySHA256(*identity)
		if err != nil {
			t.Fatalf("derive legacy workload %q identity: %v", identity.WorkloadID, err)
		}
	}
	promoteCoordinatorFreshWorkloads(t, input, *result)
}
