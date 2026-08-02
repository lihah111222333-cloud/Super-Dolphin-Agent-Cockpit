package main

import (
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
	base, commit, tree := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	inventory := gatecontract.WorkloadInventory{GoPackages: []string{"./internal/alpha", "./pkg/beta"}}
	commitCatalog, commitDigest := mustRemoteCalibrationCatalog(t, remoteci.RunInput{Profile: gatecontract.ProfileLocalFast, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindTree, ObjectFormat: gatecontract.GitObjectFormatSHA1, Tree: &gatecontract.TreeSource{SHA: tree, ParentCommitSHA: base}, SourceTreeSHA: tree}, Inventory: inventory})
	pushCatalog, pushDigest := mustRemoteCalibrationCatalog(t, remoteci.RunInput{Profile: gatecontract.ProfilePush, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindRange, ObjectFormat: gatecontract.GitObjectFormatSHA1, Range: &gatecontract.RangeSource{BaseKind: gatecontract.BaseKindCommit, BaseSHA: base, HeadSHA: commit, LocalRef: "refs/heads/main", RemoteRef: "refs/heads/main", ObservedRemoteSHA: base, UpdateKind: gatecontract.UpdateKindFastForward}, SourceTreeSHA: tree}, Inventory: inventory})
	releaseCatalog, releaseDigest := mustRemoteCalibrationCatalog(t, remoteci.RunInput{Profile: gatecontract.ProfileRelease, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1, Commit: &gatecontract.CommitSource{SHA: commit}, SourceTreeSHA: tree}, Inventory: inventory})
	calibration := gatecontract.DurationCalibration{SchemaVersion: gatecontract.DurationCalibrationSchemaVersion, Commit: commit, Tree: tree, Platform: "linux/amd64", Runner: "sha256:" + strings.Repeat("4", 64), Toolchain: "sha256:" + strings.Repeat("5", 64), CommitEntrypoint: gatecontract.CIEntrypointGitPreCommit, PushEntrypoint: gatecontract.CIEntrypointGitPrePush, ReleaseEntrypoint: gatecontract.CIEntrypointRelease, CommitCatalogDigest: commitDigest, PushCatalogDigest: pushDigest, ReleaseCatalogDigest: releaseDigest, CompletedAt: time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)}
	expected := remoteCalibrationExpectedWorkloads(commitCatalog, pushCatalog, releaseCatalog)
	return remoteDurationCalibrationFixture{store: store, calibration: calibration, commitCatalog: commitCatalog, pushCatalog: pushCatalog, releaseCatalog: releaseCatalog, expected: expected, inventory: inventory}
}

func mustRemoteCalibrationCatalog(t *testing.T, input remoteci.RunInput) (gatecontract.WorkloadCatalog, string) {
	t.Helper()
	catalog, digest, err := remoteCalibrationCatalog(input)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, digest
}

func remoteCalibrationExpectedWorkloads(catalogs ...gatecontract.WorkloadCatalog) map[string]gatecontract.Workload {
	expected := make(map[string]gatecontract.Workload)
	for _, catalog := range catalogs {
		for _, workload := range catalog.Workloads {
			expected[workload.ID+"\x00"+workload.CommandDigest] = workload
		}
	}
	return expected
}

func (fixture remoteDurationCalibrationFixture) samplesExceptRequiredWorkloads(t *testing.T) ([]gatecontract.DurationSample, gatecontract.DurationSample, gatecontract.DurationSample) {
	t.Helper()
	var samples []gatecontract.DurationSample
	var missingWorkload, missingRace gatecontract.DurationSample
	for _, workload := range fixture.expected {
		sample := gatecontract.DurationSample{Bucket: gatecontract.DurationBucket{WorkloadID: workload.ID, CommandDigest: workload.CommandDigest, Platform: fixture.calibration.Platform, Runner: fixture.calibration.Runner, Toolchain: fixture.calibration.Toolchain}, Succeeded: true, DurationMS: 1_000}
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

func assertParsedRemoteRunOptions(t *testing.T, options remoteRunOptions, requesterFingerprint string) {
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
	if options.Profile != string(gatecontract.ProfileRelease) {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if options.LedgerPath != "/tmp/remote-ci.baseline-state.sqlite" {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if options.MaxShards != 7 {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if !options.ForceRerun {
		t.Fatalf("parseRemoteRunOptions() = %#v", options)
	}
	if options.RequesterFingerprint.String() != requesterFingerprint {
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
	if input.MaxShards != 5 {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.LedgerSnapshot.Generation != 1 {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.BaselineManifestDigest != state.BaselineManifestDigest {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if input.RunnerIdentityDigest == input.BaselineManifestDigest {
		t.Fatalf("resolveRemoteRunInput() = %#v", input)
	}
	if !strings.HasPrefix(input.CandidateGateSourceSHA256, "sha256:") ||
		!strings.HasPrefix(input.CandidateGateToolchainSHA256, "sha256:") ||
		!input.ReuseBaselineGateCLI {
		t.Fatalf("candidate gate identity = %#v", input)
	}
}

func assertRemoteRunBaselineProjection(t *testing.T, input remoteci.RunInput, state remoteci.BaselineState) {
	t.Helper()
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
