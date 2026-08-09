package remoteci

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
	_ "modernc.org/sqlite"
)

func newTestCoordinator(t *testing.T, store ObjectStore, runtime Runtime) *Coordinator {
	t.Helper()
	bindCoordinatorTestManifestRegistry(store, runtime)
	coordinator, err := NewCoordinator(CoordinatorConfig{
		Bucket: "ci-bucket", SourcePrefix: "baseline-artifacts/source-bundles/",
		InternalOSSEndpoint:  "oss-cn-shenzhen-internal.aliyuncs.com",
		WorkerRoleName:       "worker-role",
		ImageCacheSnapshotID: "snap-refreshed-runtime",
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

// runCoordinatorTest explicitly separates the side-effect-free preparation phase
// from execution so tests cannot reintroduce the removed Coordinator.Run wrapper.
func runCoordinatorTest(t *testing.T, coordinator *Coordinator, ctx context.Context, input RunInput) (RunResult, error) {
	t.Helper()
	prepared, err := coordinator.Prepare(ctx, input)
	if err != nil {
		return RunResult{}, err
	}
	return coordinator.RunPrepared(ctx, prepared)
}

func testRemoteResourcePolicy() shardresource.Policy {
	return shardresource.Policy{
		Classes: []shardresource.Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "medium", VCPU: 4, MemoryGiB: 8},
			{ID: "maximum", VCPU: 8, MemoryGiB: 16},
		},
		Bootstrap: shardresource.BootstrapClasses{
			Guard: "small", NodeTest: "small", GoTest: "small",
		},
		CalibrationResource:       shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
		FastWorkloadMaxDurationMS: 5_000, MediumWorkloadMaxDurationMS: 70_000,
		HeadroomPercent: 25, MinSamplesToDownsize: 5,
	}
}

func remoteRunFixture(t *testing.T) (string, RunInput) {
	t.Helper()
	repository, base, baseTree := prepareRemoteRunFixtureBase(t)
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
	inputDigests, ledgerStore, ledgerSnapshot := remoteRunFixtureLedger(t, repository, tree, plan, digest)
	return repository, remoteRunFixtureInput(repository, base, baseTree, commit, tree, digest, inputDigests, ledgerStore, ledgerSnapshot)
}

func prepareRemoteRunFixtureBase(t *testing.T) (string, string, string) {
	t.Helper()
	repository, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve remote CI fixture repository: %v", err)
	}
	runCoordinatorGit(t, repository, "init", "--quiet")
	runCoordinatorGit(t, repository, "config", "user.email", "remote-ci@example.invalid")
	runCoordinatorGit(t, repository, "config", "user.name", "Remote CI")
	writeRemoteRunFixtureFiles(t, repository)
	runCoordinatorGit(t, repository, "add", "fixture.txt", "go.mod", "go.sum", "build/gate/runtime-proxy/go.mod", "build/gate/runtime-proxy/go.sum", "internal/devtools/gate/executor_mapping.go", "scripts/check_nested_go_modules.sh", "scripts/real_go_resolver.sh", "scripts/test_with_guard.sh", "internal/fixture/fixture.go", "internal/provider/provider.go", "internal/platform/platform.go", "internal/module/thread/thread.go", "frontend-app/package.json", "frontend-app/package-lock.json", "frontend-app/tests/e2e/business-flows.spec.js", "frontend-app/tests/e2e/desktop-wide.spec.js", "frontend-app/playwright.business-flows.config.js", "frontend-app/playwright.desktop-wide.config.js")
	runCoordinatorGit(t, repository, "add", "frontend-app/scripts/remote-preflight-carriers", "frontend-app/scripts/remote-suite-carriers")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "base")
	return repository, coordinatorGitOutput(t, repository, "rev-parse", "HEAD"), coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
}

// writeRemoteRunFixtureFiles 保持合成候选为最小真实 Go 模块，使展开目录和 workload 指纹解析与生产一致的包、守卫闭包。
func writeRemoteRunFixtureFiles(t *testing.T, repository string) {
	t.Helper()
	writeCoordinatorFixture(t, repository, "fixture.txt", "base\n")
	writeCoordinatorFixture(t, repository, "go.mod", "module example.invalid/remote-fixture\n\ngo 1.26.5\n")
	writeCoordinatorFixture(t, repository, "go.sum", "")
	writeCoordinatorFixture(t, repository, "build/gate/runtime-proxy/go.mod", "module example.invalid/remote-fixture/runtime-proxy\n\ngo 1.26.5\n")
	writeCoordinatorFixture(t, repository, "build/gate/runtime-proxy/go.sum", "")
	writeCoordinatorFixture(t, repository, "internal/devtools/gate/executor_mapping.go", "package gate\n")
	writeCoordinatorFixture(t, repository, "scripts/check_nested_go_modules.sh", "#!/usr/bin/env bash\nset -euo pipefail\n")
	writeCoordinatorFixture(t, repository, "scripts/real_go_resolver.sh", "#!/usr/bin/env bash\nset -euo pipefail\n")
	writeCoordinatorFixture(t, repository, "scripts/test_with_guard.sh", "#!/usr/bin/env bash\nset -euo pipefail\n\n# REMOTE_WORKLOAD_FINGERPRINT_CANONICAL_BEGIN\n# REMOTE_WORKLOAD_FINGERPRINT_CANONICAL_END\n")
	writeCoordinatorFixture(t, repository, "internal/fixture/fixture.go", "package fixture\n\nfunc Value() int { return 1 }\n")
	writeCoordinatorFixture(t, repository, "internal/provider/provider.go", "package provider\n")
	writeCoordinatorFixture(t, repository, "internal/platform/platform.go", "package platform\n")
	writeCoordinatorFixture(t, repository, "internal/module/thread/thread.go", "package thread\n")
	writeCoordinatorFixture(t, repository, "frontend-app/package.json", "{}\n")
	writeCoordinatorFixture(t, repository, "frontend-app/package-lock.json", "{\"name\":\"fixture\",\"lockfileVersion\":3,\"packages\":{}}\n")
	writeCoordinatorFixture(t, repository, "frontend-app/tests/e2e/business-flows.spec.js", "test('business flows', async () => {})\n")
	writeCoordinatorFixture(t, repository, "frontend-app/tests/e2e/desktop-wide.spec.js", "test('desktop wide', async () => {})\n")
	writeCoordinatorFixture(t, repository, "frontend-app/playwright.business-flows.config.js", "module.exports = {}\n")
	writeCoordinatorFixture(t, repository, "frontend-app/playwright.desktop-wide.config.js", "module.exports = {}\n")
	writeRemoteRunFrontendCarrierFixtures(t, repository)
}

func writeRemoteRunFrontendCarrierFixtures(t *testing.T, repository string) {
	t.Helper()
	for _, target := range gate.FrontendPreflightTargets() {
		carrier, err := gate.FrontendPreflightCarrierTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		writeCoordinatorFixture(t, repository, "frontend-app/"+carrier, "// protocol carrier\n")
	}
	for _, carrier := range []string{gate.FrontendChangedSuiteCarrierTarget, gate.FrontendFullSuiteCarrierTarget} {
		writeCoordinatorFixture(t, repository, "frontend-app/"+carrier, "// protocol carrier\n")
	}
}

func remoteRunFixtureLedger(t *testing.T, repository, tree string, plan gate.GatePlan, digest string) (map[string]string, *gate.DurationLedgerStore, gate.DurationLedgerSnapshot) {
	t.Helper()
	catalog, err := gate.BuildExpandedWorkloadCatalog(plan, gate.DefaultWorkloadBootstrapPolicy(), gate.WorkloadInventory{GoPackages: []string{"./internal/fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	inputDigests, _, _, err := remoteWorkloadFingerprintsWithSnapshot(context.Background(), repository, tree, remoteShardableWorkloads(catalog))
	if err != nil {
		t.Fatalf("derive remote CI fixture workload input digests: %v", err)
	}
	catalog, err = bindRemoteWorkloadInputDigests(catalog, inputDigests)
	if err != nil {
		t.Fatalf("bind remote CI fixture workload input digests: %v", err)
	}
	overhead := &gate.ShardOrchestrationOverhead{SchemaVersion: gate.ShardOrchestrationOverheadSchemaVersion, PolicyVersion: gate.ShardOverheadPolicyVersion, Platform: "linux/amd64", Runner: digest, Toolchain: digest, CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8, P95MS: 1000, SampleCount: 1, ProvenanceDigest: "sha256:" + strings.Repeat("d", 64), AcceptedGeneration: 1, AcceptedSnapshotID: "snap-accepted-baseline"}
	ledger := gate.DurationLedger{Version: 1, ShardOverhead: overhead}
	appendRemoteRunFixtureSamples(&ledger, catalog, digest)
	ledgerStore, ledgerSnapshot := newRemoteRunLedgerAuthority(t, ledger)
	return inputDigests, ledgerStore, ledgerSnapshot
}

func appendRemoteRunFixtureSamples(ledger *gate.DurationLedger, catalog gate.WorkloadCatalog, digest string) {
	for _, workload := range catalog.Workloads {
		inputDigest := workload.InputDigest
		if inputDigest == "" {
			inputDigest = "sha256:" + strings.Repeat("0", 64)
		}
		for _, resource := range []struct {
			classID     string
			cpu, memory float64
		}{{"small", 2, 4}, {"medium", 4, 8}, {"maximum", 8, 16}} {
			ledger.Samples = append(ledger.Samples, gate.DurationSample{Bucket: gate.DurationBucket{WorkloadID: workload.ID, CommandDigest: workload.CommandDigest, InputDigest: inputDigest, Platform: "linux/amd64", Runner: digest, Toolchain: digest, ExecutionMode: gate.DurationExecutionModeNormal, ResourceClassID: resource.classID, ResourceCPU: resource.cpu, ResourceMemoryGiB: resource.memory}, Succeeded: true, DurationMS: 15_000})
		}
	}
}

func remoteRunFixtureInput(repository, base, baseTree, commit, tree, digest string, inputDigests map[string]string, ledgerStore *gate.DurationLedgerStore, ledgerSnapshot gate.DurationLedgerSnapshot) RunInput {
	return RunInput{
		AgentTokenDigest: testRemoteAgentTokenDigest, AcceptedGeneration: 1,
		RepositoryRoot: repository,
		Commit:         commit, Tree: tree, Base: base,
		RunnerBaseCommit: base, RunnerBaseTree: baseTree,
		Source: gate.SourceSpec{
			Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
			Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
		},
		Profile: gate.ProfileLocalFast, Entrypoint: gate.CIEntrypointManualCLI,
		Platform:                      "linux/amd64",
		PolicyDigest:                  digest,
		ToolchainDigest:               digest,
		LedgerSnapshot:                ledgerSnapshot,
		LedgerStore:                   ledgerStore,
		RunnerImage:                   "registry.example/runner@" + digest,
		ImageCacheSnapshotID:          "snap-accepted-baseline",
		ExecutionRunnerImage:          "ghcr.io/remote-builder@sha256:" + strings.Repeat("c", 64),
		ExecutionImageCacheSnapshotID: "snap-refreshed-runtime",
		ImageCacheOnly:                true,
		RunnerIdentityDigest:          digest,
		BaselineManifestDigest:        "sha256:" + strings.Repeat("c", 64),
		RunnerConfigDigest:            "sha256:" + strings.Repeat("b", 64),
		GateBinarySHA256:              digest,
		CandidateGateSourceSHA256:     "sha256:" + strings.Repeat("d", 64),
		CandidateGateToolchainSHA256:  "sha256:" + strings.Repeat("e", 64),
		RuntimeSeedSHA256:             digest,
		Inventory:                     gate.WorkloadInventory{GoPackages: []string{"./internal/fixture"}},
		WorkloadInputDigests:          inputDigests,
		OCIProjectCache:               &BaselineOCIProjectCache{Image: "registry.example/runner@" + digest, ContentManifestSHA256: "sha256:" + strings.Repeat("c", 64), MainTree: baseTree, ToolchainDigest: digest, Platform: "linux/amd64", CachePath: OCIProjectGoBuildCachePath},
	}
}

// newRemoteRunLedgerAuthority creates the same SQLite authority used by production
// and returns the snapshot read back from it, keeping test planning and persistence aligned.
func newRemoteRunLedgerAuthority(t *testing.T, ledger gate.DurationLedger) (*gate.DurationLedgerStore, gate.DurationLedgerSnapshot) {
	t.Helper()
	overhead := ledger.ShardOverhead
	ledger.ShardOverhead = nil
	store, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	if _, err := store.CompareAndSwap(0, ledger); err != nil {
		t.Fatalf("initialize duration ledger SQLite authority: %v", err)
	}
	seedRemoteCITestAcceptedGeneration(t, store, 1)
	if overhead != nil {
		seedRemoteRunOverheadForeignKey(t, store)
		sample := gate.ShardOrchestrationOverheadSample{
			AcceptedGeneration:     overhead.AcceptedGeneration,
			ProvenanceDigest:       overhead.ProvenanceDigest,
			JobID:                  "fixture-shard-overhead",
			ShardIdentity:          "fixture-shard",
			TotalStartedAt:         time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
			TotalCompletedAt:       time.Date(2026, 7, 27, 0, 0, 2, 0, time.UTC),
			WorkloadEnvelopeStart:  time.Date(2026, 7, 27, 0, 0, 0, 500000000, time.UTC),
			WorkloadEnvelopeEnd:    time.Date(2026, 7, 27, 0, 0, 1, 500000000, time.UTC),
			AccountedDurationMS:    1000,
			AccountedIntervalCount: 1,
			OverheadMS:             1000,
		}
		if _, err := store.CompareAndSwapShardOverhead(1, *overhead, []gate.ShardOrchestrationOverheadSample{sample}); err != nil {
			t.Fatalf("seed shard overhead authority: %v", err)
		}
		planning := gate.PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, TargetDurationMS: gate.FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
		snapshot, err := store.LoadPlanning(planning)
		if err != nil {
			t.Fatalf("load planning duration ledger SQLite authority: %v", err)
		}
		return store, snapshot
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load duration ledger SQLite authority: %v", err)
	}
	if snapshot.Generation != 1 || !reflect.DeepEqual(snapshot.Ledger, ledger) {
		t.Fatalf("duration ledger SQLite snapshot = %#v, want initialized ledger", snapshot)
	}
	return store, snapshot
}

// reloadRemotePlanningSnapshot 让测试输入的 mode 与 SQLite 派生索引保持同一 planning context。
func reloadRemotePlanningSnapshot(t *testing.T, input *RunInput) {
	t.Helper()
	if input == nil || input.LedgerStore == nil {
		t.Fatal("remote CI fixture planning authority is required")
	}
	snapshot, err := input.LedgerStore.LoadPlanning(remotePlanningContext(*input))
	if err != nil {
		t.Fatalf("reload remote CI fixture planning snapshot: %v", err)
	}
	input.LedgerSnapshot = snapshot
}

// seedRemoteRunOverheadForeignKey 建立 overhead sample 所需的最小 provisional ci_runs 行。
func seedRemoteRunOverheadForeignKey(t *testing.T, store *gate.DurationLedgerStore) {
	t.Helper()
	database, err := sql.Open("sqlite", store.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC).UnixMilli()
	digest := "sha256:" + strings.Repeat("e", 64)
	_, err = database.Exec(`INSERT INTO ci_runs (job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text) VALUES (?, 0, 'manual-cli', 'local-fast', ?, ?, '1', 'snap-accepted-baseline', ?, ?, ?, ?, 'passed', 0, ?, ?, 1, '')`, "fixture-shard-overhead", digest, digest, strings.Repeat("f", 40), digest, digest, "runner@"+digest, now, now+1)
	if err != nil {
		t.Fatalf("seed shard overhead ci_runs foreign key: %v", err)
	}
}

func reportFromCreateRequest(request eci.CreateRequest, executionStartedAt time.Time, lookup func(string) (ShardRequest, bool)) (gate.PlanExecutionReport, error) {
	profile, planDigest, manifestDigest, err := validateReportCreateRequest(request)
	if err != nil {
		return gate.PlanExecutionReport{}, err
	}
	shardRequest, err := lookupReportShardRequest(lookup, manifestDigest)
	if err != nil {
		return gate.PlanExecutionReport{}, err
	}
	report := newCoordinatorPlanExecutionReport(request, profile, planDigest)
	emptyDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(nil))
	if err := appendCoordinatorGateReports(&report, shardRequest.GateIDs, executionStartedAt, emptyDigest); err != nil {
		return gate.PlanExecutionReport{}, err
	}
	if err := appendCoordinatorCompileReports(&report, shardRequest.CompileGroups, executionStartedAt, emptyDigest); err != nil {
		return gate.PlanExecutionReport{}, err
	}
	return report, nil
}

func validateReportCreateRequest(request eci.CreateRequest) (gate.Profile, string, string, error) {
	if len(request.Args) != 10 || request.Args[0] != "worker" || request.Args[1] != "run-shard" ||
		request.Args[2] != "--profile" || request.Args[4] != "--plan-digest" ||
		request.Args[6] != "--manifest-path" || request.Args[7] != gate.ExecutorShardExecutionManifestPath ||
		request.Args[8] != "--manifest-digest" {
		return "", "", "", fmt.Errorf("unexpected worker args: %v", request.Args)
	}
	return gate.Profile(request.Args[3]), request.Args[5], request.Args[9], nil
}

func lookupReportShardRequest(lookup func(string) (ShardRequest, bool), manifestDigest string) (ShardRequest, error) {
	if lookup == nil {
		return ShardRequest{}, fmt.Errorf("worker manifest registry is not bound")
	}
	request, ok := lookup(manifestDigest)
	if !ok {
		return ShardRequest{}, fmt.Errorf("worker manifest %q was not uploaded", manifestDigest)
	}
	return request, nil
}

func newCoordinatorPlanExecutionReport(request eci.CreateRequest, profile gate.Profile, planDigest string) gate.PlanExecutionReport {
	return gate.PlanExecutionReport{
		SchemaVersion: gate.ExecutorPlanReportSchemaVersion, Profile: profile, PlanDigest: planDigest, AgentTokenDigest: request.Environment[gate.ExecutorAgentTokenDigestEnvironment], ExecutionOutcome: gate.SuccessfulWorkerExecutionOutcome(),
	}
}

func appendCoordinatorGateReports(report *gate.PlanExecutionReport, gateIDs []gate.GateID, executionStartedAt time.Time, emptyDigest string) error {
	for _, id := range gateIDs {
		goFlags, err := gate.WorkloadExecutionGoFlags(string(id))
		if err != nil {
			return fmt.Errorf("derive coordinator fixture GoFlags for %q: %w", id, err)
		}
		report.Gates = append(report.Gates, gate.PlanGateExecution{
			GateID: id, Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: executionStartedAt, CompletedAt: executionStartedAt.Add(time.Second), LogDigest: emptyDigest,
			ExecutionProfile: gate.ExecutionProfile{GoFlags: goFlags, CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 100, TestBodyMS: 900, TotalMS: 1_000},
		})
	}
	return nil
}

func appendCoordinatorCompileReports(report *gate.PlanExecutionReport, groups []gate.CompileGroup, executionStartedAt time.Time, emptyDigest string) error {
	for _, group := range groups {
		artifactKey, err := gate.CompileArtifactKey(group)
		if err != nil {
			return err
		}
		report.CompileGroupExecutions = append(report.CompileGroupExecutions, gate.CompileGroupExecution{
			Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
			GroupID: group.GroupID, ArtifactKey: artifactKey, PackageTarget: group.PackageTarget,
			WorkloadIDs: slices.Clone(group.WorkloadIDs), StartedAtUnixMS: executionStartedAt.UnixMilli(),
			CompletedAtUnixMS: executionStartedAt.Add(time.Second).UnixMilli(), DurationMS: 1_000,
			ArtifactSHA256: emptyDigest, ArtifactSize: 1, CacheHits: 0, CacheMisses: 1,
			Status: gate.ResultStatusPassed, ExitCode: 0, CompileCommandDigest: emptyDigest,
			ProfileDigest: group.ProfileDigest, ResourceClassID: group.ResourceClassID,
		})
	}
	return nil
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
