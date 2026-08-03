package remoteci

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

func newTestCoordinator(t *testing.T, store ObjectStore, runtime Runtime) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(CoordinatorConfig{
		Bucket: "ci-bucket", SourcePrefix: "baseline-artifacts/source-deltas/",
		InternalOSSEndpoint:  "oss-cn-shenzhen-internal.aliyuncs.com",
		WorkerRoleName:       "worker-role",
		ImageCacheSnapshotID: "snap-accepted-baseline",
		WorkerTimeout:        10 * time.Minute,
		PollInterval:         time.Millisecond, CleanupTimeout: time.Second,
		ResourcePolicy: testRemoteResourcePolicy(),
	}, store, runtime)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	coordinator.now = func() time.Time { now = now.Add(time.Millisecond); return now }
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234567", nil }
	return coordinator
}

func testRemoteResourcePolicy() shardresource.Policy {
	return shardresource.Policy{
		Classes: []shardresource.Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "standard", VCPU: 4, MemoryGiB: 8},
			{ID: "memory", VCPU: 4, MemoryGiB: 16},
			{ID: "maximum", VCPU: 8, MemoryGiB: 32},
		},
		Bootstrap: shardresource.BootstrapClasses{
			Guard: "small", NodeTest: "standard", GoTest: "memory",
		},
		CalibrationClass: "maximum", HeadroomPercent: 25, MinSamplesToDownsize: 5,
	}
}

func remoteRunFixture(t *testing.T) (string, RunInput) {
	t.Helper()
	repository := t.TempDir()
	runCoordinatorGit(t, repository, "init", "--quiet")
	runCoordinatorGit(t, repository, "config", "user.email", "remote-ci@example.invalid")
	runCoordinatorGit(t, repository, "config", "user.name", "Remote CI")
	writeCoordinatorFixture(t, repository, "fixture.txt", "base\n")
	writeCoordinatorFixture(t, repository, "frontend-app/package.json", "{}\n")
	runCoordinatorGit(t, repository, "add", "fixture.txt", "frontend-app/package.json")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "base")
	base := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	baseTree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	writeCoordinatorFixture(t, repository, "fixture.txt", "head\n")
	runCoordinatorGit(t, repository, "add", "fixture.txt")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "head")
	commit := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	digest := "sha256:" + strings.Repeat("a", 64)
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := gate.BuildExpandedWorkloadCatalog(plan, gate.DefaultWorkloadBootstrapPolicy(), gate.WorkloadInventory{})
	if err != nil {
		t.Fatal(err)
	}
	ledger := gate.DurationLedger{Version: 1}
	for _, workload := range catalog.Workloads {
		ledger.Samples = append(ledger.Samples, gate.DurationSample{
			Bucket: gate.DurationBucket{
				WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
				Platform: "linux/arm64", Runner: digest, Toolchain: digest,
			},
			Succeeded: true, DurationMS: 15_000,
		})
	}
	ledgerStore, ledgerSnapshot := newRemoteRunLedgerAuthority(t, ledger)
	return repository, RunInput{
		AgentTokenDigest: testRemoteAgentTokenDigest, AcceptedGeneration: 1,
		RepositoryRoot: repository,
		Commit:         commit, Tree: tree, Base: base,
		RunnerBaseCommit: base, RunnerBaseTree: baseTree,
		Source: gate.SourceSpec{
			Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
			Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
		},
		Profile: gate.ProfileLocalFast, Entrypoint: gate.CIEntrypointManualCLI,
		Platform:                     "linux/amd64",
		PolicyDigest:                 digest,
		ToolchainDigest:              digest,
		LedgerSnapshot:               ledgerSnapshot,
		LedgerStore:                  ledgerStore,
		RunnerImage:                  "registry.example/runner@" + digest,
		ImageCacheSnapshotID:         "snap-accepted-baseline",
		RunnerIdentityDigest:         digest,
		BaselineManifestDigest:       "sha256:" + strings.Repeat("c", 64),
		RunnerConfigDigest:           "sha256:" + strings.Repeat("b", 64),
		GateBinarySHA256:             digest,
		CandidateGateSourceSHA256:    digest,
		CandidateGateToolchainSHA256: digest,
		RuntimeSeedSHA256:            digest,
		OCIProjectCache:              &BaselineOCIProjectCache{Image: "registry.example/runner@" + digest, ContentManifestSHA256: "sha256:" + strings.Repeat("c", 64), MainTree: baseTree, ToolchainDigest: digest, Platform: "linux/amd64", CachePath: OCIProjectGoBuildCachePath},
	}
}

// newRemoteRunLedgerAuthority creates the same SQLite authority used by production
// and returns the snapshot read back from it, keeping test planning and persistence aligned.
func newRemoteRunLedgerAuthority(t *testing.T, ledger gate.DurationLedger) (*gate.DurationLedgerStore, gate.DurationLedgerSnapshot) {
	t.Helper()
	store, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	if _, err := store.CompareAndSwap(0, ledger); err != nil {
		t.Fatalf("initialize duration ledger SQLite authority: %v", err)
	}
	seedRemoteCITestAcceptedGeneration(t, store, 1)
	snapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load duration ledger SQLite authority: %v", err)
	}
	if snapshot.Generation != 1 || !reflect.DeepEqual(snapshot.Ledger, ledger) {
		t.Fatalf("duration ledger SQLite snapshot = %#v, want initialized ledger", snapshot)
	}
	return store, snapshot
}

func reportFromCreateRequest(request eci.CreateRequest, executionStartedAt time.Time) (gate.PlanExecutionReport, error) {
	if len(request.Args) != 8 || request.Args[0] != "worker" || request.Args[1] != "run-shard" {
		return gate.PlanExecutionReport{}, fmt.Errorf("unexpected worker args: %v", request.Args)
	}
	gateIDs := strings.Split(request.Args[7], ",")
	report := gate.PlanExecutionReport{
		SchemaVersion: 6, Profile: gate.Profile(request.Args[3]), PlanDigest: request.Args[5], AgentTokenDigest: request.Environment[gate.ExecutorAgentTokenDigestEnvironment],
	}
	emptyDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(nil))
	for _, id := range gateIDs {
		report.Gates = append(report.Gates, gate.PlanGateExecution{
			GateID: gate.GateID(id), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: executionStartedAt, CompletedAt: executionStartedAt.Add(time.Second), LogDigest: emptyDigest,
			ExecutionProfile: gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 100, TestBodyMS: 900, TotalMS: 1_000},
		})
	}
	return report, nil
}

func forceFailedCoordinatorReport(report *gate.PlanExecutionReport, failureLog string) {
	log := []byte(failureLog)
	digest := sha256.Sum256(log)
	report.Gates[0].Status = gate.ResultStatusFailed
	report.Gates[0].ExitCode = 1
	report.Gates[0].Log = log
	report.Gates[0].LogDigest = fmt.Sprintf("sha256:%x", digest)
}

func runCoordinatorGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func coordinatorGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repository
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func writeCoordinatorFixture(t *testing.T, repository string, relative string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repository, relative)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, relative), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
