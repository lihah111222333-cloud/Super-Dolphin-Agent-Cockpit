package remoteci

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCalibrationCheckpointPersistsInAuthoritySQLite(t *testing.T) {
	store := calibrationCheckpointStore(t)
	authorityPath := store.AuthorityPath()
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("commit", input, result, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewCalibrationCheckpoint(store, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	gotInput, gotResult, ok := loaded.Completed("commit")
	if !ok || gotInput.Tree != input.Tree || gotResult.JobID != result.JobID {
		t.Fatalf("loaded checkpoint = %#v, %#v, %t", gotInput, gotResult, ok)
	}
	if _, err := os.Stat(authorityPath + ".calibration.checkpoint"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint JSON was written: %v", err)
	}
}

func TestCalibrationCheckpointImportsCurrentLegacyJSONOnce(t *testing.T) {
	store := calibrationCheckpointStore(t)
	authorityPath := store.AuthorityPath()
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	legacy := calibrationCheckpointDocument{SchemaVersion: legacyCalibrationCheckpointSchemaVersion, Identity: "sha256:checkpoint", Scenarios: map[string]calibrationScenarioState{"commit": {Started: true, Completed: true, Input: compactCalibrationCheckpointInput(input), Result: compactCalibrationCheckpointResult(result)}}}
	content, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := authorityPath + ".calibration.checkpoint"
	if err := os.WriteFile(legacyPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if _, got, ok := checkpoint.Completed("commit"); !ok || got.JobID != result.JobID {
		t.Fatalf("legacy checkpoint was not imported: %#v, %t", got, ok)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("imported legacy JSON remained: %v", err)
	}
}

func TestCalibrationCheckpointRejectsInvalidLegacyJSON(t *testing.T) {
	store := calibrationCheckpointStore(t)
	authorityPath := store.AuthorityPath()
	legacyPath := authorityPath + ".calibration.checkpoint"
	content := `{"schema_version":1,"identity":"sha256:checkpoint","scenarios":{},"unexpected":true}`
	if err := os.WriteFile(legacyPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCalibrationCheckpoint(store, "sha256:checkpoint"); err == nil {
		t.Fatal("invalid legacy JSON was accepted")
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("invalid legacy JSON was removed: %v", err)
	}
}

func TestCalibrationCheckpointReopenAndCachedRetryPersist(t *testing.T) {
	store := calibrationCheckpointStore(t)
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint")
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
	if _, _, ok := checkpoint.Completed("full"); ok {
		t.Fatal("reopened checkpoint remained complete")
	}
	if err := checkpoint.Observe("full", input, testCalibrationCheckpointResult(input), true); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := checkpoint.Completed("full"); !ok {
		t.Fatal("cached retry did not restore completed checkpoint")
	}
}

func TestCalibrationCheckpointDoesNotPersistExecutionPayload(t *testing.T) {
	store := calibrationCheckpointStore(t)
	authorityPath := store.AuthorityPath()
	checkpoint, err := NewCalibrationCheckpoint(store, "sha256:checkpoint")
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

func TestCalibrationCheckpointRejectsMissingReceiptBinding(t *testing.T) {
	checkpoint, err := NewCalibrationCheckpoint(calibrationCheckpointStore(t), "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.CandidateTestBinaryReceiptBindingDigest = ""
	if err := checkpoint.Observe("commit", input, result, true); err == nil {
		t.Fatal("empty candidate test-binary receipt binding digest was accepted")
	}
}

func TestCalibrationCheckpointConcurrentDifferentScenariosAreRetained(t *testing.T) {
	firstStore, secondStore := calibrationCheckpointStores(t)
	first, err := NewCalibrationCheckpoint(firstStore, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCalibrationCheckpoint(secondStore, "sha256:checkpoint")
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
	record, found, err := firstStore.LoadCalibrationCheckpoint("sha256:checkpoint")
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
			errs <- store.CompareAndSwapCalibrationCheckpointScenario("sha256:checkpoint", 1, nil, next)
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
	return store
}

func calibrationCheckpointStores(t *testing.T) (*gatecontract.DurationLedgerStore, *gatecontract.DurationLedgerStore) {
	t.Helper()
	authorityPath := calibrationCheckpointAuthorityPath(t)
	first, err := gatecontract.NewDurationLedgerStore(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gatecontract.NewDurationLedgerStore(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	return first, second
}

func testCalibrationCheckpointInput() RunInput {
	tree := strings.Repeat("1", 40)
	return RunInput{Tree: tree, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindTree, Tree: &gatecontract.TreeSource{SHA: tree, ParentCommitSHA: strings.Repeat("2", 40)}, SourceTreeSHA: tree}, Profile: gatecontract.ProfileLocalFast, Entrypoint: gatecontract.CIEntrypointGitPreCommit, Platform: "linux/amd64", ToolchainDigest: "sha256:" + strings.Repeat("3", 64), Calibration: true, RunnerIdentityDigest: "sha256:" + strings.Repeat("4", 64), RunnerImage: "ubuntu:22.04"}
}

func testCalibrationCheckpointResult(input RunInput) RunResult {
	binding, err := CandidateTestBinaryReceiptBindingDigestFromBuilds(nil, input.Tree)
	if err != nil {
		panic(err)
	}
	return RunResult{JobID: "job-checkpoint", Entrypoint: input.Entrypoint, Profile: input.Profile, PlanDigest: "sha256:" + strings.Repeat("5", 64), CatalogDigest: "sha256:" + strings.Repeat("6", 64), SourceTreeSHA: input.Tree, CandidateCLIManifestSHA256: strings.Repeat("c", 64), CandidateTestBinaryReceiptBindingDigest: binding, Status: gatecontract.ResultStatusPassed, Authoritative: true, CleanupComplete: true, CompletedAt: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)}
}
