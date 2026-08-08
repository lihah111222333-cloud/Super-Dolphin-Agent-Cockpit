package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"golang.org/x/sync/errgroup"
)

func TestRemoteRunnerIdentityDigestIgnoresSourceCacheAndRuntimeImageRefresh(t *testing.T) {
	state := remoteRunRunnerIdentityState()
	digest := remoteRunRunnerIdentity(state)
	refreshed := state
	refreshed.Generation = 42
	refreshed.MainCommit = strings.Repeat("2", 40)
	refreshed.MainTree = strings.Repeat("3", 40)
	refreshed.BaselineManifestDigest = "sha256:" + strings.Repeat("4", 64)
	refreshed.CreatedAt = time.Now().UTC()
	refreshed.AcceptedAt = refreshed.CreatedAt.Add(time.Minute)
	refreshed.GateBinarySHA256 = "sha256:" + strings.Repeat("5", 64)
	refreshed.RuntimeImage = "registry.example/runtime@sha256:" + strings.Repeat("6", 64)
	refreshed.ImageCacheID = "cache-source-refresh"
	refreshed.ImageCacheSnapshotID = "snapshot-source-refresh"
	if got := remoteRunRunnerIdentity(refreshed); got != digest {
		t.Fatalf("source/cache-only refresh changed runner digest: got %q, want %q", got, digest)
	}
}

func TestRemoteRunnerIdentityDigestChangesWithRunnerInputs(t *testing.T) {
	state := remoteRunRunnerIdentityState()
	wantDifferent := map[string]func(*remoteci.BaselineState){
		"platform": func(value *remoteci.BaselineState) {
			value.Platform = "linux/arm64"
		},
		"policy": func(value *remoteci.BaselineState) {
			value.PolicyDigest = "sha256:" + strings.Repeat("3", 64)
		},
		"toolchain": func(value *remoteci.BaselineState) {
			value.ToolchainDigest = "sha256:" + strings.Repeat("4", 64)
		},
		"runtime seed": func(value *remoteci.BaselineState) {
			value.RuntimeSeedSHA256 = "sha256:" + strings.Repeat("5", 64)
		},
	}
	workerDigest := "sha256:" + strings.Repeat("f", 64)
	original := remoteRunnerIdentityDigest(state, workerDigest)
	for name, mutate := range wantDifferent {
		t.Run(name, func(t *testing.T) {
			changed := state
			mutate(&changed)
			if got := remoteRunnerIdentityDigest(changed, workerDigest); got == original {
				t.Fatalf("runner input %q did not change digest", name)
			}
		})
	}
	if got := remoteRunnerIdentityDigest(state, "sha256:"+strings.Repeat("e", 64)); got == original {
		t.Fatal("worker execution input did not change runner digest")
	}
}

func TestRemoteDurationCalibrationMatchesStableRunnerAcrossSourceRefresh(t *testing.T) {
	state := remoteRunRunnerIdentityState()
	runnerIdentity := remoteRunRunnerIdentity(state)
	calibration := remoteAutomationCalibration(state, runnerIdentity)
	refreshed := state
	refreshed.MainCommit = strings.Repeat("7", 40)
	refreshed.MainTree = strings.Repeat("8", 40)
	refreshed.BaselineManifestDigest = "sha256:" + strings.Repeat("9", 64)
	if !remoteDurationCalibrationMatches(&calibration, refreshed, runnerIdentity) {
		t.Fatal("source-only refresh invalidated stable runner calibration")
	}
	refreshed.Platform = "linux/arm64"
	if remoteDurationCalibrationMatches(&calibration, refreshed, runnerIdentity) {
		t.Fatal("platform drift reused incompatible calibration")
	}
}

// TestRemoteDurationCalibrationReadyRequiresAcceptedShardOverhead 验证 metadata-only 校准不能宣称 ready。
func TestRemoteDurationCalibrationReadyRequiresAcceptedShardOverhead(t *testing.T) {
	state := remoteRunRunnerIdentityState()
	runnerIdentity := remoteRunRunnerIdentity(state)
	fixture := newRemoteDurationCalibrationFixture(t)
	fixture.calibration.Runner = runnerIdentity
	fixture.calibration.WorkloadCount = len(fixture.expected)
	fixture.calibration.RacePackageCount = len(fixture.inventory.GoPackages)
	snapshot, err := fixture.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	ledger := snapshot.Ledger
	ledger.Calibration = &fixture.calibration
	if _, err := fixture.store.CompareAndSwap(snapshot.Generation, ledger); err != nil {
		t.Fatal(err)
	}

	ledgerPath := fixture.store.AuthorityPath()
	assertRemoteDurationCalibrationMetadataOnlyNotReady(t, ledgerPath, state, runnerIdentity, fixture.store)

	seedRemoteDurationCalibrationFixtureOverhead(t, fixture)
	assertRemoteDurationCalibrationMetadataOnlyNotReady(t, ledgerPath, state, runnerIdentity, fixture.store)

	recordRemoteDurationCalibrationFixtureCatalogs(t, fixture)
	appendCompleteRemoteCalibrationSamples(t, fixture)
	assertRemoteDurationCalibrationCompleteReady(t, ledgerPath, state, runnerIdentity)
}

// recordRemoteDurationCalibrationFixtureCatalogs 持久化校准 metadata 引用的三个精确目录及观测。
func recordRemoteDurationCalibrationFixtureCatalogs(t *testing.T, fixture remoteDurationCalibrationFixture) {
	t.Helper()
	entries := []struct {
		catalog    gatecontract.WorkloadCatalog
		entrypoint gatecontract.CIEntrypointID
		profile    gatecontract.Profile
	}{
		{catalog: fixture.commitCatalog, entrypoint: gatecontract.CIEntrypointGitPreCommit, profile: gatecontract.ProfileLocalFast},
		{catalog: fixture.pushCatalog, entrypoint: gatecontract.CIEntrypointGitPrePush, profile: gatecontract.ProfilePush},
		{catalog: fixture.releaseCatalog, entrypoint: gatecontract.CIEntrypointRelease, profile: gatecontract.ProfileRelease},
	}
	observedAt := time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)
	for _, entry := range entries {
		if err := fixture.store.RecordWorkloadCatalog(entry.catalog, gatecontract.WorkloadCatalogObservation{
			SourceTreeSHA: fixture.calibration.Tree, Entrypoint: entry.entrypoint, Profile: entry.profile,
			AcceptedGeneration: fixture.acceptedGeneration, ObservedAt: observedAt,
		}); err != nil {
			t.Fatalf("record calibration catalog: %v", err)
		}
	}
}

// assertRemoteDurationCalibrationMetadataOnlyNotReady 验证仅有 calibration metadata 时保持不可用且不清理记录。
func assertRemoteDurationCalibrationMetadataOnlyNotReady(t *testing.T, ledgerPath string, state remoteci.BaselineState, runnerIdentity string, store *gatecontract.DurationLedgerStore) {
	t.Helper()
	ready, err := remoteDurationCalibrationReady(ledgerPath, state, runnerIdentity)
	if err != nil {
		t.Fatalf("metadata-only readiness check: %v", err)
	}
	if ready {
		t.Fatal("metadata-only calibration reported ready")
	}
	prepared, err := prepareAutomaticRemoteCalibrationLedger(ledgerPath, state, runnerIdentity)
	if err != nil {
		t.Fatalf("metadata-only preparation: %v", err)
	}
	if prepared {
		t.Fatal("metadata-only calibration prepared as ready")
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Ledger.Calibration == nil {
		t.Fatal("metadata-only calibration was cleared")
	}
}

// assertRemoteDurationCalibrationCompleteReady 验证完整 accepted overhead 允许 ready。
func assertRemoteDurationCalibrationCompleteReady(t *testing.T, ledgerPath string, state remoteci.BaselineState, runnerIdentity string) {
	t.Helper()
	ready, err := remoteDurationCalibrationReady(ledgerPath, state, runnerIdentity)
	if err != nil {
		t.Fatalf("complete readiness check: %v", err)
	}
	if !ready {
		t.Fatal("complete accepted shard overhead was not ready")
	}
	prepared, err := prepareAutomaticRemoteCalibrationLedger(ledgerPath, state, runnerIdentity)
	if err != nil {
		t.Fatalf("complete preparation: %v", err)
	}
	if !prepared {
		t.Fatal("complete accepted shard overhead was not prepared as ready")
	}
}

func TestEnsureRemoteDurationCalibrationConcurrentAgentsDoNotUseFileAdmission(t *testing.T) {
	options, state, runnerIdentity := automaticCalibrationConcurrencyFixture(t)
	started, release, calls, run := concurrentAutomaticCalibrationRun(t, options, state)
	group := startAutomaticCalibrationCalls(options, state, runnerIdentity, run)
	assertAutomaticCalibrationRunsOverlap(t, started, release, calls, group)
}

// TestEnsureRemoteDurationCalibrationRunsForSelectedTests 验证 test 场景 miss 不再跳过自动校准。
func TestEnsureRemoteDurationCalibrationRunsForSelectedTests(t *testing.T) {
	options, _, _ := automaticCalibrationConcurrencyFixture(t)
	options.Scenario = "test"
	options.Tests = []string{"internal/devtools/gate"}
	state := remoteRunRunnerIdentityState()
	state.MainCommit = strings.Repeat("b", 40)
	runnerIdentity := remoteRunRunnerIdentity(state)
	var called bool
	if err := ensureRemoteDurationCalibrationWithRun(options, state, runnerIdentity, func(got remoteRunOptions) error {
		called = true
		if got.Scenario != "" || len(got.Tests) != 0 {
			return fmt.Errorf("automatic calibration retained selected-test options: scenario=%q tests=%v", got.Scenario, got.Tests)
		}
		if got.Commit != state.MainCommit {
			return fmt.Errorf("automatic calibration commit = %q, want %q", got.Commit, state.MainCommit)
		}
		return nil
	}); err != nil {
		t.Fatalf("ensureRemoteDurationCalibrationWithRun() error = %v", err)
	}
	if !called {
		t.Fatal("selected-tests miss skipped automatic calibration")
	}
}

func automaticCalibrationConcurrencyFixture(t *testing.T) (remoteRunOptions, remoteci.BaselineState, string) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(root, "duration-ledger.sqlite")
	state := remoteRunRunnerIdentityState()
	state.MainCommit = strings.Repeat("a", 40)
	runnerIdentity := remoteRunRunnerIdentity(state)
	return remoteRunOptions{Scenario: "commit", LedgerPath: ledgerPath}, state, runnerIdentity
}

func concurrentAutomaticCalibrationRun(t *testing.T, options remoteRunOptions, state remoteci.BaselineState) (<-chan struct{}, chan<- struct{}, *atomic.Int32, func(remoteRunOptions) error) {
	t.Helper()
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	run := func(got remoteRunOptions) error {
		if calls.Add(1) == 2 {
			close(started)
		}
		if got.Commit != state.MainCommit || got.LedgerPath != options.LedgerPath {
			return errors.New("automatic calibration options drifted")
		}
		<-release
		return nil
	}
	return started, release, &calls, run
}

func startAutomaticCalibrationCalls(options remoteRunOptions, state remoteci.BaselineState, runnerIdentity string, run func(remoteRunOptions) error) *errgroup.Group {
	start := make(chan struct{})
	var group errgroup.Group
	for range 2 {
		group.Go(func() error {
			<-start
			return ensureRemoteDurationCalibrationWithRun(options, state, runnerIdentity, run)
		})
	}
	close(start)
	return &group
}

func assertAutomaticCalibrationRunsOverlap(t *testing.T, started <-chan struct{}, release chan<- struct{}, calls *atomic.Int32, group *errgroup.Group) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("automatic calibration runs were serialized")
	}
	close(release)
	if err := group.Wait(); err != nil {
		t.Fatalf("ensureRemoteDurationCalibrationWithRun() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("automatic calibration calls = %d, want 2", got)
	}
}

func TestEnsureRemoteDurationCalibrationUsesExplicitCandidateWithoutMovingRepositoryState(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	parent := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^{commit}")
	if err := os.WriteFile(filepath.Join(repository, "fixture.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runRemoteRunGit(t, repository, "add", "fixture.txt")
	tree := remoteRunGitOutput(t, repository, "write-tree")
	status := remoteRunGitOutput(t, repository, "status", "--porcelain=v1")
	ledgerRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(ledgerRoot, "duration-ledger.sqlite")
	state := remoteRunRunnerIdentityState()
	state.MainCommit = remoteRunGitOutput(t, repository, "rev-parse", "HEAD^1^{commit}")
	runnerIdentity := remoteRunRunnerIdentity(state)
	options := remoteRunOptions{
		Scenario: "commit", RepositoryRoot: repository, Tree: tree,
		ParentCommit: parent, LedgerPath: ledgerPath,
		AgentTokenDigest: "sha256:" + strings.Repeat("a", sha256.Size*2),
	}
	var calibrationCommit string
	run := func(got remoteRunOptions) error {
		calibrationCommit = got.Commit
		if got.AgentTokenDigest != options.AgentTokenDigest {
			t.Fatalf("automatic calibration agent token digest = %q, want %q", got.AgentTokenDigest, options.AgentTokenDigest)
		}
		return runExplicitCandidateCalibration(repository, tree, parent, ledgerPath, state, got)
	}
	if err := ensureRemoteDurationCalibrationWithRun(options, state, runnerIdentity, run); err != nil {
		t.Fatalf("ensureRemoteDurationCalibrationWithRun() error = %v", err)
	}
	if calibrationCommit == "" {
		t.Fatal("automatic calibration did not run")
	}
	if got := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^{commit}"); got != parent {
		t.Fatalf("HEAD moved to %q, want %q", got, parent)
	}
	if got := remoteRunGitOutput(t, repository, "write-tree"); got != tree {
		t.Fatalf("index tree changed to %q, want %q", got, tree)
	}
	if got := remoteRunGitOutput(t, repository, "status", "--porcelain=v1"); got != status {
		t.Fatalf("worktree status changed to %q, want %q", got, status)
	}
}

// runExplicitCandidateCalibration 校验自动校准绑定的候选提交并写入成功校准。
func runExplicitCandidateCalibration(
	repository string,
	tree string,
	parent string,
	ledgerPath string,
	state remoteci.BaselineState,
	options remoteRunOptions,
) error {
	if options.Tree != "" || options.ParentCommit != "" || options.Commit == state.MainCommit {
		return errors.New("automatic calibration did not bind the explicit candidate commit")
	}
	identity, err := resolveRemoteCalibrationIdentity(repository, options.Commit)
	if err != nil {
		return err
	}
	if identity.tree != tree || identity.base != parent {
		return errors.New("automatic calibration candidate identity drifted")
	}
	again, err := createRemoteCalibrationCandidateCommit(repository, tree, parent)
	if err != nil {
		return err
	}
	if again != options.Commit {
		return errors.New("automatic calibration candidate commit is not deterministic")
	}
	return storeRemoteAutomationCalibration(ledgerPath, state)
}

// storeRemoteAutomationCalibration 原子写入测试用的可比 runner 校准身份。
func storeRemoteAutomationCalibration(ledgerPath string, state remoteci.BaselineState) error {
	store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
	if err != nil {
		return err
	}
	ledger := gatecontract.NewDurationLedger()
	calibration := remoteAutomationCalibration(state, remoteRunRunnerIdentity(state))
	ledger.Calibration = &calibration
	_, err = store.CompareAndSwap(0, ledger)
	return err
}

func TestPrepareAutomaticRemoteCalibrationLedgerPreservesSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.sqlite")
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	state := remoteRunRunnerIdentityState()
	runnerIdentity := remoteRunRunnerIdentity(state)
	staleRunner := "sha256:" + strings.Repeat("f", 64)
	sample := gatecontract.DurationSample{
		Bucket: gatecontract.DurationBucket{
			WorkloadID: "guard:fixture", CommandDigest: strings.Repeat("1", 64),
			InputDigest: "sha256:" + strings.Repeat("0", 64), Platform: state.Platform, Runner: staleRunner, Toolchain: state.ToolchainDigest,
			ExecutionMode: gatecontract.DurationExecutionModeCalibration, ResourceClassID: "calibration", ResourceCPU: 4, ResourceMemoryGiB: 8,
		},
		Succeeded: true, DurationMS: 1234,
	}
	ledger := gatecontract.NewDurationLedger()
	calibration := remoteAutomationCalibration(state, staleRunner)
	ledger.Calibration = &calibration
	ledger.Samples = []gatecontract.DurationSample{sample}
	if _, err := store.CompareAndSwap(0, ledger); err != nil {
		t.Fatal(err)
	}
	ready, err := prepareAutomaticRemoteCalibrationLedger(path, state, runnerIdentity)
	if err != nil {
		t.Fatalf("prepareAutomaticRemoteCalibrationLedger() error = %v", err)
	}
	if ready {
		t.Fatal("stale calibration reported ready")
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Ledger.Calibration != nil ||
		len(snapshot.Ledger.Samples) != 1 || snapshot.Ledger.Samples[0] != sample {
		t.Fatalf("mismatched calibration was not cleared without rewriting samples: %#v", snapshot.Ledger)
	}
}

func TestPrepareAutomaticRemoteCalibrationLedgerDoesNotReuseRetiredV1Identity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.sqlite")
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	state := remoteRunRunnerIdentityState()
	runnerIdentity := remoteRunRunnerIdentity(state)
	legacyIdentity := "sha256:" + strings.Repeat("e", 64)
	sample := gatecontract.DurationSample{
		Bucket: gatecontract.DurationBucket{
			WorkloadID: "guard:fixture", CommandDigest: strings.Repeat("1", 64),
			InputDigest: "sha256:" + strings.Repeat("0", 64), Platform: state.Platform, Runner: legacyIdentity, Toolchain: state.ToolchainDigest,
			ExecutionMode: gatecontract.DurationExecutionModeCalibration, ResourceClassID: "calibration", ResourceCPU: 4, ResourceMemoryGiB: 8,
		},
		Succeeded: true, DurationMS: 1234,
	}
	ledger := gatecontract.NewDurationLedger()
	calibration := remoteAutomationCalibration(state, legacyIdentity)
	ledger.Calibration = &calibration
	ledger.Samples = []gatecontract.DurationSample{sample}
	if _, err := store.CompareAndSwap(0, ledger); err != nil {
		t.Fatal(err)
	}
	ready, err := prepareAutomaticRemoteCalibrationLedger(path, state, runnerIdentity)
	if err != nil {
		t.Fatalf("prepareAutomaticRemoteCalibrationLedger() error = %v", err)
	}
	if ready {
		t.Fatal("same-baseline legacy calibration was reported ready without catalogs and complete samples")
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Ledger.Calibration != nil ||
		len(snapshot.Ledger.Samples) != 1 ||
		snapshot.Ledger.Samples[0].Bucket.Runner != legacyIdentity {
		t.Fatalf("retired v1 identity was reused or rewritten: %#v", snapshot.Ledger)
	}
}

func TestPrepareAutomaticRemoteCalibrationLedgerDoesNotReuseV2RuntimeImageIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.sqlite")
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	state := remoteRunRunnerIdentityState()
	v2Identity := historicalV2RemoteRunnerIdentity(state, "sha256:"+strings.Repeat("f", 64))
	ledger := gatecontract.NewDurationLedger()
	ledger.Calibration = new(remoteAutomationCalibration(state, v2Identity))
	ledger.Samples = []gatecontract.DurationSample{{Bucket: gatecontract.DurationBucket{WorkloadID: "guard:fixture", CommandDigest: strings.Repeat("1", 64), InputDigest: "sha256:" + strings.Repeat("0", 64), Platform: state.Platform, Runner: v2Identity, Toolchain: state.ToolchainDigest, ExecutionMode: gatecontract.DurationExecutionModeCalibration, ResourceClassID: "calibration", ResourceCPU: 4, ResourceMemoryGiB: 8}, Succeeded: true, DurationMS: 1234}}
	if _, err := store.CompareAndSwap(0, ledger); err != nil {
		t.Fatal(err)
	}
	ready, err := prepareAutomaticRemoteCalibrationLedger(path, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("v2 runtime-image calibration was reused")
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Ledger.Calibration != nil || snapshot.Ledger.Samples[0].Bucket.Runner != v2Identity {
		t.Fatalf("v2 identity migration mutated ledger: %#v", snapshot.Ledger)
	}
}

func historicalV2RemoteRunnerIdentity(state remoteci.BaselineState, workerExecutionDigest string) string {
	material := strings.Join([]string{"super-dolphin-gate-runner-identity-v2", state.RuntimeImage, state.PolicyDigest, state.ToolchainDigest, workerExecutionDigest}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return fmt.Sprintf("sha256:%x", digest)
}

func remoteAutomationCalibration(state remoteci.BaselineState, runner string) gatecontract.DurationCalibration {
	return gatecontract.DurationCalibration{
		SchemaVersion:              gatecontract.DurationCalibrationSchemaVersion,
		Commit:                     strings.Repeat("1", 40),
		Tree:                       strings.Repeat("2", 40),
		Platform:                   state.Platform,
		Runner:                     runner,
		Toolchain:                  state.ToolchainDigest,
		CommitEntrypoint:           gatecontract.CIEntrypointGitPreCommit,
		PushEntrypoint:             gatecontract.CIEntrypointGitPrePush,
		ReleaseEntrypoint:          gatecontract.CIEntrypointRelease,
		CommitCatalogDigest:        "sha256:" + strings.Repeat("3", 64),
		PushCatalogDigest:          "sha256:" + strings.Repeat("4", 64),
		ReleaseCatalogDigest:       "sha256:" + strings.Repeat("5", 64),
		CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8,
		WorkloadCount:      1,
		RacePackageCount:   1,
		AcceptedSnapshotID: "snapshot-test",
		CompletedAt:        time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC),
	}
}
