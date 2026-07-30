package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestAppendRemoteDurationSamplesRefreshesSQLiteAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ci-duration-ledger.json")
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	if _, err := store.CompareAndSwap(0, gatecontract.NewDurationLedger()); err != nil {
		t.Fatalf("create duration ledger: %v", err)
	}
	parentID := "go-test:./internal/archtest"
	parentDigest := strings.Repeat("a", 64)
	testName := "TestCodeSizeGuard/size_and_freeze"
	sample := gatecontract.DurationSample{
		Bucket: gatecontract.DurationBucket{
			WorkloadID:    gatecontract.GoTestDurationWorkloadID(parentID, testName),
			CommandDigest: gatecontract.GoTestDurationCommandDigest(parentDigest, testName),
			Platform:      "linux/amd64",
			Runner:        "runner-v2",
			Toolchain:     "go1.25.1",
		},
		Succeeded:           true,
		DurationMS:          4210,
		TargetKind:          gatecontract.WorkloadKindGoTest,
		ParentWorkloadID:    parentID,
		ParentCommandDigest: parentDigest,
		TargetName:          testName,
		TargetStatus:        gatecontract.GoTestStatusPass,
	}
	if err := appendRemoteDurationSamples(path, []gatecontract.DurationSample{sample}); err != nil {
		t.Fatalf("appendRemoteDurationSamples() error = %v", err)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("load duration ledger SQLite authority: %v", err)
	}
	if persisted.Generation != 2 || len(persisted.Ledger.Samples) != 1 {
		t.Fatalf("persisted duration ledger generation=%d samples=%d, want 2 and 1", persisted.Generation, len(persisted.Ledger.Samples))
	}
	if got := persisted.Ledger.Samples[0]; got != sample {
		t.Fatalf("persisted test timing = %#v, want %#v", got, sample)
	}
	if _, err := os.Stat(store.AuthorityPath()); err != nil {
		t.Fatalf("stat duration ledger SQLite authority: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy JSON unexpectedly remained authoritative: err=%v", err)
	}
}

func TestParseRemoteRunOptions(t *testing.T) {
	requesterFingerprint := "sha256:" + strings.Repeat("a", 64)
	t.Setenv(gatecontract.RequesterFingerprintEnvironment, requesterFingerprint)
	options, err := parseRemoteRunOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--repository", "/tmp/repository",
		"--commit", "main",
		"--base", "main^",
		"--profile", string(gatecontract.ProfileRelease),
		"--ledger", "/tmp/ledger.json",
		"--max-shards", "7",
		"--force-rerun",
	})
	if err != nil {
		t.Fatalf("parseRemoteRunOptions() error = %v", err)
	}
	assertParsedRemoteRunOptions(t, options, requesterFingerprint)
}

func TestParseRemoteRunOptionsRejectsConflictingRequesterFingerprint(t *testing.T) {
	t.Setenv(
		gatecontract.RequesterFingerprintEnvironment,
		"sha256:"+strings.Repeat("a", 64),
	)
	_, err := parseRemoteRunOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--ledger", "/tmp/ledger.json",
		"--requester-fingerprint", "sha256:" + strings.Repeat("b", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("conflicting requester fingerprint error = %v", err)
	}
}

func TestParseRemoteRunOptionsRejectsInvalidInvocation(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--config", "/tmp/config.json", "--ledger", "/tmp/ledger.json", "--max-shards", "129"},
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
		config.DataCache.MaxSizeGiB != 100 ||
		config.Capacity.MaxShardsPerJob != 5 ||
		config.Capacity.SeedClass != "memory" ||
		len(config.Capacity.ResourcePolicy.Classes) != 4 ||
		config.Capacity.ResourcePolicy.Bootstrap.GoTest != "memory" {
		t.Fatalf("loadRemoteRunConfig() = %#v", config)
	}
}

func TestLoadRemoteRunConfigRejectsDrift(t *testing.T) {
	cases := map[string]string{
		"unknown field":        strings.Replace(validRemoteRunConfigJSON(), `"schema_version": 4`, `"schema_version": 4, "unknown": true`, 1),
		"refresh interval":     strings.Replace(validRemoteRunConfigJSON(), `"refresh_interval_minutes": 1440`, `"refresh_interval_minutes": 60`, 1),
		"unpinned image":       strings.Replace(validRemoteRunConfigJSON(), `"image": "registry.example/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"image": "registry.example/runtime:latest"`, 1),
		"missing network":      strings.Replace(validRemoteRunConfigJSON(), `"vswitch_id": "vsw-test"`, `"vswitch_id": ""`, 1),
		"missing seed class":   strings.Replace(validRemoteRunConfigJSON(), `"seed_class": "memory"`, `"seed_class": ""`, 1),
		"wrong cpu":            strings.Replace(validRemoteRunConfigJSON(), `"vcpu": 2`, `"vcpu": 3`, 1),
		"wrong memory":         strings.Replace(validRemoteRunConfigJSON(), `"memory_gib": 32`, `"memory_gib": 64`, 1),
		"unknown bootstrap":    strings.Replace(validRemoteRunConfigJSON(), `"go_test": "memory"`, `"go_test": "missing"`, 1),
		"legacy shard field":   strings.Replace(validRemoteRunConfigJSON(), `"max_shards_per_job": 5`, `"shards": 5`, 1),
		"small cache maximum":  strings.Replace(validRemoteRunConfigJSON(), `"max_size_gib": 100`, `"max_size_gib": 19`, 1),
		"large cache maximum":  strings.Replace(validRemoteRunConfigJSON(), `"max_size_gib": 100`, `"max_size_gib": 101`, 1),
		"legacy fixed cache":   strings.Replace(validRemoteRunConfigJSON(), `"max_size_gib": 100`, `"size_gib": 100`, 1),
		"absolute source":      strings.Replace(validRemoteRunConfigJSON(), `"source_prefix": "source-deltas/"`, `"source_prefix": "/source-deltas/"`, 1),
		"traversal source":     strings.Replace(validRemoteRunConfigJSON(), `"source_prefix": "source-deltas/"`, `"source_prefix": "../source-deltas/"`, 1),
		"unterminated source":  strings.Replace(validRemoteRunConfigJSON(), `"source_prefix": "source-deltas/"`, `"source_prefix": "source-deltas"`, 1),
		"overlapping prefixes": strings.Replace(validRemoteRunConfigJSON(), `"source_prefix": "source-deltas/"`, `"source_prefix": "baseline-artifacts/"`, 1),
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

func TestResolveRemoteRunInputUsesExactGitObjects(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	ledgerPath := writeRemoteRunLedgerFixture(t)
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatalf("loadRemoteRunConfig() error = %v", err)
	}
	state := remoteRunBaselineState(t, repository)
	if state.DataCacheSizeGiB == config.DataCache.MaxSizeGiB {
		t.Fatal("fixture must prove an accepted prior-capacity Anchor remains runnable during migration")
	}
	input, err := resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Profile:        string(gatecontract.ProfileLocalFast),
		LedgerPath:     ledgerPath,
	}, config, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatalf("resolveRemoteRunInput() error = %v", err)
	}
	assertRemoteRunInputIdentity(t, input, state)
	assertRemoteRunBaselineProjection(t, input, state)
}

func TestRemoteBaselineDeltaProjectionPreservesOrder(t *testing.T) {
	deltas := []remoteci.BaselineDeltaRef{{
		Generation: 2, SourceObjectPrefix: "baseline-artifacts/2/", ManifestDigest: "sha256:" + strings.Repeat("a", 64),
		BaseCommit: strings.Repeat("1", 40), BaseTree: strings.Repeat("2", 40),
		MainCommit: strings.Repeat("3", 40), MainTree: strings.Repeat("4", 40),
	}, {
		Generation: 3, SourceObjectPrefix: "baseline-artifacts/3/", ManifestDigest: "sha256:" + strings.Repeat("b", 64),
		BaseCommit: strings.Repeat("3", 40), BaseTree: strings.Repeat("4", 40),
		MainCommit: strings.Repeat("5", 40), MainTree: strings.Repeat("6", 40),
	}}
	projection := remoteBaselineDeltaProjection(deltas)
	if len(projection) != len(deltas) || projection[0].Generation != 2 || projection[1].Generation != 3 ||
		projection[0].ObjectPrefix != deltas[0].SourceObjectPrefix || projection[1].MainTree != deltas[1].MainTree {
		t.Fatalf("delta projection = %#v", projection)
	}
}

func TestResolveRemoteRunInputCalibrationReusesPassedWorkloadsUnlessForced(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	ledgerPath := writeRemoteRunLedgerFixture(t)
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	options := remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Scenario:       "commit",
		LedgerPath:     ledgerPath,
		Calibration:    true,
	}
	state := remoteRunBaselineState(t, repository)
	runnerIdentity := remoteRunRunnerIdentity(state)
	input, err := resolveRemoteRunInput(options, config, state, runnerIdentity)
	if err != nil {
		t.Fatalf("resolveRemoteRunInput() error = %v", err)
	}
	if input.ForceRerun {
		t.Fatal("calibration unexpectedly bypassed the passed-workload cache")
	}
	options.ForceRerun = true
	input, err = resolveRemoteRunInput(options, config, state, runnerIdentity)
	if err != nil {
		t.Fatalf("resolveRemoteRunInput(force) error = %v", err)
	}
	if !input.ForceRerun {
		t.Fatal("explicit force rerun was not propagated")
	}
}

func TestResolveRemoteRunInputBindsAuthoritativePreCommitTree(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	ledgerPath := writeRemoteRunLedgerFixture(t)
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
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
	}, config, state, remoteRunRunnerIdentity(state))
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
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	ledgerPath := writeRemoteRunLedgerFixture(t)
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
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
	}, config, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatalf("resolveRemoteRunInput() error = %v", err)
	}
	assertRemoteRunPushSource(t, input, base)
}

func TestResolveRemoteRunInputRejectsPushWithoutRangeIdentity(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	ledgerPath := writeRemoteRunLedgerFixture(t)
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	state := remoteRunBaselineState(t, repository)
	_, err = resolveRemoteRunInput(remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Profile:        string(gatecontract.ProfilePush),
		LedgerPath:     ledgerPath,
	}, config, state, remoteRunRunnerIdentity(state))
	if err == nil {
		t.Fatal("resolveRemoteRunInput() accepted push without range identity")
	}
}

func TestResolveRemoteRunInputBindsPushCreateToEmptyTree(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	ledgerPath := writeRemoteRunLedgerFixture(t)
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
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
	}, config, state, remoteRunRunnerIdentity(state))
	if err != nil {
		t.Fatalf("resolveRemoteRunInput() error = %v", err)
	}
	if input.Source.Range == nil || input.Source.Range.BaseKind != gatecontract.BaseKindEmptyTree ||
		input.Source.Range.BaseSHA != "" || input.Base == "" {
		t.Fatalf("create source = %#v, bundle base = %q", input.Source, input.Base)
	}
}

func writeRemoteRunLedgerFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ci-duration-ledger.json")
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ledger := gatecontract.NewDurationLedger()
	ledger.Calibration = &gatecontract.DurationCalibration{
		SchemaVersion:        gatecontract.DurationCalibrationSchemaVersion,
		Commit:               repeatRemoteHex("1", 40),
		Tree:                 repeatRemoteHex("2", 40),
		Platform:             "linux/arm64",
		Runner:               remoteRunRunnerIdentity(remoteRunRunnerIdentityState()),
		Toolchain:            "sha256:" + repeatRemoteHex("c", 64),
		CommitEntrypoint:     gatecontract.CIEntrypointGitPreCommit,
		PushEntrypoint:       gatecontract.CIEntrypointGitPrePush,
		ReleaseEntrypoint:    gatecontract.CIEntrypointRelease,
		CommitCatalogDigest:  "sha256:" + repeatRemoteHex("7", 64),
		PushCatalogDigest:    "sha256:" + repeatRemoteHex("8", 64),
		ReleaseCatalogDigest: "sha256:" + repeatRemoteHex("9", 64),
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
	path := filepath.Join(t.TempDir(), "duration-ledger.json")
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

func TestRemoteCalibrationRunOptionsPropagateOnlyExplicitForceRerun(t *testing.T) {
	for _, forceRerun := range []bool{false, true} {
		commitOptions, pushOptions, releaseOptions := remoteCalibrationRunOptions(
			remoteRunOptions{ForceRerun: forceRerun},
			strings.Repeat("1", 40),
			strings.Repeat("2", 40),
			strings.Repeat("3", 40),
		)
		for scenario, options := range map[string]remoteRunOptions{
			"commit": commitOptions,
			"push":   pushOptions,
			"full":   releaseOptions,
		} {
			if options.ForceRerun != forceRerun {
				t.Fatalf("scenario %s ForceRerun = %t, want %t", scenario, options.ForceRerun, forceRerun)
			}
		}
	}
}

func TestAcceptRemoteDurationCalibrationRequiresEveryWorkloadAndRacePackage(t *testing.T) {
	fixture := newRemoteDurationCalibrationFixture(t)
	samples, missingWorkload, missingRace := fixture.samplesExceptRequiredWorkloads(t)
	if _, err := fixture.store.AppendSamples(samples); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.accept(); err == nil {
		t.Fatal("acceptRemoteDurationCalibration() accepted missing workload samples")
	}
	if _, err := fixture.store.AppendSamples([]gatecontract.DurationSample{missingWorkload}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.accept(); err == nil {
		t.Fatal("acceptRemoteDurationCalibration() accepted a missing race duration sample")
	}
	if _, err := fixture.store.AppendSamples([]gatecontract.DurationSample{missingRace}); err != nil {
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
