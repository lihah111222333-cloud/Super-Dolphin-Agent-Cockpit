package remoteci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCalibrationCheckpointKeepsPartialScenarioIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.Observe("commit", RunInput{Calibration: true}, RunResult{}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, completed := checkpoint.Completed("commit"); completed {
		t.Fatal("result-free calibration failure was marked complete")
	}
	observed := RunResult{DurationSamples: []gatecontract.DurationSample{{DurationMS: 1}}}
	if err := checkpoint.Observe("commit", RunInput{Calibration: true}, observed, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, completed := loaded.Completed("commit"); completed {
		t.Fatal("observed partial calibration checkpoint was completed")
	}
}

func TestCalibrationCheckpointRestoresCompletedScenarioOnlyForSameIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("push", input, result, true); err != nil {
		t.Fatal(err)
	}
	gotInput, gotResult, ok := checkpoint.Completed("push")
	if !ok || !gotInput.Calibration || !gotResult.Authoritative || gotResult.CandidateCLIManifestSHA256 != result.CandidateCLIManifestSHA256 {
		t.Fatalf("completed checkpoint = %#v, %#v, %t", gotInput, gotResult, ok)
	}
	fresh, err := NewCalibrationCheckpoint(path, "sha256:another-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, completed := fresh.Completed("push"); completed {
		t.Fatal("identity-mismatched checkpoint reused an old completed scenario")
	}
}

func TestCalibrationCheckpointReopenPersistsIncompleteStartedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
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
	loaded, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, completed := loaded.Completed("full"); completed {
		t.Fatal("reopened calibration scenario remained completed")
	}
	if err := loaded.Observe("full", input, testCalibrationCheckpointResult(input), true); err != nil {
		t.Fatal(err)
	}
	if _, _, completed := loaded.Completed("full"); !completed {
		t.Fatal("reopened calibration scenario could not complete from a cached retry")
	}
}

func TestCalibrationCheckpointMarksCachedRetryComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	partial := RunResult{DurationSamples: []gatecontract.DurationSample{{DurationMS: 1}}}
	if err := checkpoint.Observe("full", input, partial, false); err != nil {
		t.Fatal(err)
	}
	completed := testCalibrationCheckpointResult(input)
	if err := checkpoint.Observe("full", input, completed, true); err != nil {
		t.Fatal(err)
	}
	if _, got, ok := checkpoint.Completed("full"); !ok || !got.Authoritative {
		t.Fatalf("cached retry completion = %#v, %t", got, ok)
	}
}

func TestCalibrationCheckpointRejectsCorruptedObservedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	content := `{"schema_version":5,"identity":"sha256:checkpoint","scenarios":{"full":{"started":true,"completed":true,"input":{"calibration":true},"result":{"authoritative":false}}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCalibrationCheckpoint(path, "sha256:checkpoint"); err == nil {
		t.Fatal("corrupted completed state was accepted")
	}
}

func TestCalibrationCheckpointRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	content := `{"schema_version":5,"identity":"sha256:checkpoint","scenarios":{},"unexpected":true}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCalibrationCheckpoint(path, "sha256:checkpoint"); err == nil {
		t.Fatal("unknown checkpoint field was accepted")
	}
}

func TestCalibrationCheckpointDoesNotPersistLedgerOrExecutionPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	samples := make([]gatecontract.DurationSample, 10_000)
	input := testCalibrationCheckpointInput()
	input.LedgerSnapshot = gatecontract.DurationLedgerSnapshot{
		Ledger: gatecontract.DurationLedger{Samples: samples},
	}
	result := testCalibrationCheckpointResult(input)
	result.DurationSamples = samples
	if err := checkpoint.Observe("full", input, result, true); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) >= 64<<10 {
		t.Fatalf("compact checkpoint bytes = %d, want < 64 KiB", len(content))
	}
	for _, forbidden := range []string{
		"LedgerSnapshot",
		"LedgerStore",
		"duration_samples",
		"gate_executions",
		"shards",
	} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("compact checkpoint retained %q", forbidden)
		}
	}
}

func TestCalibrationCheckpointResetsLargeLegacyPayloadToIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	largePayload := strings.Repeat("x", 2<<20)
	content := `{"schema_version":1,"identity":"sha256:checkpoint","scenarios":{"commit":{"started":true,"completed":true,"input":{"LedgerSnapshot":"` + largePayload + `"},"result":{"DurationSamples":"` + largePayload + `"}}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, completed := checkpoint.Completed("commit"); completed {
		t.Fatal("legacy completion was trusted without a compact authoritative replay")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= 1024 {
		t.Fatalf("reset checkpoint bytes = %d, want < 1 KiB", info.Size())
	}
}

func TestCalibrationCheckpointResetsSchemaV2CompletionToIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	content := `{"schema_version":2,"identity":"sha256:checkpoint","scenarios":{"commit":{"started":true,"completed":true,"input":{"calibration":true},"result":{"job_id":"job-old","authoritative":true,"completed_at":"2026-07-30T00:00:00Z"}}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, completed := checkpoint.Completed("commit"); completed {
		t.Fatal("schema v2 completion was trusted without SQLite run identity")
	}
}

func TestCalibrationCheckpointResetsSchemaV2IncompleteScenario(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	content := `{"schema_version":2,"identity":"sha256:checkpoint","scenarios":{"commit":{"started":true,"completed":false}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, completed := checkpoint.Completed("commit"); completed {
		t.Fatal("schema v2 incomplete scenario became completed")
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), `"schema_version":5`) {
		t.Fatalf("reset checkpoint = %s, want schema v5", persisted)
	}
}

func TestCalibrationCheckpointResetsEveryPreV5SchemaToIncomplete(t *testing.T) {
	for _, schemaVersion := range []string{"1", "2", "3", "4"} {
		t.Run("schema-v"+schemaVersion, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
			content := `{"schema_version":` + schemaVersion + `,"identity":"sha256:checkpoint","scenarios":{"commit":{"started":true,"completed":true,"input":{"calibration":true},"result":{"authoritative":true}}}}`
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, completed := checkpoint.Completed("commit"); completed {
				t.Fatalf("schema v%s completion was reused", schemaVersion)
			}
		})
	}
}

func TestCalibrationCheckpointRejectsEmptyCandidateTestBinaryReceiptBindingDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint")
	checkpoint, err := NewCalibrationCheckpoint(path, "sha256:checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	input := testCalibrationCheckpointInput()
	result := testCalibrationCheckpointResult(input)
	result.CandidateTestBinaryReceiptBindingDigest = ""
	if err := checkpoint.Observe("commit", input, result, true); err == nil {
		t.Fatal("empty candidate test-binary receipt binding digest was accepted")
	}
	validResult := testCalibrationCheckpointResult(input)
	validResult.DurationSamples = []gatecontract.DurationSample{{DurationMS: 1}}
	if err := checkpoint.Observe("commit", input, validResult, true); err != nil {
		t.Fatal(err)
	}
	state := checkpoint.document.Scenarios["commit"]
	state.Result.CandidateTestBinaryReceiptBindingDigest = ""
	checkpoint.document.Scenarios["commit"] = state
	if err := checkpoint.persist(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCalibrationCheckpoint(path, "sha256:checkpoint"); err == nil {
		t.Fatal("persisted empty candidate test-binary receipt binding digest was accepted")
	}
}

func testCalibrationCheckpointInput() RunInput {
	tree := strings.Repeat("1", 40)
	return RunInput{
		Tree: tree,
		Source: gatecontract.SourceSpec{
			Kind: gatecontract.SourceKindTree,
			Tree: &gatecontract.TreeSource{
				SHA: tree, ParentCommitSHA: strings.Repeat("2", 40),
			},
			SourceTreeSHA: tree,
		},
		Profile:              gatecontract.ProfileLocalFast,
		Entrypoint:           gatecontract.CIEntrypointGitPreCommit,
		Platform:             "linux/amd64",
		ToolchainDigest:      "sha256:" + strings.Repeat("3", 64),
		Calibration:          true,
		RunnerIdentityDigest: "sha256:" + strings.Repeat("4", 64),
		RunnerImage:          "ubuntu:22.04",
	}
}

func testCalibrationCheckpointResult(input RunInput) RunResult {
	binding, err := CandidateTestBinaryReceiptBindingDigestFromBuilds(nil, input.Tree)
	if err != nil {
		panic(err)
	}
	return RunResult{
		JobID:                                   "job-checkpoint",
		Entrypoint:                              input.Entrypoint,
		Profile:                                 input.Profile,
		PlanDigest:                              "sha256:" + strings.Repeat("5", 64),
		CatalogDigest:                           "sha256:" + strings.Repeat("6", 64),
		SourceTreeSHA:                           input.Tree,
		CandidateCLIManifestSHA256:              strings.Repeat("c", 64),
		CandidateTestBinaryReceiptBindingDigest: binding,
		Status:                                  gatecontract.ResultStatusPassed,
		Authoritative:                           true,
		CleanupComplete:                         true,
		CompletedAt:                             time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
	}
}
