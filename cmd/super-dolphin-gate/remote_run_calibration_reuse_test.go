package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// remoteCalibrationRacePackage 仅为证据回归测试识别独立 race workload。
func remoteCalibrationRacePackage(parent gatecontract.GateID, kind gatecontract.WorkloadKind) bool {
	return parent == gatecontract.GateIDBackendTestGuardWithRace && kind == gatecontract.WorkloadKindGoTest
}

func TestRemoteCalibrationReusedPassRequiresCoverageAndOverheadNotSyntheticTiming(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	fixture.calibration.Toolchain = remoteRunRunnerIdentityState().ToolchainDigest
	passed := remoteCalibrationFixturePassedWorkloads(fixture)

	if _, err := acceptRemoteDurationCalibrationWithPasses(
		fixture.store, fixture.calibration, passed,
		fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog,
	); err == nil || !strings.Contains(err.Error(), "accepted shard overhead is incomplete") {
		t.Fatalf("reused PASS without shard overhead error = %v", err)
	}

	acceptedFixture := newRemoteDurationCalibrationFixture(t)
	acceptedFixture.calibration.Toolchain = remoteRunRunnerIdentityState().ToolchainDigest
	seedRemoteRunShardOverheadFixture(
		t, acceptedFixture.store, acceptedFixture.calibration.Runner, acceptedFixture.calibration.AcceptedSnapshotID,
	)
	if _, err := acceptRemoteDurationCalibrationWithPasses(
		acceptedFixture.store, acceptedFixture.calibration,
		remoteCalibrationFixturePassedWorkloads(acceptedFixture),
		acceptedFixture.commitCatalog, acceptedFixture.pushCatalog, acceptedFixture.releaseCatalog,
	); err != nil {
		t.Fatalf("reused PASS with canonical coverage and persisted overhead: %v", err)
	}
}

// TestRemoteCalibrationReusedPassAcceptsCrossInputUpperBoundWithCurrentPass
// 验证保守历史时长只有与当前 input correctness PASS 联合时才可接受。
func TestRemoteCalibrationReusedPassAcceptsCrossInputUpperBoundWithCurrentPass(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	fixture.calibration.Toolchain = remoteRunRunnerIdentityState().ToolchainDigest
	seedRemoteDurationCalibrationFixtureOverhead(t, fixture)
	samples, missingWorkload, missingRace := fixture.samplesExceptRequiredWorkloads(t)
	samples = append(samples, missingWorkload, missingRace)
	for index := range samples {
		samples[index].Bucket.InputDigest = "sha256:" + strings.Repeat("f", 64)
	}
	if _, err := fixture.store.AppendSamples(fixture.acceptedGeneration, samples); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRemoteDurationCalibrationWithPasses(
		fixture.store, fixture.calibration, nil,
		fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog,
	); err == nil || !strings.Contains(err.Error(), "no successful calibration duration evidence") {
		t.Fatalf("cross-input history without current PASS error = %v", err)
	}
	if _, err := acceptRemoteDurationCalibrationWithPasses(
		fixture.store, fixture.calibration, remoteCalibrationFixturePassedWorkloads(fixture),
		fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog,
	); err != nil {
		t.Fatalf("cross-input upper bounds with current PASS: %v", err)
	}
}

func TestRemoteCalibrationCoverageAcceptsPersistedPassIdentityWithoutFreshExecution(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	identities := make([]gatecontract.WorkloadPassIdentity, 0, len(fixture.commitCatalog.Workloads))
	for _, workload := range fixture.commitCatalog.Workloads {
		identities = append(identities, mustRemoteCalibrationPassIdentity(t, workload))
	}
	result := remoteci.RunResult{
		Status: gatecontract.ResultStatusPassed, WorkloadPassIdentities: identities,
	}
	passed, err := remoteCalibrationPassedCatalogWorkloadSet(
		remoteci.RunInput{Inventory: fixture.inventory}, fixture.commitCatalog, result,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range fixture.commitCatalog.Workloads {
		if _, ok := passed[remoteCalibrationWorkloadKey(workload)]; !ok {
			t.Fatalf("persisted PASS identity did not cover workload %q", workload.ID)
		}
	}
}

func TestRemoteCalibrationCoverageSeparatesSameCommandByInputDigest(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	var original gatecontract.Workload
	for _, workload := range fixture.commitCatalog.Workloads {
		if workload.Shardable {
			original = workload
			break
		}
	}
	if original.ID == "" {
		t.Fatal("calibration catalog contains no shardable workload")
	}
	drifted := original
	drifted.InputDigest = "sha256:" + strings.Repeat("f", 64)
	if drifted.InputDigest == original.InputDigest {
		t.Fatal("test workload input digests did not diverge")
	}
	identity := mustRemoteCalibrationPassIdentity(t, original)
	result := remoteci.RunResult{
		Status: gatecontract.ResultStatusPassed, WorkloadPassIdentities: []gatecontract.WorkloadPassIdentity{identity},
	}
	catalog := gatecontract.WorkloadCatalog{Version: fixture.commitCatalog.Version, Authoritative: fixture.commitCatalog.Authoritative, Workloads: []gatecontract.Workload{original, drifted}}
	passed, err := remoteCalibrationPassedCatalogWorkloadSet(
		remoteci.RunInput{Inventory: fixture.inventory}, catalog, result,
	)
	if err != nil {
		t.Fatal(err)
	}
	if remoteCalibrationWorkloadKey(original) == remoteCalibrationWorkloadKey(drifted) {
		t.Fatal("same-command workloads with different input digests collided")
	}
	if _, ok := passed[remoteCalibrationWorkloadKey(original)]; !ok {
		t.Fatalf("persisted PASS identity did not cover original workload %q", original.ID)
	}
	if _, ok := passed[remoteCalibrationWorkloadKey(drifted)]; ok {
		t.Fatalf("persisted PASS identity incorrectly covered input-digest drift for %q", drifted.ID)
	}
}

func TestRemoteCalibrationEvidenceExpectedKeyIncludesInputDigest(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	var original gatecontract.Workload
	for _, catalog := range []gatecontract.WorkloadCatalog{fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog} {
		for _, workload := range catalog.Workloads {
			parent, err := gatecontract.WorkloadParentGateID(workload.ID)
			if err != nil {
				t.Fatal(err)
			}
			if workload.Shardable && remoteCalibrationRacePackage(parent, workload.Kind) {
				original = workload
				break
			}
		}
		if original.ID != "" {
			break
		}
	}
	if original.ID == "" {
		t.Fatal("calibration catalog contains no shardable race workload")
	}
	drifted := original
	drifted.InputDigest = "sha256:" + strings.Repeat("a", 64)
	passed := map[string]struct{}{remoteCalibrationWorkloadKey(original): {}}
	catalog := gatecontract.WorkloadCatalog{Version: fixture.commitCatalog.Version, Authoritative: fixture.commitCatalog.Authoritative, Workloads: []gatecontract.Workload{original, drifted}}
	if _, _, err := verifyRemoteCalibrationEvidence(gatecontract.DurationSampleIndex{}, passed, catalog); err == nil || !strings.Contains(err.Error(), "no successful calibration duration evidence") {
		t.Fatalf("input-digest drift was not independently required by evidence verifier: %v", err)
	}
}

func TestRemoteCalibrationEvidenceRejectsEmptyInputDigest(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	var workload gatecontract.Workload
	for _, candidate := range fixture.commitCatalog.Workloads {
		if candidate.Shardable {
			workload = candidate
			break
		}
	}
	if workload.ID == "" {
		t.Fatal("calibration catalog contains no shardable workload")
	}
	workload.InputDigest = ""
	catalog := gatecontract.WorkloadCatalog{
		Version: fixture.commitCatalog.Version, Authoritative: fixture.commitCatalog.Authoritative,
		Workloads: []gatecontract.Workload{workload},
	}
	_, _, err := verifyRemoteCalibrationEvidence(gatecontract.DurationSampleIndex{}, nil, catalog)
	if err == nil || !errors.Is(err, errRemoteCalibrationSamplesIncomplete) {
		t.Fatalf("empty production input digest error = %v, want errRemoteCalibrationSamplesIncomplete", err)
	}
}

func TestRemoteCalibrationRunsRejectResultInputIdentityDrift(t *testing.T) {
	inputs, results := remoteCalibrationResultBindingFixtures()
	cases := []struct {
		name   string
		mutate func(*remoteci.RunResult)
	}{
		{name: "source tree", mutate: func(result *remoteci.RunResult) { result.SourceTreeSHA = strings.Repeat("b", 40) }},
		{name: "entrypoint", mutate: func(result *remoteci.RunResult) { result.Entrypoint = gatecontract.CIEntrypointRelease }},
		{name: "accepted generation", mutate: func(result *remoteci.RunResult) { result.AcceptedGeneration++ }},
		{name: "image snapshot", mutate: func(result *remoteci.RunResult) { result.ImageCacheSnapshotID = "snapshot-tampered" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tampered := results
			testCase.mutate(&tampered[0])
			if err := validateRemoteCalibrationRuns(inputs, tampered); err == nil {
				t.Fatalf("validateRemoteCalibrationRuns accepted tampered %s", testCase.name)
			}
		})
	}
}

func TestRemoteCalibrationCatalogsRejectTamperedPlanBinding(t *testing.T) {
	inputs, results := remoteCalibrationCatalogBindingFixtures(t)
	if _, _, err := remoteCalibrationCatalogs(inputs, results); err != nil {
		t.Fatalf("remoteCalibrationCatalogs(valid) error = %v", err)
	}
	tampered := results
	tampered[0].PlanDigest = "sha256:" + strings.Repeat("f", 64)
	if _, _, err := remoteCalibrationCatalogs(inputs, tampered); err == nil {
		t.Fatal("remoteCalibrationCatalogs accepted a tampered plan digest")
	}
}

func remoteCalibrationResultBindingFixtures() ([3]remoteci.RunInput, [3]remoteci.RunResult) {
	var inputs [3]remoteci.RunInput
	var results [3]remoteci.RunResult
	for index, scenario := range []string{"commit", "push", "full"} {
		input, result := calibrationRunsInputResult(scenario, 1)
		input.Tree = strings.Repeat("a", 40)
		input.Source.SourceTreeSHA = input.Tree
		switch index {
		case 0:
			input.Source.Kind = gatecontract.SourceKindTree
		case 1:
			input.Source.Kind = gatecontract.SourceKindRange
		}
		result.SourceTreeSHA = input.Tree
		result.RunnerImage = input.RunnerImage
		result.Authoritative = true
		inputs[index], results[index] = input, result
	}
	return inputs, results
}

func remoteCalibrationCatalogBindingFixtures(t *testing.T) ([3]remoteci.RunInput, [3]remoteci.RunResult) {
	t.Helper()
	fixture := newRemoteDurationCalibrationFixture(t)
	base, commit, tree := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	inputs := [3]remoteci.RunInput{
		{LedgerStore: fixture.store, AcceptedGeneration: fixture.acceptedGeneration, ImageCacheSnapshotID: fixture.calibration.AcceptedSnapshotID, Tree: tree, Profile: gatecontract.ProfileLocalFast, Entrypoint: gatecontract.CIEntrypointGitPreCommit, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindTree, ObjectFormat: gatecontract.GitObjectFormatSHA1, Tree: &gatecontract.TreeSource{SHA: tree, ParentCommitSHA: base}, SourceTreeSHA: tree}},
		{LedgerStore: fixture.store, AcceptedGeneration: fixture.acceptedGeneration, ImageCacheSnapshotID: fixture.calibration.AcceptedSnapshotID, Tree: tree, Profile: gatecontract.ProfilePush, Entrypoint: gatecontract.CIEntrypointGitPrePush, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindRange, ObjectFormat: gatecontract.GitObjectFormatSHA1, Range: &gatecontract.RangeSource{BaseKind: gatecontract.BaseKindCommit, BaseSHA: base, HeadSHA: commit, LocalRef: "refs/heads/main", RemoteRef: "refs/heads/main", ObservedRemoteSHA: base, UpdateKind: gatecontract.UpdateKindFastForward}, SourceTreeSHA: tree}},
		{LedgerStore: fixture.store, AcceptedGeneration: fixture.acceptedGeneration, ImageCacheSnapshotID: fixture.calibration.AcceptedSnapshotID, Tree: tree, Profile: gatecontract.ProfileRelease, Entrypoint: gatecontract.CIEntrypointRelease, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1, Commit: &gatecontract.CommitSource{SHA: commit}, SourceTreeSHA: tree}},
	}
	catalogs := [3]gatecontract.WorkloadCatalog{fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog}
	var results [3]remoteci.RunResult
	for index, catalog := range catalogs {
		plan, err := gatecontract.BuildGatePlan(inputs[index].Profile, inputs[index].Source)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := gatecontract.WorkloadCatalogDigest(catalog)
		if err != nil {
			t.Fatal(err)
		}
		completed := time.Date(2026, time.August, 5, 1, 0, index, 0, time.UTC)
		if err := fixture.store.RecordWorkloadCatalog(catalog, gatecontract.WorkloadCatalogObservation{SourceTreeSHA: tree, Entrypoint: inputs[index].Entrypoint, Profile: inputs[index].Profile, AcceptedGeneration: inputs[index].AcceptedGeneration, ObservedAt: completed}); err != nil {
			t.Fatal(err)
		}
		results[index] = remoteci.RunResult{AcceptedGeneration: inputs[index].AcceptedGeneration, ImageCacheSnapshotID: inputs[index].ImageCacheSnapshotID, Entrypoint: inputs[index].Entrypoint, Profile: inputs[index].Profile, PlanDigest: plan.PlanDigest, CatalogDigest: digest, SourceTreeSHA: tree, Status: gatecontract.ResultStatusPassed}
	}
	return inputs, results
}

func mustRemoteCalibrationPassIdentity(t *testing.T, workload gatecontract.Workload) gatecontract.WorkloadPassIdentity {
	t.Helper()
	identity := gatecontract.WorkloadPassIdentity{
		WorkloadID:        gatecontract.GateID(workload.ID),
		ExecutionDigest:   gatecontract.WorkloadPassExecutionDigest(workload),
		InputDigest:       workload.InputDigest,
		EnvironmentDigest: "sha256:" + strings.Repeat("e", 64),
	}
	digest, err := gatecontract.WorkloadPassIdentitySHA256(identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.IdentityDigest = digest
	return identity
}

func remoteCalibrationFixturePassedWorkloads(fixture remoteDurationCalibrationFixture) map[string]struct{} {
	passed := make(map[string]struct{})
	for _, catalog := range []gatecontract.WorkloadCatalog{
		fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog,
	} {
		for _, workload := range catalog.Workloads {
			if workload.Shardable {
				passed[remoteCalibrationWorkloadKey(workload)] = struct{}{}
			}
		}
	}
	return passed
}
