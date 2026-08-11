package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remoteDurationCalibrationFixture struct {
	store                                      *gatecontract.DurationLedgerStore
	acceptedGeneration                         uint64
	calibration                                gatecontract.DurationCalibration
	commitCatalog, pushCatalog, releaseCatalog gatecontract.WorkloadCatalog
	expected                                   map[string]gatecontract.Workload
	inventory                                  gatecontract.WorkloadInventory
}

func newRemoteDurationCalibrationFixture(t *testing.T) remoteDurationCalibrationFixture {
	t.Helper()
	store, err := prepareRemoteCalibrationLedger(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	seedRemoteRunTestAcceptedGeneration(t, store, 1)
	base, commit, tree := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	inventory := gatecontract.WorkloadInventory{GoPackages: []string{"./internal/alpha", "./pkg/beta"}}
	commitCatalog, commitDigest := mustRemoteCalibrationCatalog(t, remoteci.RunInput{Profile: gatecontract.ProfileLocalFast, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindTree, ObjectFormat: gatecontract.GitObjectFormatSHA1, Tree: &gatecontract.TreeSource{SHA: tree, ParentCommitSHA: base}, SourceTreeSHA: tree}, Inventory: inventory})
	pushCatalog, pushDigest := mustRemoteCalibrationCatalog(t, remoteci.RunInput{Profile: gatecontract.ProfilePush, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindRange, ObjectFormat: gatecontract.GitObjectFormatSHA1, Range: &gatecontract.RangeSource{BaseKind: gatecontract.BaseKindCommit, BaseSHA: base, HeadSHA: commit, LocalRef: "refs/heads/main", RemoteRef: "refs/heads/main", ObservedRemoteSHA: base, UpdateKind: gatecontract.UpdateKindFastForward}, SourceTreeSHA: tree}, Inventory: inventory})
	releaseCatalog, releaseDigest := mustRemoteCalibrationCatalog(t, remoteci.RunInput{Profile: gatecontract.ProfileRelease, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1, Commit: &gatecontract.CommitSource{SHA: commit}, SourceTreeSHA: tree}, Inventory: inventory})
	calibration := gatecontract.DurationCalibration{SchemaVersion: gatecontract.DurationCalibrationSchemaVersion, Commit: commit, Tree: tree, Platform: "linux/amd64", Runner: "sha256:" + strings.Repeat("4", 64), Toolchain: remoteRunRunnerIdentityState().ToolchainDigest, CommitEntrypoint: gatecontract.CIEntrypointGitPreCommit, PushEntrypoint: gatecontract.CIEntrypointGitPrePush, ReleaseEntrypoint: gatecontract.CIEntrypointRelease, CommitCatalogDigest: commitDigest, PushCatalogDigest: pushDigest, ReleaseCatalogDigest: releaseDigest, CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8, AcceptedSnapshotID: "snapshot-test", CompletedAt: time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)}
	expected := remoteCalibrationExpectedWorkloads(commitCatalog, pushCatalog, releaseCatalog)
	return remoteDurationCalibrationFixture{store: store, acceptedGeneration: 1, calibration: calibration, commitCatalog: commitCatalog, pushCatalog: pushCatalog, releaseCatalog: releaseCatalog, expected: expected, inventory: inventory}
}

func seedRemoteDurationCalibrationFixtureOverhead(t *testing.T, fixture remoteDurationCalibrationFixture) {
	t.Helper()
	seedRemoteRunShardOverheadFixture(t, fixture.store, fixture.calibration.Runner, fixture.calibration.AcceptedSnapshotID)
}

// remoteCalibrationCatalog 仅为测试 fixture 构造待持久化的校准 catalog。
func remoteCalibrationCatalog(input remoteci.RunInput) (gatecontract.WorkloadCatalog, string, error) {
	plan, err := gatecontract.BuildGatePlan(input.Profile, input.Source)
	if err != nil {
		return gatecontract.WorkloadCatalog{}, "", err
	}
	catalog, err := gatecontract.BuildCalibrationWorkloadCatalog(plan, gatecontract.DefaultWorkloadBootstrapPolicy(), input.Inventory)
	if err != nil {
		return gatecontract.WorkloadCatalog{}, "", err
	}
	digest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		return gatecontract.WorkloadCatalog{}, "", err
	}
	return catalog, digest, nil
}

func mustRemoteCalibrationCatalog(t *testing.T, input remoteci.RunInput) (gatecontract.WorkloadCatalog, string) {
	t.Helper()
	catalog, _, err := remoteCalibrationCatalog(input)
	if err != nil {
		t.Fatal(err)
	}
	for index := range catalog.Workloads {
		// 直接目录构造器把生产输入绑定留给协调器；测试 fixture 仍须模拟校准证据
		// 实际读取的持久化精确目录，因此每个 bucket 都绑定有效摘要。
		seed := sha256.Sum256([]byte("remote-calibration-fixture-input\x00" + catalog.Workloads[index].ID))
		catalog.Workloads[index].InputDigest = "sha256:" + hex.EncodeToString(seed[:])
	}
	digest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, digest
}

func remoteCalibrationExpectedWorkloads(catalogs ...gatecontract.WorkloadCatalog) map[string]gatecontract.Workload {
	expected := make(map[string]gatecontract.Workload)
	for _, catalog := range catalogs {
		for _, workload := range catalog.Workloads {
			expected[remoteCalibrationWorkloadKey(workload)] = workload
		}
	}
	return expected
}

func (fixture remoteDurationCalibrationFixture) samplesExceptRequiredWorkloads(t *testing.T) ([]gatecontract.DurationSample, gatecontract.DurationSample, gatecontract.DurationSample) {
	t.Helper()
	var samples []gatecontract.DurationSample
	var missingWorkload, missingRace gatecontract.DurationSample
	for _, workload := range fixture.expected {
		sample := gatecontract.DurationSample{Bucket: gatecontract.DurationBucket{WorkloadID: workload.ID, CommandDigest: workload.CommandDigest, InputDigest: workload.InputDigest, Platform: fixture.calibration.Platform, Runner: fixture.calibration.Runner, Toolchain: fixture.calibration.Toolchain, ExecutionMode: gatecontract.DurationExecutionModeCalibration, ResourceClassID: fixture.calibration.CalibrationResourceClassID, ResourceCPU: fixture.calibration.CalibrationResourceCPU, ResourceMemoryGiB: fixture.calibration.CalibrationResourceMemoryGiB}, Succeeded: true, DurationMS: 1_000}
		parent, err := gatecontract.WorkloadParentGateID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if missingRace.Bucket.WorkloadID == "" && remoteCalibrationRaceSample(workload, parent) {
			missingRace = sample
			continue
		}
		if missingWorkload.Bucket.WorkloadID == "" && remoteCalibrationNonRaceSample(workload, parent) {
			missingWorkload = sample
			continue
		}
		samples = append(samples, sample)
	}
	if missingWorkload.Bucket.WorkloadID == "" {
		t.Fatal("calibration catalogs contain no non-race gate workload")
	}
	if missingRace.Bucket.WorkloadID == "" {
		t.Fatal("calibration catalogs contain no per-package race workload")
	}
	return samples, missingWorkload, missingRace
}

func remoteCalibrationRaceSample(workload gatecontract.Workload, parent gatecontract.GateID) bool {
	return workload.Shardable && parent == gatecontract.GateIDBackendTestGuardWithRace && workload.Kind == gatecontract.WorkloadKindGoTest
}

func remoteCalibrationNonRaceSample(workload gatecontract.Workload, parent gatecontract.GateID) bool {
	return workload.Shardable && parent != gatecontract.GateIDBackendTestGuardWithRace
}

func (fixture remoteDurationCalibrationFixture) accept() (gatecontract.DurationLedgerSnapshot, error) {
	return acceptRemoteDurationCalibration(fixture.store, fixture.calibration, fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog)
}

func (fixture remoteDurationCalibrationFixture) acceptExistingSamples() (bool, error) {
	return acceptRemoteDurationCalibrationFromExistingSamples(fixture.store, fixture.calibration, fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog)
}

func assertParsedRemoteRunOptions(t *testing.T, options remoteRunOptions, agentTokenDigest string) {
	t.Helper()
	if options.ConfigPath != "/tmp/remote-ci.json" {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if options.RepositoryRoot != "/tmp/repository" {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if options.Commit != "main" {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if options.Base != "main^" {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if options.LedgerPath != "/tmp/remote-ci.baseline-state.sqlite" {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if options.AgentTokenDigest != agentTokenDigest {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
}

func assertRemoteRunInputIdentity(t *testing.T, input remoteci.RunInput, state remoteci.BaselineState) {
	t.Helper()
	assertRemoteRunInputGitIdentity(t, input)
	assertRemoteRunInputAuthority(t, input, state)
}

func assertRemoteRunInputGitIdentity(t *testing.T, input remoteci.RunInput) {
	t.Helper()
	if len(input.Commit) != 40 {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if len(input.Tree) != 40 {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if len(input.Base) != 40 {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.Source.ObjectFormat != gatecontract.GitObjectFormatSHA1 {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.Source.Kind != gatecontract.SourceKindCommit {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.Source.Commit == nil {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.Source.Commit.SHA != input.Commit {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
}

func assertRemoteRunInputAuthority(t *testing.T, input remoteci.RunInput, state remoteci.BaselineState) {
	t.Helper()
	if input.LedgerSnapshot.Generation == 0 || input.LedgerSnapshot.Ledger.ShardOverhead != nil {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.BaselineManifestDigest != state.BaselineManifestDigest {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.RunnerIdentityDigest == input.BaselineManifestDigest {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.CandidateGateSourceSHA256 != "" || input.CandidateGateToolchainSHA256 != "" ||
		input.ExecutionRunnerImage != "" || input.ExecutionImageCacheSnapshotID != "" || input.ImageCacheOnly {
		t.Fatalf("resolveRemoteRunInput() eagerly bound MISS-only identity = %#v", input)
	}
}

func assertRemoteRunBaselineProjection(t *testing.T, input remoteci.RunInput, state remoteci.BaselineState) {
	t.Helper()
	if input.AcceptedGeneration != state.Generation {
		t.Fatalf("run input accepted generation = %d, want accepted baseline generation %d", input.AcceptedGeneration, state.Generation)
	}
	if !reflect.DeepEqual(input.OCIProjectCache, state.OCIProjectCache) {
		t.Fatalf("OCI project cache projection = %#v, want %#v", input.OCIProjectCache, state.OCIProjectCache)
	}
}

func assertRemoteRunPushSource(t *testing.T, input remoteci.RunInput, base string) {
	t.Helper()
	if input.RemoteName != "origin" {
		t.Fatalf("push source = %#v", input.Source)
	}
	if input.RemoteURL != "ssh://git@example.invalid/repository.git" {
		t.Fatalf("push source = %#v", input.Source)
	}
	if input.Source.Kind != gatecontract.SourceKindRange {
		t.Fatalf("push source = %#v", input.Source)
	}
	if input.Source.Range == nil {
		t.Fatalf("push source = %#v", input.Source)
	}
	if input.Source.Range.BaseSHA != base {
		t.Fatalf("push source = %#v", input.Source)
	}
	if input.Source.Range.HeadSHA != input.Commit {
		t.Fatalf("push source = %#v", input.Source)
	}
	if input.Source.Range.ObservedRemoteSHA != base {
		t.Fatalf("push source = %#v", input.Source)
	}
	if input.Source.Range.UpdateKind != gatecontract.UpdateKindFastForward {
		t.Fatalf("push source = %#v", input.Source)
	}
}

func assertAcceptedRemoteCalibration(t *testing.T, snapshot gatecontract.DurationLedgerSnapshot, fixture remoteDurationCalibrationFixture) {
	t.Helper()
	if snapshot.Ledger.Calibration == nil {
		t.Fatalf("accepted calibration = %#v", snapshot.Ledger.Calibration)
	}
	if snapshot.Ledger.Calibration.WorkloadCount != len(fixture.expected) {
		t.Fatalf("accepted calibration = %#v", snapshot.Ledger.Calibration)
	}
	if snapshot.Ledger.Calibration.RacePackageCount != len(fixture.inventory.GoPackages) {
		t.Fatalf("accepted calibration = %#v", snapshot.Ledger.Calibration)
	}
}

func assertRemoteCalibrationCommitOptions(t *testing.T, options remoteRunOptions, tree, base string) {
	t.Helper()
	if options.Commit != "" {
		t.Fatalf("commit options = %#v", options)
	}
	if options.Tree != tree {
		t.Fatalf("commit options = %#v", options)
	}
	if options.ParentCommit != base {
		t.Fatalf("commit options = %#v", options)
	}
	if options.Scenario != "commit" {
		t.Fatalf("commit options = %#v", options)
	}
	if options.Entrypoint != string(gatecontract.CIEntrypointGitPreCommit) {
		t.Fatalf("commit options = %#v", options)
	}
	if !options.Calibration {
		t.Fatalf("commit options = %#v", options)
	}
}

func assertRemoteCalibrationPushOptions(t *testing.T, options remoteRunOptions, commit, base string) {
	t.Helper()
	if options.Commit != commit {
		t.Fatalf("push options = %#v", options)
	}
	if options.Tree != "" {
		t.Fatalf("push options = %#v", options)
	}
	if options.Base != base {
		t.Fatalf("push options = %#v", options)
	}
	if options.Scenario != "push" {
		t.Fatalf("push options = %#v", options)
	}
	if options.Entrypoint != string(gatecontract.CIEntrypointGitPrePush) {
		t.Fatalf("push options = %#v", options)
	}
	if options.UpdateKind != string(gatecontract.UpdateKindFastForward) {
		t.Fatalf("push options = %#v", options)
	}
	if !options.Calibration {
		t.Fatalf("push options = %#v", options)
	}
}

func assertRemoteCalibrationReleaseOptions(t *testing.T, options remoteRunOptions, commit string) {
	t.Helper()
	if options.Commit != commit {
		t.Fatalf("release options = %#v", options)
	}
	if options.Tree != "" {
		t.Fatalf("release options = %#v", options)
	}
	if options.Base != "" {
		t.Fatalf("release options = %#v", options)
	}
	if options.Scenario != "full" {
		t.Fatalf("release options = %#v", options)
	}
	if options.Entrypoint != string(gatecontract.CIEntrypointRelease) {
		t.Fatalf("release options = %#v", options)
	}
	if !options.Calibration {
		t.Fatalf("release options = %#v", options)
	}
}
