package remoteci

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

const calibrationCheckpointAgentTokenDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCalibrationCheckpointPersistsInAuthoritySQLite(t *testing.T) {
	store := calibrationCheckpointStore(t)
	authorityPath := store.AuthorityPath()
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("commit", input, result, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewCalibrationCheckpoint(store, "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	gotInput, gotResult, ok := requireCalibrationCheckpointCompletion(t, loaded, "commit")
	if !ok || gotInput.Tree != input.Tree || gotInput.ImageCacheSnapshotID != input.ImageCacheSnapshotID || gotResult.JobID != result.JobID || gotResult.ImageCacheSnapshotID != result.ImageCacheSnapshotID {
		t.Fatalf("loaded checkpoint = %#v, %#v, %t", gotInput, gotResult, ok)
	}
	if _, err := os.Stat(authorityPath + ".calibration.checkpoint"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint JSON was written: %v", err)
	}
}

func TestCalibrationCheckpointReopenAndCachedRetryPersist(t *testing.T) {
	store := calibrationCheckpointStore(t)
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("full", input, result, true); err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.Reopen("full"); err != nil {
		t.Fatal(err)
	}
	if calibrationCheckpointCompleted(t, checkpoint, "full") {
		t.Fatal("reopened checkpoint remained complete")
	}
	if err := checkpoint.Observe("full", input, testCalibrationCheckpointResult(input), true); err != nil {
		t.Fatal(err)
	}
	if !calibrationCheckpointCompleted(t, checkpoint, "full") {
		t.Fatal("cached retry did not restore completed checkpoint")
	}
}

func calibrationCheckpointCompleted(t *testing.T, checkpoint *CalibrationCheckpoint, scenario string) bool {
	t.Helper()
	_, _, completed := requireCalibrationCheckpointCompletion(t, checkpoint, scenario)
	return completed
}

func requireCalibrationCheckpointCompletion(t *testing.T, checkpoint *CalibrationCheckpoint, scenario string) (RunInput, RunResult, bool) {
	t.Helper()
	input, result, completed, err := checkpoint.Completed(scenario)
	if err != nil {
		t.Fatal(err)
	}
	return input, result, completed
}

func TestCalibrationCheckpointCompletedPropagatesCorruptState(t *testing.T) {
	store := calibrationCheckpointStore(t)
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("commit", input, result, true); err != nil {
		t.Fatal(err)
	}
	corruptCalibrationCheckpointResult(t, store, "sha256:checkpoint", "commit")
	_, _, completed, err := checkpoint.Completed("commit")
	if err == nil {
		t.Fatal("corrupt calibration checkpoint completed without error")
	}
	if completed {
		t.Fatal("corrupt calibration checkpoint was reported as completed")
	}
}

func corruptCalibrationCheckpointResult(t *testing.T, store *gatecontract.DurationLedgerStore, identity, scenario string) {
	t.Helper()
	database, err := sql.Open("sqlite", store.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE remote_ci_calibration_checkpoint_scenarios SET result_json = ? WHERE identity = ? AND scenario = ?`, "{", identity, scenario); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCalibrationCheckpointDoesNotPersistExecutionPayload(t *testing.T) {
	store := calibrationCheckpointStore(t)
	authorityPath := store.AuthorityPath()
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	samples := make([]gatecontract.DurationSample, 10_000)
	input := testCalibrationCheckpointInput()
	input.LedgerSnapshot = gatecontract.DurationLedgerSnapshot{Ledger: gatecontract.DurationLedger{Samples: samples}}
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = samples
	if err := checkpoint.Observe("full", input, result, true); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var inputJSON, resultJSON string
	if err := database.QueryRow(`SELECT input_json, result_json FROM remote_ci_calibration_checkpoint_scenarios WHERE identity = ? AND scenario = ?`, "sha256:checkpoint", "full").Scan(&inputJSON, &resultJSON); err != nil {
		t.Fatal(err)
	}
	if len(inputJSON)+len(resultJSON) >= 64<<10 {
		t.Fatalf("checkpoint payload bytes = %d, want < 64 KiB", len(inputJSON)+len(resultJSON))
	}
	for _, forbidden := range []string{"LedgerSnapshot", "LedgerStore", "duration_samples", "gate_executions", "shards"} {
		if strings.Contains(inputJSON, forbidden) || strings.Contains(resultJSON, forbidden) {
			t.Fatalf("checkpoint retained %q", forbidden)
		}
	}
}

func TestCalibrationCheckpointRetainsForceAuditIdentity(t *testing.T) {
	checkpoint, err := NewCalibrationCheckpoint(calibrationCheckpointStore(t), "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	input.Force = true
	result := testCalibrationCheckpointResult(input)
	result.Force = true
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("commit", input, result, true); err != nil {
		t.Fatal(err)
	}
	resumedInput, resumedResult, completed := requireCalibrationCheckpointCompletion(t, checkpoint, "commit")
	if !completed || !resumedInput.Force || !resumedResult.Force {
		t.Fatalf("checkpoint force audit identity = input=%#v result=%#v completed=%t", resumedInput.Force, resumedResult.Force, completed)
	}
}

func TestCalibrationCheckpointAcceptsAllHitWithoutMissExecutionIdentity(t *testing.T) {
	checkpoint, err := NewCalibrationCheckpoint(calibrationCheckpointStore(t), "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	input.CandidateGateSourceSHA256 = ""
	input.CandidateGateToolchainSHA256 = ""
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("push", input, result, true); err != nil {
		t.Fatalf("observe all-hit checkpoint: %v", err)
	}
	resumedInput, resumedResult, completed := requireCalibrationCheckpointCompletion(t, checkpoint, "push")
	if !completed {
		t.Fatal("all-hit checkpoint was not completed")
	}
	if resumedInput.CandidateGateSourceSHA256 != "" || resumedInput.CandidateGateToolchainSHA256 != "" ||
		resumedResult.CandidateGateSourceSHA256 != "" || resumedResult.CandidateGateToolchainSHA256 != "" {
		t.Fatal("all-hit checkpoint synthesized MISS-only candidate identity")
	}
}

func TestCalibrationCheckpointRejectsIncompleteCompletedIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RunInput, *RunResult)
	}{
		{name: "missing target platform", mutate: func(input *RunInput, _ *RunResult) { input.Platform = "" }},
		{name: "missing accepted snapshot from input", mutate: func(input *RunInput, _ *RunResult) { input.ImageCacheSnapshotID = "" }},
		{name: "missing accepted snapshot from result", mutate: func(_ *RunInput, result *RunResult) { result.ImageCacheSnapshotID = "" }},
		{name: "accepted snapshot drift", mutate: func(_ *RunInput, result *RunResult) { result.ImageCacheSnapshotID = "snap-drift" }},
		{name: "missing fixed resource", mutate: func(input *RunInput, _ *RunResult) { input.CalibrationResource.MemoryGiB = 0 }},
		{name: "missing candidate compile source", mutate: func(input *RunInput, _ *RunResult) { input.CandidateGateSourceSHA256 = "" }},
		{name: "candidate compile toolchain drift", mutate: func(_ *RunInput, result *RunResult) { result.CandidateGateToolchainSHA256 = "sha256:drift" }},
		{name: "malformed matching candidate identity", mutate: func(input *RunInput, result *RunResult) {
			input.CandidateGateSourceSHA256, result.CandidateGateSourceSHA256 = "sha256:drift", "sha256:drift"
			input.CandidateGateToolchainSHA256, result.CandidateGateToolchainSHA256 = "sha256:drift", "sha256:drift"
		}},
		{name: "accepted generation drifts from checkpoint", mutate: func(input *RunInput, result *RunResult) { input.AcceptedGeneration, result.AcceptedGeneration = 8, 8 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkpoint, err := NewCalibrationCheckpoint(calibrationCheckpointStore(t), "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
			if err != nil {
				t.Fatal(err)
			}
			input := testCalibrationCheckpointInput()
			result := testCalibrationCheckpointResult(input)
			test.mutate(&input, &result)
			if err := checkpoint.Observe("commit", input, result, true); err == nil {
				t.Fatal("completed checkpoint accepted incomplete identity")
			}
		})
	}
}

func TestCalibrationCheckpointConcurrentDifferentScenariosAreRetained(t *testing.T) {
	firstStore, secondStore := calibrationCheckpointStores(t)
	first, err := NewCalibrationCheckpoint(firstStore, "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCalibrationCheckpoint(secondStore, "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for scenario, checkpoint := range map[string]*CalibrationCheckpoint{"commit": first, "push": second} {
		group.Go(func() {
			<-start
			errs <- checkpoint.Observe(scenario, input, result, true)
		})
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	record, found, err := firstStore.LoadCalibrationCheckpoint("sha256:checkpoint", calibrationCheckpointAgentTokenDigest)
	if err != nil || !found || len(record.Scenarios) != 2 {
		t.Fatalf("checkpoint scenarios = %#v, found=%t, err=%v", record.Scenarios, found, err)
	}
}

func TestCalibrationCheckpointConcurrentSameScenarioReturnsConflict(t *testing.T) {
	firstStore, secondStore := calibrationCheckpointStores(t)
	next := gatecontract.CalibrationCheckpointScenarioRecord{Scenario: "commit", Started: true}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for _, store := range []*gatecontract.DurationLedgerStore{firstStore, secondStore} {
		group.Go(func() {
			<-start
			errs <- store.CompareAndSwapCalibrationCheckpointScenario("sha256:checkpoint", calibrationCheckpointAgentTokenDigest, calibrationCheckpointSchemaVersion, 7, nil, next)
		})
	}
	close(start)
	group.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, gatecontract.ErrCalibrationCheckpointConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("same-scenario outcomes: successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestCalibrationCheckpointRejectsDifferentAgentDigest(t *testing.T) {
	store := calibrationCheckpointStore(t)
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint", 7, calibrationCheckpointAgentTokenDigest)
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("commit", input, result, true); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCalibrationCheckpoint(store, "sha256:checkpoint", 7, "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err == nil {
		t.Fatal("checkpoint accepted a different agent token digest")
	}
}

func TestCalibrationCheckpointImageCacheSnapshotFieldContract(t *testing.T) {
	tests := []struct {
		name    string
		typeOf  reflect.Type
		jsonTag string
	}{
		{name: "run input", typeOf: reflect.TypeFor[RunInput](), jsonTag: ""},
		{name: "run result", typeOf: reflect.TypeFor[RunResult](), jsonTag: "image_cache_snapshot_id"},
		{name: "checkpoint input", typeOf: reflect.TypeFor[calibrationCheckpointInput](), jsonTag: "image_cache_snapshot_id"},
		{name: "checkpoint result", typeOf: reflect.TypeFor[calibrationCheckpointResult](), jsonTag: "image_cache_snapshot_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field, found := test.typeOf.FieldByName("ImageCacheSnapshotID")
			if !found || field.Tag.Get("json") != test.jsonTag {
				t.Fatalf("%s ImageCacheSnapshotID field contract = %#v, found=%t, want json tag %q", test.name, field, found, test.jsonTag)
			}
		})
	}
}

func TestCalibrationCheckpointRejectsRetiredSchemaVersion(t *testing.T) {
	store := calibrationCheckpointStore(t)
	if err := store.CompareAndSwapCalibrationCheckpointScenario(
		"sha256:checkpoint",
		calibrationCheckpointAgentTokenDigest,
		calibrationCheckpointSchemaVersion-1,
		7,
		nil,
		gatecontract.CalibrationCheckpointScenarioRecord{Scenario: "legacy-completed", Started: true},
	); err == nil {
		t.Fatal("checkpoint authority accepted retired schema version")
	}
}

func calibrationCheckpointAuthorityPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "duration-ledger.sqlite")
}

func calibrationCheckpointStore(t *testing.T) *gatecontract.DurationLedgerStore {
	t.Helper()
	store, err := gatecontract.NewDurationLedgerStore(calibrationCheckpointAuthorityPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(0, gatecontract.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedRemoteCITestAcceptedGeneration(t, store, 7)
	return store
}

func calibrationCheckpointStores(t *testing.T) (*gatecontract.DurationLedgerStore, *gatecontract.DurationLedgerStore) {
	t.Helper()
	authorityPath := calibrationCheckpointAuthorityPath(t)
	first, err := gatecontract.NewDurationLedgerStore(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.CompareAndSwap(0, gatecontract.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedRemoteCITestAcceptedGeneration(t, first, 7)
	second, err := gatecontract.NewDurationLedgerStore(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	return first, second
}

func seedRemoteCITestAcceptedGeneration(t *testing.T, store *gatecontract.DurationLedgerStore, generation uint64) {
	t.Helper()
	state := validBaselineState()
	state.Generation = generation
	state.ImageCacheID = fmt.Sprintf("imc-baseline-%d", generation)
	state.ImageCacheSnapshotID = fmt.Sprintf("snap-baseline-%d", generation)
	if err := state.Validate(); err != nil {
		t.Fatalf("validate accepted baseline state fixture: %v", err)
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal accepted baseline state fixture: %v", err)
	}
	stateSHA256 := fmt.Sprintf("sha256:%x", sha256.Sum256(stateJSON))
	database, err := sql.Open("sqlite", store.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO ci_remote_baseline_state (
		singleton, schema_version, generation, state_json, state_sha256, updated_at_unix_ms
	) VALUES (1, 3, ?, ?, ?, 1)
	ON CONFLICT(singleton) DO UPDATE SET
		schema_version = excluded.schema_version,
		generation = excluded.generation,
		state_json = excluded.state_json,
		state_sha256 = excluded.state_sha256,
		updated_at_unix_ms = excluded.updated_at_unix_ms`, fmt.Sprintf("%d", generation), string(stateJSON), stateSHA256); err != nil {
		t.Fatal(err)
	}
}

func testCalibrationCheckpointInput() RunInput {
	tree := strings.Repeat("1", 40)
	return RunInput{AgentTokenDigest: calibrationCheckpointAgentTokenDigest, AcceptedGeneration: 7, ImageCacheSnapshotID: "snap-accepted-baseline", Tree: tree, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindTree, Tree: &gatecontract.TreeSource{SHA: tree, ParentCommitSHA: strings.Repeat("2", 40)}, SourceTreeSHA: tree}, Profile: gatecontract.ProfileLocalFast, Entrypoint: gatecontract.CIEntrypointGitPreCommit, Platform: "linux/amd64", ToolchainDigest: "sha256:" + strings.Repeat("3", 64), CandidateGateSourceSHA256: "sha256:" + strings.Repeat("5", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("6", 64), Calibration: true, RunnerIdentityDigest: "sha256:" + strings.Repeat("4", 64), RunnerImage: "ubuntu:22.04", CalibrationResource: shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8}}
}

func testCalibrationCheckpointResult(input RunInput) RunResult {
	return RunResult{AgentTokenDigest: input.AgentTokenDigest, AcceptedGeneration: input.AcceptedGeneration, ImageCacheSnapshotID: input.ImageCacheSnapshotID, JobID: "job-checkpoint", Entrypoint: input.Entrypoint, Profile: input.Profile, PlanDigest: "sha256:" + strings.Repeat("7", 64), CatalogDigest: "sha256:" + strings.Repeat("8", 64), SourceTreeSHA: input.Tree, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, CalibrationResourceClassID: input.CalibrationResource.ID, CalibrationResourceCPU: input.CalibrationResource.VCPU, CalibrationResourceMemoryGiB: input.CalibrationResource.MemoryGiB, Status: gatecontract.ResultStatusPassed, Authoritative: true, CleanupComplete: true, CompletedAt: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)}
}
