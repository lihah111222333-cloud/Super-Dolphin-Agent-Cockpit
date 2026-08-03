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

func TestRemoteCIConcurrencyPolicyHasNoRepositorySerialBoundary(t *testing.T) {
	if err := ValidateConcurrencyPolicy(); err != nil {
		t.Fatal(err)
	}
	if GitHookInvocationConcurrencyPolicy != "unbounded_by_repository" || RemoteCIJobConcurrencyPolicy != "unbounded_by_repository" || ShardConcurrencyPolicy != "unbounded_by_repository" {
		t.Fatal("normal hook, job, and shard concurrency must remain unbounded")
	}
	if GitIndexLockBoundary != "git_worktree_index_consistency_not_ci_admission" {
		t.Fatal("Git index boundary drifted")
	}
}

func TestGenerationOneReceiptImportIsTheOnlyWritePathIdentity(t *testing.T) {
	if GenerationOneReceiptImportPathID != "external-eci-imagecache-generation-one-strict-receipt-import/v1" {
		t.Fatalf("GenerationOneReceiptImportPathID = %q", GenerationOneReceiptImportPathID)
	}
}

func TestWorkloadPassReuseSQLiteOwnershipAndRetention(t *testing.T) {
	if err := ValidateSQLAuthorityBinding(SQLDomainRunWorkloadResult, RunWorkloadResultsTable); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSQLAuthorityBinding(SQLDomainWorkloadPassEvidence, WorkloadPassEvidenceTable); err != nil {
		t.Fatal(err)
	}
	for _, binding := range RetentionRootBindings() {
		if binding.Table == WorkloadPassEvidenceTable && binding.GenerationColumn == AcceptedGenerationColumn {
			return
		}
	}
	t.Fatalf("%s must retain by %s", WorkloadPassEvidenceTable, AcceptedGenerationColumn)
}

func TestWorkloadPassEvidenceGenerationWindowIsCurrentAndPreviousTwo(t *testing.T) {
	if WorkloadPassEvidenceFreshnessPolicy != "accepted-generation-authority-identity-receipt-no-wall-clock-ttl/v1" {
		t.Fatalf("WorkloadPassEvidenceFreshnessPolicy = %q", WorkloadPassEvidenceFreshnessPolicy)
	}
	for _, generation := range []uint64{7, 6, 5} {
		if err := ValidateWorkloadPassEvidenceGeneration(7, generation); err != nil {
			t.Fatalf("generation %d unexpectedly rejected: %v", generation, err)
		}
	}
	for _, generation := range []uint64{0, 4, 8} {
		if err := ValidateWorkloadPassEvidenceGeneration(7, generation); err == nil {
			t.Fatalf("generation %d unexpectedly accepted", generation)
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
		{name: "calibration class", err: ValidateCalibrationResources("", 4, 16)},
		{name: "calibration CPU", err: ValidateCalibrationResources("fixed", 0, 16)},
		{name: "calibration memory", err: ValidateCalibrationResources("fixed", 4, 0)},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if check.err == nil {
				t.Fatal("expected drift to be rejected")
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

func TestRequiredChecksObservedPassAllowsOnlyCanonicalStrictReuse(t *testing.T) {
	observations := make([]CheckObservation, 0, len(RequiredChecks()))
	for index, check := range RequiredChecks() {
		observation := CheckObservation{Check: check, Passed: true, SourceTree: "target-tree", AcceptedSnapshotID: "accepted-snapshot", PlanDigest: "plan-digest", StartedAtUnixMS: 1, CompletedAtUnixMS: 2, DurationMS: 1}
		if index%2 == 0 {
			observation.Executed = true
		} else {
			observation.Reused = true
			observation.ReuseProofSHA256 = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		}
		digest, err := CheckObservationReceiptDigest(observation)
		if err != nil {
			t.Fatal(err)
		}
		observation.ReceiptSHA256 = digest
		observations = append(observations, observation)
	}
	if err := ValidateRequiredChecksObservedPass(observations); err != nil {
		t.Fatalf("mixed executed and reused observations rejected: %v", err)
	}

	for name, mutate := range map[string]func(*CheckObservation){
		"reuse requires proof":     func(observation *CheckObservation) { observation.ReuseProofSHA256 = "" },
		"reuse proof is canonical": func(observation *CheckObservation) { observation.ReuseProofSHA256 = "sha256:ABC" },
		"executed result has no proof": func(observation *CheckObservation) {
			observation.Reused = false
			observation.ReuseProofSHA256 = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := append([]CheckObservation(nil), observations...)
			mutate(&candidate[1])
			digest, err := CheckObservationReceiptDigest(candidate[1])
			if err != nil {
				t.Fatal(err)
			}
			candidate[1].ReceiptSHA256 = digest
			if err := ValidateRequiredChecksObservedPass(candidate); err == nil {
				t.Fatal("expected invalid reuse observation to be rejected")
			}
		})
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

func TestTimingWarningSQLiteLifecycleIsCanonicalAndNotASixthRetentionRoot(t *testing.T) {
	for domain, table := range map[SQLDomain]string{
		SQLDomainLiveTimingWarning: LiveTimingWarningsTable,
		SQLDomainRunTimingWarning:  RunTimingWarningsTable,
	} {
		if err := ValidateSQLAuthorityBinding(domain, table); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range RetentionRootBindings() {
		if binding.Table == LiveTimingWarningsTable || binding.Table == RunTimingWarningsTable {
			t.Fatalf("timing warning lifecycle table %q became a historical root", binding.Table)
		}
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
	assertDocumentBlock(t, document, "code contract", "<!-- cicontract:begin -->", "<!-- cicontract:end -->", CanonicalMarkdown())
	assertDocumentBlock(t, document, "retention", "<!-- cicontract:retention:begin -->", "<!-- cicontract:retention:end -->", CanonicalRetentionMarkdown())
	assertDocumentBlock(t, document, "timing", "<!-- cicontract:timing:begin -->", "<!-- cicontract:timing:end -->", CanonicalTimingMarkdown())
}

// assertDocumentBlock 校验 accepted 文档中的标记块完整且逐字等于代码 owner 输出。
func assertDocumentBlock(t *testing.T, document, name, begin, end, want string) {
	t.Helper()
	start := strings.Index(document, begin)
	if start < 0 {
		t.Fatalf("accepted document is missing the %s begin marker", name)
	}
	relativeEnd := strings.Index(document[start:], end)
	if relativeEnd < 0 {
		t.Fatalf("accepted document is missing the %s end marker", name)
	}
	finish := start + relativeEnd + len(end)
	if got := document[start:finish]; got != want {
		t.Fatalf("accepted %s prose and code contract differ", name)
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
		WorkloadPassEvidenceTable:   true,
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
