package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"golang.org/x/sync/errgroup"
)

func TestParseRemoteRunOptions(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(cicontract.AgentTokenEnvironment, token)
	options, err := parseRemoteRunOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--repository", "/tmp/repository",
		"--commit", "main",
		"--base", "main^",
		"--profile", string(gatecontract.ProfileRelease),
		"--ledger", "/tmp/remote-ci.baseline-state.sqlite",
	})
	if err != nil {
		t.Fatalf("parseRemoteRunOptions() error = %v", err)
	}
	assertParsedRemoteRunOptions(t, options, digest)
}

func TestParseRemoteRunOptionsRejectsRetiredWorkloadReuseFlag(t *testing.T) {
	_, err := parseRemoteRunOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--force-rerun",
	})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("parseRemoteRunOptions(retired workload reuse flag) error = %v", err)
	}
}

func TestParseRemoteRunOptionsDerivesSingleSQLiteAuthority(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	options, err := parseRemoteRunOptions([]string{"--config", "/tmp/remote-ci.json", "--agent-token", token})
	if err != nil {
		t.Fatal(err)
	}
	if options.LedgerPath != "/tmp/remote-ci.baseline-state.sqlite" {
		t.Fatalf("remote SQLite authority = %q", options.LedgerPath)
	}
	_, err = parseRemoteRunOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--ledger", "/tmp/another-truth-source.sqlite", "--agent-token", token,
	})
	if err == nil || !strings.Contains(err.Error(), "SQLite authority") {
		t.Fatalf("second SQLite truth source error = %v", err)
	}
}

func TestParseRemoteRunOptionsRejectsRetiredRequesterFingerprint(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseRemoteRunOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--ledger", "/tmp/remote-ci.baseline-state.sqlite",
		"--agent-token", token,
		"--requester-fingerprint", "sha256:" + strings.Repeat("b", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("retired requester fingerprint error = %v", err)
	}
}

func TestParseRemoteRunOptionsRejectsInvalidInvocation(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--config", "/tmp/config.json", "--ledger", "/tmp/ledger.json", "--max-shards", "1"},
		{"--config", "/tmp/config.json", "--ledger", "/tmp/ledger.json", "unexpected"},
		{
			"--config", "/tmp/config.json", "--ledger", "/tmp/ledger.json",
			"--commit", "HEAD", "--tree", "HEAD^{tree}",
		},
	} {
		if _, err := parseRemoteRunOptions(args); err == nil {
			t.Fatalf("parseRemoteRunOptions(%q) unexpectedly passed", args)
		}
	}
}

func TestParseRemoteRunOptionsRejectsRemovedShardFlag(t *testing.T) {
	if _, err := parseRemoteRunOptions([]string{"--config", "/tmp/config.json", "--max-shards", "1"}); err == nil {
		t.Fatal("parseRemoteRunOptions() accepted removed --max-shards flag")
	}
}

func TestLoadRemoteRunConfig(t *testing.T) {
	document := strings.Replace(
		validRemoteRunConfigJSON(),
		`"source_prefix": "source-deltas/"`,
		`"source_prefix": "baseline-artifacts/source-deltas/"`,
		1,
	)
	path := writeRemoteRunConfigFixture(t, document)
	config, err := loadRemoteRunConfig(path)
	if err != nil {
		t.Fatalf("loadRemoteRunConfig() error = %v", err)
	}
	if config.RegionID != "cn-shenzhen" || config.OSS.SourcePrefix != "baseline-artifacts/source-deltas/" ||
		len(config.Capacity.ResourcePolicy.Classes) != 4 ||
		config.Capacity.ResourcePolicy.Bootstrap.GoTest != "memory" {
		t.Fatalf("loadRemoteRunConfig() = %#v", config)
	}
}

func TestLoadRemoteRunConfigRejectsDrift(t *testing.T) {
	cases := map[string]string{
		"unknown field":          strings.Replace(validRemoteRunConfigJSON(), `"schema_version": 8`, `"schema_version": 8, "unknown": true`, 1),
		"legacy schema":          strings.Replace(validRemoteRunConfigJSON(), `"schema_version": 8`, `"schema_version": 7`, 1),
		"legacy data cache":      strings.Replace(validRemoteRunConfigJSON(), `"capacity":`, `"data_cache": {}, "capacity":`, 1),
		"legacy OCI cache":       strings.Replace(validRemoteRunConfigJSON(), `"capacity":`, `"oci_cache": {}, "capacity":`, 1),
		"legacy runtime":         strings.Replace(validRemoteRunConfigJSON(), `"capacity":`, `"runtime": {"image": "registry.example/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, "capacity":`, 1),
		"legacy baseline prefix": strings.Replace(validRemoteRunConfigJSON(), `"source_prefix": "source-deltas/"`, `"source_prefix": "source-deltas/", "baseline_prefix": "baseline-artifacts/"`, 1),
		"legacy seed class":      strings.Replace(validRemoteRunConfigJSON(), `"resource_policy":`, `"seed_class": "memory", "resource_policy":`, 1),
		"missing network":        strings.Replace(validRemoteRunConfigJSON(), `"vswitch_id": "vsw-test"`, `"vswitch_id": ""`, 1),
		"wrong cpu":              strings.Replace(validRemoteRunConfigJSON(), `"vcpu": 2`, `"vcpu": 3`, 1),
		"wrong memory":           strings.Replace(validRemoteRunConfigJSON(), `"memory_gib": 32`, `"memory_gib": 64`, 1),
		"unknown bootstrap":      strings.Replace(validRemoteRunConfigJSON(), `"go_test": "memory"`, `"go_test": "missing"`, 1),
		"absolute source":        strings.Replace(validRemoteRunConfigJSON(), `"source_prefix": "source-deltas/"`, `"source_prefix": "/source-deltas/"`, 1),
		"traversal source":       strings.Replace(validRemoteRunConfigJSON(), `"source_prefix": "source-deltas/"`, `"source_prefix": "../source-deltas/"`, 1),
		"unterminated source":    strings.Replace(validRemoteRunConfigJSON(), `"source_prefix": "source-deltas/"`, `"source_prefix": "source-deltas"`, 1),
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeRemoteRunConfigFixture(t, document)
			if _, err := loadRemoteRunConfig(path); err == nil {
				t.Fatal("loadRemoteRunConfig() unexpectedly passed")
			}
		})
	}
}

func TestValidateRunnableRemoteBaselineRejectsMissingOCIProjectCache(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	state := remoteRunBaselineState(t, repository)
	if err := validateRunnableRemoteBaseline(state); err != nil {
		t.Fatalf("current baseline rejected: %v", err)
	}
	state.OCIProjectCache = nil
	err := validateRunnableRemoteBaseline(state)
	if err == nil || !strings.Contains(err.Error(), "OCI project cache") {
		t.Fatalf("legacy baseline error = %v, want OCI cache rejection", err)
	}
}

func TestResolveRemoteRunInputAcceptsRefreshedRuntimeImageFromSQLiteAuthority(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	state := remoteRunBaselineState(t, repository)
	refreshedImage := "registry.example/runtime@sha256:" + strings.Repeat("c", 64)
	state.RuntimeImage = refreshedImage
	state.ImageDigest = remoteRuntimeImageDigest(refreshedImage)
	state.OCIProjectCache.Image = refreshedImage
	state.ImageCacheID = "imc-audit-only-1"
	state.ImageCacheSnapshotID = "snap-runtime-1"
	input, err := resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Profile:        string(gatecontract.ProfileLocalFast),
		LedgerPath:     writeRemoteRunLedgerFixture(t, remoteRunRunnerIdentity(state)),
	}, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatalf("refreshed SQLite baseline rejected by run source: %v", err)
	}
	if input.RunnerImage != refreshedImage {
		t.Fatalf("run source runner image = %q, want refreshed SQLite authority %q", input.RunnerImage, refreshedImage)
	}
	if input.ImageCacheSnapshotID != state.ImageCacheSnapshotID {
		t.Fatalf("run source ImageCacheSnapshotID = %q, want accepted baseline %q", input.ImageCacheSnapshotID, state.ImageCacheSnapshotID)
	}
	if input.ImageCacheSnapshotID == state.ImageCacheID {
		t.Fatalf("run source used audit ImageCacheID %q instead of snapshot %q", state.ImageCacheID, state.ImageCacheSnapshotID)
	}
}

func TestResolveRemoteRunInputUsesExactGitObjects(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	ledgerPath := writeRemoteRunLedgerFixture(t)
	state := remoteRunBaselineState(t, repository)
	input, err := resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Profile:        string(gatecontract.ProfileLocalFast),
		LedgerPath:     ledgerPath,
	}, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatalf("resolveRemoteRunInput() error = %v", err)
	}
	assertRemoteRunInputIdentity(t, input, state)
	assertRemoteRunBaselineProjection(t, input, state)
}

func TestResolveRemoteRunInputCompilesCandidateGateWhenCLIClosureChanges(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	mainPath := filepath.Join(repository, "cmd", "super-dolphin-gate", "main.go")
	if err := os.WriteFile(mainPath, []byte("package main\n\nfunc main() { println(\"candidate\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runRemoteRunGit(t, repository, "add", "cmd/super-dolphin-gate/main.go")
	runRemoteRunGit(t, repository, "commit", "--quiet", "-m", "修改候选 CLI")
	state := remoteRunBaselineState(t, repository)
	input, err := resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Scenario:       "commit",
		LedgerPath:     writeRemoteRunLedgerFixture(t),
	}, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatal(err)
	}
	canonicalRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	baselineSource, _, _, err := remoteci.LoadGateCLICompileClosure(context.Background(), canonicalRepository, state.MainTree)
	if err != nil {
		t.Fatal(err)
	}
	if input.CandidateGateSourceSHA256 == baselineSource {
		t.Fatal("candidate gate closure change kept the baseline source digest")
	}
}

func TestResolveRemoteRunInputBindsAuthoritativePreCommitTree(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	ledgerPath := writeRemoteRunLedgerFixture(t)
	tree := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	parent := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^")
	state := remoteRunBaselineState(t, repository)
	input, err := resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		Tree:           tree,
		ParentCommit:   parent,
		Scenario:       "commit",
		Entrypoint:     string(gatecontract.CIEntrypointGitPreCommit),
		LedgerPath:     ledgerPath,
	}, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatalf("resolveRemoteRunInput() error = %v", err)
	}
	if input.Commit != "" || input.Tree != tree ||
		input.Entrypoint != gatecontract.CIEntrypointGitPreCommit ||
		input.Source.Kind != gatecontract.SourceKindTree ||
		input.Source.Tree == nil ||
		input.Source.Tree.SHA != tree ||
		input.Source.Tree.ParentCommitSHA != parent {
		t.Fatalf("pre-commit input = %#v", input)
	}
}

func TestResolveRemoteRunInputBindsPushRange(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	ledgerPath := writeRemoteRunLedgerFixture(t)
	base := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^")
	state := remoteRunBaselineState(t, repository)
	input, err := resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		RemoteName:     "origin",
		RemoteURL:      "ssh://git@example.invalid/repository.git",
		Commit:         "HEAD",
		Base:           base,
		Profile:        string(gatecontract.ProfilePush),
		LocalRef:       "refs/heads/main",
		RemoteRef:      "refs/heads/main",
		ObservedRemote: base,
		UpdateKind:     string(gatecontract.UpdateKindFastForward),
		LedgerPath:     ledgerPath,
	}, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatalf("resolveRemoteRunInput() error = %v", err)
	}
	assertRemoteRunPushSource(t, input, base)
}

func TestResolveRemoteRunInputRejectsPushWithoutRangeIdentity(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	ledgerPath := writeRemoteRunLedgerFixture(t)
	state := remoteRunBaselineState(t, repository)
	_, err := resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Profile:        string(gatecontract.ProfilePush),
		LedgerPath:     ledgerPath,
	}, state, remoteRunRunnerIdentity(state))
	if err == nil {
		t.Fatal("resolveRemoteRunInput() accepted push without range identity")
	}
}

func TestResolveRemoteRunInputBindsPushCreateToEmptyTree(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	ledgerPath := writeRemoteRunLedgerFixture(t)
	state := remoteRunBaselineState(t, repository)
	input, err := resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Scenario:       "push",
		LocalRef:       "refs/heads/topic",
		RemoteRef:      "refs/heads/topic",
		ObservedRemote: strings.Repeat("0", 40),
		UpdateKind:     string(gatecontract.UpdateKindCreate),
		LedgerPath:     ledgerPath,
	}, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatalf("resolveRemoteRunInput() error = %v", err)
	}
	if input.Source.Range == nil || input.Source.Range.BaseKind != gatecontract.BaseKindEmptyTree ||
		input.Source.Range.BaseSHA != "" || input.Base == "" {
		t.Fatalf("create source = %#v, bundle base = %q", input.Source, input.Base)
	}
}

func writeRemoteRunLedgerFixture(t *testing.T, runners ...string) string {
	t.Helper()
	runner := remoteRunRunnerIdentity(remoteRunRunnerIdentityState())
	if len(runners) > 1 {
		t.Fatalf("remote run ledger fixture accepts at most one runner identity, got %d", len(runners))
	}
	if len(runners) == 1 {
		runner = runners[0]
	}
	path := filepath.Join(t.TempDir(), "ci-duration-ledger.sqlite")
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ledger := gatecontract.NewDurationLedger()
	ledger.Calibration = &gatecontract.DurationCalibration{
		SchemaVersion:        gatecontract.DurationCalibrationSchemaVersion,
		Commit:               strings.Repeat("1", 40),
		Tree:                 strings.Repeat("2", 40),
		Platform:             "linux/amd64",
		Runner:               runner,
		Toolchain:            remoteRunRunnerIdentityState().ToolchainDigest,
		CommitEntrypoint:     gatecontract.CIEntrypointGitPreCommit,
		PushEntrypoint:       gatecontract.CIEntrypointGitPrePush,
		ReleaseEntrypoint:    gatecontract.CIEntrypointRelease,
		CommitCatalogDigest:  "sha256:" + strings.Repeat("7", 64),
		PushCatalogDigest:    "sha256:" + strings.Repeat("8", 64),
		ReleaseCatalogDigest: "sha256:" + strings.Repeat("9", 64),
		WorkloadCount:        1,
		RacePackageCount:     1,
		CompletedAt:          time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC),
	}
	if _, err := store.CompareAndSwap(0, ledger); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareRemoteCalibrationLedgerInitializesAndResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.sqlite")
	store, err := prepareRemoteCalibrationLedger(path)
	if err != nil {
		t.Fatalf("prepareRemoteCalibrationLedger() error = %v", err)
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != 1 || snapshot.Ledger.Calibration != nil ||
		snapshot.Ledger.Version != gatecontract.NewDurationLedger().Version {
		t.Fatalf("initial snapshot = %#v", snapshot)
	}
	if _, err := prepareRemoteCalibrationLedger(path); err != nil {
		t.Fatalf("prepareRemoteCalibrationLedger(resume) error = %v", err)
	}
}

func TestRemoteCalibrationRunOptionsUseAuthoritativeCommitPushAndReleaseEntrypoints(t *testing.T) {
	commit := strings.Repeat("1", 40)
	tree := strings.Repeat("2", 40)
	base := strings.Repeat("3", 40)
	commitOptions, pushOptions, releaseOptions := remoteCalibrationRunOptions(
		remoteRunOptions{ConfigPath: "/tmp/config", LedgerPath: "/tmp/ledger"},
		commit,
		tree,
		base,
	)
	assertRemoteCalibrationCommitOptions(t, commitOptions, tree, base)
	assertRemoteCalibrationPushOptions(t, pushOptions, commit, base)
	assertRemoteCalibrationReleaseOptions(t, releaseOptions, commit)
}

func TestAcceptRemoteDurationCalibrationRequiresEveryShardableWorkloadAndRacePackage(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	samples, missingWorkload, missingRace := fixture.samplesExceptRequiredWorkloads(t)
	if _, err := fixture.store.AppendSamples(fixture.acceptedGeneration, samples); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.accept(); err == nil {
		t.Fatal("acceptRemoteDurationCalibration() accepted missing workload samples")
	}
	if _, err := fixture.store.AppendSamples(fixture.acceptedGeneration, []gatecontract.DurationSample{missingWorkload}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.accept(); err == nil {
		t.Fatal("acceptRemoteDurationCalibration() accepted a missing race duration sample")
	}
	if _, err := fixture.store.AppendSamples(fixture.acceptedGeneration, []gatecontract.DurationSample{missingRace}); err != nil {
		t.Fatal(err)
	}
	accepted, err := fixture.acceptExistingSamples()
	if err != nil {
		t.Fatalf("acceptRemoteDurationCalibrationFromExistingSamples() error = %v", err)
	}
	if !accepted {
		t.Fatal("existing complete duration samples did not repair calibration metadata")
	}
	snapshot, err := fixture.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	assertAcceptedRemoteCalibration(t, snapshot, fixture)
}

func TestAcceptRemoteDurationCalibrationReusesEquivalentConcurrentWinner(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	appendCompleteRemoteCalibrationSamples(t, fixture)
	first, second := concurrentRemoteCalibrationAccepts(t, fixture)
	assertEquivalentCalibrationSnapshots(t, first, second)
}

func TestAcceptRemoteDurationCalibrationRejectsNonEquivalentConcurrentWinner(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	appendCompleteRemoteCalibrationSamples(t, fixture)
	if _, err := fixture.accept(); err != nil {
		t.Fatal(err)
	}
	conflict := fixture.calibration
	conflict.Tree = strings.Repeat("f", 40)
	if _, err := acceptRemoteDurationCalibration(fixture.store, conflict, fixture.commitCatalog, fixture.pushCatalog, fixture.releaseCatalog); err == nil || !strings.Contains(err.Error(), "completed concurrently") {
		t.Fatalf("non-equivalent concurrent calibration error = %v", err)
	}
}

func appendCompleteRemoteCalibrationSamples(t *testing.T, fixture remoteDurationCalibrationFixture) {
	t.Helper()
	samples, missingWorkload, missingRace := fixture.samplesExceptRequiredWorkloads(t)
	samples = append(samples, missingWorkload, missingRace)
	if _, err := fixture.store.AppendSamples(fixture.acceptedGeneration, samples); err != nil {
		t.Fatal(err)
	}
}

func concurrentRemoteCalibrationAccepts(t *testing.T, fixture remoteDurationCalibrationFixture) (gatecontract.DurationLedgerSnapshot, gatecontract.DurationLedgerSnapshot) {
	t.Helper()
	results := make(chan gatecontract.DurationLedgerSnapshot, 2)
	var group errgroup.Group
	for range 2 {
		group.Go(func() error {
			snapshot, err := fixture.accept()
			if err == nil {
				results <- snapshot
			}
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	close(results)
	var snapshots []gatecontract.DurationLedgerSnapshot
	for snapshot := range results {
		snapshots = append(snapshots, snapshot)
	}
	if len(snapshots) != 2 {
		t.Fatalf("accepted snapshots = %d, want 2", len(snapshots))
	}
	return snapshots[0], snapshots[1]
}

func assertEquivalentCalibrationSnapshots(t *testing.T, first, second gatecontract.DurationLedgerSnapshot) {
	t.Helper()
	if first.Ledger.Calibration == nil || second.Ledger.Calibration == nil || !equivalentRemoteDurationCalibration(*first.Ledger.Calibration, *second.Ledger.Calibration) {
		t.Fatalf("equivalent concurrent snapshots = %#v, %#v", first.Ledger.Calibration, second.Ledger.Calibration)
	}
}

func TestAcceptRemoteDurationCalibrationDoesNotRequireOwnerOnlyWorkloadDuration(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	samples := make([]gatecontract.DurationSample, 0, len(fixture.expected))
	foundOwnerOnly := false
	for _, workload := range fixture.expected {
		if !workload.Shardable {
			foundOwnerOnly = true
			continue
		}
		samples = append(samples, gatecontract.DurationSample{
			Bucket: gatecontract.DurationBucket{
				WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
				Platform: fixture.calibration.Platform, Runner: fixture.calibration.Runner,
				Toolchain: fixture.calibration.Toolchain,
			},
			Succeeded:  true,
			DurationMS: 1_000,
		})
	}
	if !foundOwnerOnly {
		t.Fatal("calibration catalogs contain no owner-only workload")
	}
	if _, err := fixture.store.AppendSamples(fixture.acceptedGeneration, samples); err != nil {
		t.Fatal(err)
	}
	accepted, err := fixture.acceptExistingSamples()
	if err != nil {
		t.Fatalf("acceptRemoteDurationCalibrationFromExistingSamples() error = %v", err)
	}
	if !accepted {
		t.Fatal("complete shardable duration samples did not repair calibration metadata")
	}
}

func TestRemoteProfileDeadlineKeeps100SecondsAdvisory(t *testing.T) {
	cases := []struct {
		profile gatecontract.Profile
		want    time.Duration
	}{
		{profile: gatecontract.ProfileLocalFast, want: 10 * time.Minute},
		{profile: gatecontract.ProfilePush, want: 10 * time.Minute},
		{profile: gatecontract.ProfileRelease, want: 30 * time.Minute},
	}
	for _, testCase := range cases {
		got, err := remoteProfileDeadline(testCase.profile)
		if err != nil || got != testCase.want {
			t.Fatalf("remoteProfileDeadline(%q) = %v, %v, want %v", testCase.profile, got, err, testCase.want)
		}
	}
	if _, err := remoteProfileDeadline(gatecontract.Profile("unknown")); err == nil {
		t.Fatal("remoteProfileDeadline() accepted unsupported profile")
	}
}

func TestRemoteContainerDeadlineKeepsWorkerBudgetSeparateFromMaterialization(t *testing.T) {
	for _, workerTimeout := range []time.Duration{10 * time.Minute, 30 * time.Minute} {
		got, err := remoteContainerDeadline(workerTimeout)
		want := workerTimeout + remoteWorkerSetupAllowance + remoteContainerReportAllowance
		if err != nil || got != want {
			t.Fatalf("remoteContainerDeadline(%v) = %v, %v", workerTimeout, got, err)
		}
	}
	for _, invalid := range []time.Duration{gatecontract.FullCITargetDuration, time.Second} {
		if _, err := remoteContainerDeadline(invalid); err == nil {
			t.Fatalf("remoteContainerDeadline() accepted worker timeout %s", invalid)
		}
	}
}

func TestResolveRemoteScenarioCoversFourEntrypoints(t *testing.T) {
	cases := map[string]gatecontract.Profile{
		"commit": gatecontract.ProfileLocalFast,
		"push":   gatecontract.ProfilePush,
		"full":   gatecontract.ProfileRelease,
		"test":   gatecontract.ProfileLocalFast,
	}
	for scenario, wantProfile := range cases {
		options := remoteRunOptions{Scenario: scenario}
		if scenario == "test" {
			options.Tests = []string{"./internal/module/skill"}
		}
		gotScenario, gotProfile, err := resolveRemoteScenario(options)
		if err != nil {
			t.Fatalf("resolveRemoteScenario(%q) error = %v", scenario, err)
		}
		if gotScenario != scenario || gotProfile != wantProfile {
			t.Fatalf("resolveRemoteScenario(%q) = %q, %q", scenario, gotScenario, gotProfile)
		}
	}
}

func TestSelectRemoteTestsRequiresExactInventoryTargets(t *testing.T) {
	inventory := gatecontract.WorkloadInventory{
		GoPackages:        []string{"./internal/module/skill"},
		FrontendFullTests: []string{"src/App.test.tsx"},
	}
	selected, err := selectRemoteTests(inventory, []string{
		"./internal/module/skill#TestLoad",
		"./internal/module/skill#BenchmarkLoad",
		"frontend-app/src/App.test.tsx",
	})
	if err != nil {
		t.Fatalf("selectRemoteTests() error = %v", err)
	}
	if len(selected.GoTests) != 1 || len(selected.GoBenchmarks) != 1 ||
		len(selected.FrontendFullTests) != 1 {
		t.Fatalf("selected = %#v", selected)
	}
	if _, err := selectRemoteTests(inventory, []string{"./internal/missing"}); err == nil {
		t.Fatal("selectRemoteTests() accepted a target absent from the exact source tree")
	}
	if _, err := selectRemoteTests(inventory, []string{
		"./internal/module/skill",
		"./internal/module/skill#TestLoad",
	}); err == nil {
		t.Fatal("selectRemoteTests() accepted overlapping package and exact test selectors")
	}
}
