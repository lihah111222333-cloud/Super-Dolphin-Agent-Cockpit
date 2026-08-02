package cicontract

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptedContract(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	if got := len(Requirements()); got < 9 {
		t.Fatalf("requirement count = %d, want at least one per section", got)
	}
}

func TestRefreshTransitionMatrix(t *testing.T) {
	phases := []RefreshPhase{RefreshIdle, RefreshClaimed, RefreshBuilding, RefreshCachePreparing, RefreshReadyValidated, RefreshPromoted, RefreshRetiring, RefreshCleanupPending, RefreshUnchanged, RefreshFailed}
	allowed := map[[2]RefreshPhase]bool{
		{RefreshIdle, RefreshClaimed}:                  true,
		{RefreshClaimed, RefreshUnchanged}:             true,
		{RefreshClaimed, RefreshBuilding}:              true,
		{RefreshBuilding, RefreshCachePreparing}:       true,
		{RefreshCachePreparing, RefreshReadyValidated}: true,
		{RefreshReadyValidated, RefreshPromoted}:       true,
		{RefreshPromoted, RefreshRetiring}:             true,
		{RefreshRetiring, RefreshIdle}:                 true,
		{RefreshRetiring, RefreshCleanupPending}:       true,
		{RefreshCleanupPending, RefreshRetiring}:       true,
		{RefreshCleanupPending, RefreshIdle}:           true,
		{RefreshClaimed, RefreshFailed}:                true,
		{RefreshBuilding, RefreshFailed}:               true,
		{RefreshCachePreparing, RefreshFailed}:         true,
		{RefreshReadyValidated, RefreshFailed}:         true,
	}
	for _, current := range phases {
		for _, next := range phases {
			got := ValidateRefreshTransition(current, next) == nil
			if got != allowed[[2]RefreshPhase{current, next}] {
				t.Errorf("transition %q -> %q allowed = %t, want %t", current, next, got, allowed[[2]RefreshPhase{current, next}])
			}
		}
	}
}

func TestContractValidatorsRejectDrift(t *testing.T) {
	checks := []struct {
		name string
		err  error
	}{
		{name: "platform", err: ValidateTargetPlatform("linux/arm64")},
		{name: "toolchain", err: ValidateGoToolchainVersion("go1.25.7")},
		{name: "shard target", err: ValidateShardTargetDuration(99 * time.Second)},
		{name: "refresh interval", err: ValidateRefreshMinimumInterval(time.Hour)},
		{name: "calibration class", err: ValidateCalibrationResources("", 4, 16)},
		{name: "calibration CPU", err: ValidateCalibrationResources("fixed", 0, 16)},
		{name: "calibration memory", err: ValidateCalibrationResources("fixed", 4, 0)},
		{name: "snapshot root", err: ValidateSourceSnapshotLayout("/tmp/source", SourceSnapshotManifestPath)},
		{name: "snapshot manifest", err: ValidateSourceSnapshotLayout(SourceSnapshotRootPath, "/tmp/manifest.json")},
		{name: "refresh check log prefix", err: ValidateRefreshCheckLogPrefix("REMOTE_CI_CHECK_PASS=")},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if check.err == nil {
				t.Fatal("expected drift to be rejected")
			}
		})
	}
}

func TestIncrementalRefreshTransferMatrix(t *testing.T) {
	cases := []struct {
		name               string
		mode               RefreshTransferMode
		acceptedGeneration uint64
		acceptedSnapshotID string
		deltaIdentity      string
		wantErr            bool
	}{
		{name: "accepted snapshot delta", mode: RefreshTransferAcceptedSnapshotDelta, acceptedGeneration: 7, acceptedSnapshotID: "snapshot-7", deltaIdentity: "delta-8"},
		{name: "full workspace mode", mode: "full_workspace", acceptedGeneration: 7, acceptedSnapshotID: "snapshot-7", deltaIdentity: "delta-8", wantErr: true},
		{name: "missing accepted generation", mode: RefreshTransferAcceptedSnapshotDelta, acceptedSnapshotID: "snapshot-7", deltaIdentity: "delta-8", wantErr: true},
		{name: "missing accepted snapshot", mode: RefreshTransferAcceptedSnapshotDelta, acceptedGeneration: 7, deltaIdentity: "delta-8", wantErr: true},
		{name: "missing delta identity", mode: RefreshTransferAcceptedSnapshotDelta, acceptedGeneration: 7, acceptedSnapshotID: "snapshot-7", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateIncrementalRefreshTransfer(testCase.mode, testCase.acceptedGeneration, testCase.acceptedSnapshotID, testCase.deltaIdentity)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("ValidateIncrementalRefreshTransfer() error = %v, wantErr %t", err, testCase.wantErr)
			}
		})
	}
}

func TestDeltaRebuildRequiresCompleteTreeAndClosureEvidence(t *testing.T) {
	if err := ValidateDeltaRebuild(RefreshTransferAcceptedSnapshotDelta, 7, "snapshot-7", "delta-8", "tree-8", "closure-8"); err != nil {
		t.Fatalf("ValidateDeltaRebuild() error = %v", err)
	}
	for name, target := range map[string]string{"missing tree": "", "missing closure": "tree-8"} {
		t.Run(name, func(t *testing.T) {
			closure := "closure-8"
			if name == "missing closure" {
				closure = ""
			}
			if err := ValidateDeltaRebuild(RefreshTransferAcceptedSnapshotDelta, 7, "snapshot-7", "delta-8", target, closure); err == nil {
				t.Fatal("expected missing rebuild evidence to be rejected")
			}
		})
	}
}

func TestRequiredChecksObservedPassMatrix(t *testing.T) {
	observations := make([]CheckObservation, 0, len(RequiredChecks()))
	for _, check := range RequiredChecks() {
		observation := CheckObservation{Check: check, Executed: true, Passed: true, SourceTree: "target-tree", AcceptedSnapshotID: "accepted-snapshot", PlanDigest: "plan-digest", StartedAtUnixMS: 1, CompletedAtUnixMS: 2, DurationMS: 1}
		digest, err := CheckObservationReceiptDigest(observation)
		if err != nil {
			t.Fatal(err)
		}
		observation.ReceiptSHA256 = digest
		observations = append(observations, observation)
	}
	if err := ValidateRequiredChecksObservedPass(observations); err != nil {
		t.Fatalf("ValidateRequiredChecksObservedPass() error = %v", err)
	}
	for _, mutate := range []func([]CheckObservation){
		func(values []CheckObservation) { values[0].Passed = false },
		func(values []CheckObservation) { values[len(values)-1] = values[0] },
	} {
		candidate := append([]CheckObservation(nil), observations...)
		mutate(candidate)
		if err := ValidateRequiredChecksObservedPass(candidate); err == nil {
			t.Fatal("expected incomplete required check observations to be rejected")
		}
	}
	if err := ValidateRequiredChecksObservedPass(observations[:len(observations)-1]); err == nil {
		t.Fatal("expected missing required check observation to be rejected")
	}
}

func TestTimingWarningActionIsNonTerminating(t *testing.T) {
	if err := ValidateTimingWarningAction(TimingWarningWarnAndContinue); err != nil {
		t.Fatalf("ValidateTimingWarningAction() error = %v", err)
	}
	for _, action := range []TimingWarningAction{"cancel", "kill", "fail"} {
		if err := ValidateTimingWarningAction(action); err == nil {
			t.Fatalf("ValidateTimingWarningAction(%q) accepted terminating action", action)
		}
	}
}

func TestValidateNonACRRegistryHost(t *testing.T) {
	for name, reference := range map[string]string{
		"official registry":    "registry-1.docker.io/library/alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"no explicit registry": "library/alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateNonACRRegistryHost(reference); err != nil {
				t.Fatalf("ValidateNonACRRegistryHost(%q) error = %v", reference, err)
			}
		})
	}
	for name, reference := range map[string]string{
		"root":         "aliyuncs.com/repository/image",
		"subdomain":    "registry.cn-shenzhen.aliyuncs.com/repository/image",
		"port":         "registry.cn-shenzhen.aliyuncs.com:5000/repository/image",
		"uppercase":    "REGISTRY.CN-SHENZHEN.ALIYUNCS.COM/repository/image",
		"trailing dot": "registry.cn-shenzhen.aliyuncs.com./repository/image",
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateNonACRRegistryHost(reference); err == nil {
				t.Fatalf("ValidateNonACRRegistryHost(%q) accepted Alibaba Cloud ACR host", reference)
			}
		})
	}
}

func TestCanonicalMarkdownCoversEverySection(t *testing.T) {
	markdown := CanonicalMarkdown()
	if !strings.HasPrefix(markdown, "<!-- cicontract:begin -->\n") || !strings.HasSuffix(markdown, "<!-- cicontract:end -->") {
		t.Fatal("canonical markdown markers are invalid")
	}
	for section := 1; section <= 9; section++ {
		if !strings.Contains(markdown, "| §"+string(rune('0'+section))+" |") {
			t.Fatalf("canonical markdown is missing section %d", section)
		}
	}
}

func TestAcceptedDocumentMatchesCodeContract(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve code contract source path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(DocumentPath)))
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	const begin, end = "<!-- cicontract:begin -->", "<!-- cicontract:end -->"
	start := strings.Index(document, begin)
	if start < 0 {
		t.Fatal("accepted document is missing the code contract begin marker")
	}
	relativeEnd := strings.Index(document[start:], end)
	if relativeEnd < 0 {
		t.Fatal("accepted document is missing the code contract end marker")
	}
	finish := start + relativeEnd + len(end)
	if got := document[start:finish]; got != CanonicalMarkdown() {
		t.Fatal("accepted document and code contract differ")
	}
	const retentionBegin, retentionEnd = "<!-- cicontract:retention:begin -->", "<!-- cicontract:retention:end -->"
	retentionStart := strings.Index(document, retentionBegin)
	if retentionStart < 0 {
		t.Fatal("accepted document is missing the retention begin marker")
	}
	retentionRelativeEnd := strings.Index(document[retentionStart:], retentionEnd)
	if retentionRelativeEnd < 0 {
		t.Fatal("accepted document is missing the retention end marker")
	}
	retentionFinish := retentionStart + retentionRelativeEnd + len(retentionEnd)
	if got := document[retentionStart:retentionFinish]; got != CanonicalRetentionMarkdown() {
		t.Fatal("accepted retention prose and code contract differ")
	}
	const timingBegin, timingEnd = "<!-- cicontract:timing:begin -->", "<!-- cicontract:timing:end -->"
	timingStart := strings.Index(document, timingBegin)
	if timingStart < 0 {
		t.Fatal("accepted document is missing the timing begin marker")
	}
	timingRelativeEnd := strings.Index(document[timingStart:], timingEnd)
	if timingRelativeEnd < 0 {
		t.Fatal("accepted document is missing the timing end marker")
	}
	timingFinish := timingStart + timingRelativeEnd + len(timingEnd)
	if got := document[timingStart:timingFinish]; got != CanonicalTimingMarkdown() {
		t.Fatal("accepted timing prose and code contract differ")
	}
}

func TestReturnedContractSlicesCannotMutateOwner(t *testing.T) {
	rules := Requirements()
	rules[0].ID = "mutated"
	if Requirements()[0].ID == "mutated" {
		t.Fatal("requirements returned a mutable owner slice")
	}
	legacy := ForbiddenLegacyCapabilities()
	legacy[0] = "mutated"
	if ForbiddenLegacyCapabilities()[0] == "mutated" {
		t.Fatal("forbidden capabilities returned a mutable owner slice")
	}
	bindings := SQLAuthorityBindings()
	bindings[0].Table = "mutated"
	if SQLAuthorityBindings()[0].Table == "mutated" {
		t.Fatal("SQL authority bindings returned a mutable owner slice")
	}
	retentionRoots := RetentionRootBindings()
	retentionRoots[0].Table = "mutated"
	if RetentionRootBindings()[0].Table == "mutated" {
		t.Fatal("retention root bindings returned a mutable owner slice")
	}
}

func TestSQLAuthorityBindingsRejectSecondTruthSource(t *testing.T) {
	for _, binding := range SQLAuthorityBindings() {
		if err := ValidateSQLAuthorityBinding(binding.Domain, binding.Table); err != nil {
			t.Fatalf("validate canonical SQL authority binding %+v: %v", binding, err)
		}
		if err := ValidateSQLAuthorityBinding(binding.Domain, binding.Table+"_json_mirror"); err == nil {
			t.Fatalf("SQL domain %q accepted a second truth source", binding.Domain)
		}
	}
	if err := ValidateSQLAuthorityBinding("json_baseline", "baseline.json"); err == nil {
		t.Fatal("unknown non-SQL truth source was accepted")
	}
}

func TestRetentionRootsBindExactlyThreeAcceptedGenerations(t *testing.T) {
	if err := ValidateRetentionGenerations(); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		DurationSamplesTable:        true,
		CatalogObservationsTable:    true,
		RemoteRunsTable:             true,
		CalibrationCheckpointsTable: true,
	}
	for _, binding := range RetentionRootBindings() {
		if !want[binding.Table] {
			t.Fatalf("unexpected retention root %q", binding.Table)
		}
		if binding.GenerationColumn != AcceptedGenerationColumn {
			t.Fatalf("retention root %q generation column = %q, want %q", binding.Table, binding.GenerationColumn, AcceptedGenerationColumn)
		}
		delete(want, binding.Table)
	}
	if len(want) != 0 {
		t.Fatalf("missing retention roots: %v", want)
	}
}
