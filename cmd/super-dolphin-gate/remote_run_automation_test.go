package main

import (
	"errors"
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

func TestRemoteRunnerIdentityDigestIgnoresSourceOnlyBaselineRefresh(t *testing.T) {
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
	refreshed.RuntimeSeedSHA256 = "sha256:" + strings.Repeat("6", 64)
	if got := remoteRunRunnerIdentity(refreshed); got != digest {
		t.Fatalf("source-only refresh changed runner digest: got %q, want %q", got, digest)
	}
}

func TestRemoteRunnerIdentityDigestChangesWithRunnerInputs(t *testing.T) {
	state := remoteRunRunnerIdentityState()
	wantDifferent := map[string]func(*remoteci.BaselineState){
		"runtime image": func(value *remoteci.BaselineState) {
			value.RuntimeImage = "registry.example/runtime@sha256:" + strings.Repeat("2", 64)
		},
		"policy": func(value *remoteci.BaselineState) {
			value.PolicyDigest = "sha256:" + strings.Repeat("3", 64)
		},
		"toolchain": func(value *remoteci.BaselineState) {
			value.ToolchainDigest = "sha256:" + strings.Repeat("4", 64)
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

func TestEnsureRemoteDurationCalibrationSingleFlightsConcurrentAgents(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(root, "duration-ledger.json")
	state := remoteRunRunnerIdentityState()
	state.MainCommit = strings.Repeat("a", 40)
	runnerIdentity := remoteRunRunnerIdentity(state)
	options := remoteRunOptions{Scenario: "commit", LedgerPath: ledgerPath}
	var calls atomic.Int32
	run := func(got remoteRunOptions) error {
		calls.Add(1)
		if got.Commit != state.MainCommit || got.LedgerPath != ledgerPath {
			return errors.New("automatic calibration options drifted")
		}
		store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
		if err != nil {
			return err
		}
		ledger := gatecontract.NewDurationLedger()
		calibration := remoteAutomationCalibration(state, runnerIdentity)
		ledger.Calibration = &calibration
		_, err = store.CompareAndSwap(0, ledger)
		return err
	}
	start := make(chan struct{})
	var group errgroup.Group
	for range 2 {
		group.Go(func() error {
			<-start
			return ensureRemoteDurationCalibrationWithRun(options, state, runnerIdentity, run)
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("ensureRemoteDurationCalibrationWithRun() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("automatic calibration calls = %d, want 1", got)
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
	ledgerPath := filepath.Join(ledgerRoot, "duration-ledger.json")
	state := remoteRunRunnerIdentityState()
	state.MainCommit = remoteRunGitOutput(t, repository, "rev-parse", "HEAD^1^{commit}")
	runnerIdentity := remoteRunRunnerIdentity(state)
	options := remoteRunOptions{
		Scenario: "commit", RepositoryRoot: repository, Tree: tree,
		ParentCommit: parent, LedgerPath: ledgerPath,
	}
	var calibrationCommit string
	run := func(got remoteRunOptions) error {
		calibrationCommit = got.Commit
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
	path := filepath.Join(t.TempDir(), "duration-ledger.json")
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
			Platform: state.Platform, Runner: staleRunner, Toolchain: state.ToolchainDigest,
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
	if snapshot.Ledger.Calibration != nil || len(snapshot.Ledger.Samples) != 1 ||
		snapshot.Ledger.Samples[0] != sample {
		t.Fatalf("reset ledger = %#v", snapshot.Ledger)
	}
}

func TestPrepareAutomaticRemoteCalibrationLedgerMigratesSameBaselineIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json")
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	state := remoteRunRunnerIdentityState()
	runnerIdentity := remoteRunRunnerIdentity(state)
	legacyIdentity := legacyRemoteRunnerIdentityDigest(state)
	sample := gatecontract.DurationSample{
		Bucket: gatecontract.DurationBucket{
			WorkloadID: "guard:fixture", CommandDigest: strings.Repeat("1", 64),
			Platform: state.Platform, Runner: legacyIdentity, Toolchain: state.ToolchainDigest,
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
	if !ready {
		t.Fatal("same-baseline legacy calibration was not migrated as ready")
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Ledger.Calibration == nil ||
		snapshot.Ledger.Calibration.Runner != runnerIdentity ||
		len(snapshot.Ledger.Samples) != 1 ||
		snapshot.Ledger.Samples[0].Bucket.Runner != runnerIdentity {
		t.Fatalf("migrated ledger = %#v", snapshot.Ledger)
	}
}

func remoteAutomationCalibration(state remoteci.BaselineState, runner string) gatecontract.DurationCalibration {
	return gatecontract.DurationCalibration{
		SchemaVersion:        gatecontract.DurationCalibrationSchemaVersion,
		Commit:               strings.Repeat("1", 40),
		Tree:                 strings.Repeat("2", 40),
		Platform:             state.Platform,
		Runner:               runner,
		Toolchain:            state.ToolchainDigest,
		CommitEntrypoint:     gatecontract.CIEntrypointGitPreCommit,
		PushEntrypoint:       gatecontract.CIEntrypointGitPrePush,
		ReleaseEntrypoint:    gatecontract.CIEntrypointRelease,
		CommitCatalogDigest:  "sha256:" + strings.Repeat("3", 64),
		PushCatalogDigest:    "sha256:" + strings.Repeat("4", 64),
		ReleaseCatalogDigest: "sha256:" + strings.Repeat("5", 64),
		WorkloadCount:        1,
		RacePackageCount:     1,
		CompletedAt:          time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC),
	}
}
