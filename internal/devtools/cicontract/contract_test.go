package cicontract

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptedContract(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	if got := len(requirements); got < 9 {
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

func TestArchtestCompileGroupBudgetIsBoundedPerECIShard(t *testing.T) {
	if CompileGroupMaxSelectors != 64 {
		t.Fatalf("CompileGroupMaxSelectors = %d, want 64", CompileGroupMaxSelectors)
	}
	if ArchtestMaxSelectorsPerCompileGroup != CompileGroupMaxSelectors {
		t.Fatalf("legacy archtest selector alias = %d, want generic owner %d", ArchtestMaxSelectorsPerCompileGroup, CompileGroupMaxSelectors)
	}
}

func TestClassifyWorkloadResourceDurationUsesFrozenNormalTiers(t *testing.T) {
	tests := []struct {
		durationMS int64
		want       WorkloadResourceTier
	}{
		{durationMS: 1, want: WorkloadResourceTierFast},
		{durationMS: FastWorkloadResourceDuration.Milliseconds(), want: WorkloadResourceTierFast},
		{durationMS: FastWorkloadResourceDuration.Milliseconds() + 1, want: WorkloadResourceTierMedium},
		{durationMS: MediumWorkloadResourceDuration.Milliseconds(), want: WorkloadResourceTierMedium},
		{durationMS: MediumWorkloadResourceDuration.Milliseconds() + 1, want: WorkloadResourceTierSlow},
	}
	for _, test := range tests {
		got, err := ClassifyWorkloadResourceDuration(test.durationMS)
		if err != nil {
			t.Fatalf("ClassifyWorkloadResourceDuration(%d) error = %v", test.durationMS, err)
		}
		if got != test.want {
			t.Fatalf("ClassifyWorkloadResourceDuration(%d) = %d, want %d", test.durationMS, got, test.want)
		}
	}
	if _, err := ClassifyWorkloadResourceDuration(0); err == nil {
		t.Fatal("ClassifyWorkloadResourceDuration(0) error = nil")
	}
}

func TestGenerationOneBootstrapIsTheOnlyWritePathIdentity(t *testing.T) {
	if GenerationOneBootstrapPathID != "normal-run-hook-configured-aliyun-eci-generation-one-strict-receipt-bootstrap/v1" {
		t.Fatalf("GenerationOneBootstrapPathID = %q", GenerationOneBootstrapPathID)
	}
}

func TestImageCacheRefreshOperatorProducesRuntimeMaterialOnly(t *testing.T) {
	if ImageCacheRefreshOperatorPathID != "script-oss-handoff-aliyun-eci-offline-imagecache-runtime/v2" {
		t.Fatalf("ImageCacheRefreshOperatorPathID = %q", ImageCacheRefreshOperatorPathID)
	}
	if ImageCacheRefreshInterval != 24*time.Hour {
		t.Fatalf("ImageCacheRefreshInterval = %s", ImageCacheRefreshInterval)
	}
}

func TestCIExecutionBoundaryIsAlibabaECIOnly(t *testing.T) {
	if CIExecutionBoundary != "aliyun-eci-only-no-github-runner/v1" {
		t.Fatalf("CIExecutionBoundary = %q", CIExecutionBoundary)
	}
}

func TestOCICacheMaterialCannotClaimECIAuthority(t *testing.T) {
	if CacheMaterialSchemaID != "remote-ci-cache-material/v1" {
		t.Fatalf("CacheMaterialSchemaID = %q", CacheMaterialSchemaID)
	}
	if CacheMaterialAuthority != "non_authoritative_material" {
		t.Fatalf("CacheMaterialAuthority = %q", CacheMaterialAuthority)
	}
	if strings.Contains(CacheMaterialSchemaID, "generation-one") || strings.Contains(CacheMaterialSchemaID, "receipt") || strings.Contains(CacheMaterialSchemaID, "check") {
		t.Fatalf("cache material schema must not resemble authoritative evidence: %q", CacheMaterialSchemaID)
	}
}

func TestAcceptedBaselineProjectionIsAlibabaECIOnly(t *testing.T) {
	if err := ValidateAcceptedBaselineProjection(BaselineStateSchemaVersion, ExecutionProviderID, "cn-shenzhen"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		schema   uint32
		provider string
		region   string
	}{
		{schema: BaselineStateSchemaVersion - 1, provider: ExecutionProviderID, region: "cn-shenzhen"},
		{schema: BaselineStateSchemaVersion, provider: "docker/v1", region: "cn-shenzhen"},
		{schema: BaselineStateSchemaVersion, provider: ExecutionProviderID, region: ""},
	} {
		if err := ValidateAcceptedBaselineProjection(test.schema, test.provider, test.region); err == nil {
			t.Fatalf("projection unexpectedly accepted: %+v", test)
		}
	}
}

func TestIncrementalSourceTransportContractIsFrozen(t *testing.T) {
	if err := ValidateSourceTransportContract(); err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"baseline repository": SourceBaselineRepositoryPath,
		"bundle":              SourceBundleName,
		"manifest":            SourceManifestName,
		"ref":                 SourceBundleRef,
		"base ref":            SourceBaseRef,
		"transport kind":      SourceTransportKind,
		"header":              SourceBundleHeaderVersion,
		"upload barrier":      SourceAssetsUploadBarrier,
	}
	want := map[string]string{
		"baseline repository": "/opt/super-dolphin-gate/source-baseline.git",
		"bundle":              "source.bundle",
		"manifest":            "source-manifest.json",
		"ref":                 "refs/source/materialized",
		"base ref":            "refs/source/base",
		"transport kind":      "git-bundle-thin",
		"header":              "v2",
		"upload barrier":      "source-bundle-and-strict-manifest-before-lpt-shards/v1",
	}
	for name, got := range checks {
		if got != want[name] {
			t.Errorf("%s = %q, want %q", name, got, want[name])
		}
	}
	if SourceManifestSchemaVersion != 3 || SourceBundlePrerequisiteCount != 1 {
		t.Fatalf("source transport schema/prerequisite = %d/%d, want 3/1", SourceManifestSchemaVersion, SourceBundlePrerequisiteCount)
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
		{name: "execution provider", err: ValidateExecutionProvider("docker/v1")},
		{name: "platform", err: ValidateTargetPlatform("linux/arm64")},
		{name: "toolchain", err: ValidateGoToolchainVersion("go1.25.7")},
		{name: "shard target", err: ValidateShardTargetDuration(99 * time.Second)},
		{name: "calibration class", err: ValidateCalibrationResources("", 4, 16)},
		{name: "calibration CPU", err: ValidateCalibrationResources("fixed", 0, 16)},
		{name: "calibration memory", err: ValidateCalibrationResources("fixed", 4, 0)},
		{name: "normal CPU", err: ValidateNormalResources(0, 4)},
		{name: "normal memory", err: ValidateNormalResources(4, 0)},
		{name: "normal pair", err: ValidateNormalResources(4, 16)},
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

// TestRequiredChecksObservedPassForScopedCatalog 锁定较小 profile 只接受其目录范围，
// 同时拒绝空、重复或乱序范围。
func TestRequiredChecksObservedPassForScopedCatalog(t *testing.T) {
	required := []RequiredCheck{RequiredCheckGate, RequiredCheckNormal, RequiredCheckFrontend}
	observations := make([]CheckObservation, 0, len(required))
	for _, check := range required {
		observation := CheckObservation{Check: check, Executed: true, Passed: true, SourceTree: "target-tree", AcceptedSnapshotID: "accepted-snapshot", PlanDigest: "plan-digest", StartedAtUnixMS: 1, CompletedAtUnixMS: 2, DurationMS: 1}
		digest, err := CheckObservationReceiptDigest(observation)
		if err != nil {
			t.Fatal(err)
		}
		observation.ReceiptSHA256 = digest
		observations = append(observations, observation)
	}
	if err := ValidateRequiredChecksObservedPassFor(required, observations); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]RequiredCheck{nil, {RequiredCheckNormal, RequiredCheckGate}, {RequiredCheckGate, RequiredCheckGate}} {
		if err := ValidateRequiredChecksObservedPassFor(invalid, observations); err == nil {
			t.Fatalf("invalid required check scope %v was accepted", invalid)
		}
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

func TestCompileGroupBatchEnvironmentContractLocksTempRoot(t *testing.T) {
	var compileGroup Requirement
	for _, requirement := range requirements {
		if requirement.ID == "5.4" {
			compileGroup = requirement
			break
		}
	}
	if compileGroup.ID == "" {
		t.Fatal("compile-group requirement 5.4 is missing")
	}
	for _, required := range []string{
		"worker supervisor",
		"TMPDIR",
		"temp-data",
		"/tmp",
		"镜像默认环境",
		"唯一短 0700",
		"GOTMPDIR",
		"结束时清理",
		"长 lane/batchRoot",
	} {
		if !strings.Contains(compileGroup.Summary, required) {
			t.Fatalf("compile-group environment contract is missing %q: %s", required, compileGroup.Summary)
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
	assertDocumentBlock(t, document, "scheduling", "<!-- cicontract:scheduling:begin -->", "<!-- cicontract:scheduling:end -->", CanonicalSchedulingMarkdown())
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

func TestSQLAuthoritySchemaTablesRejectUnregisteredExtraTable(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("validate canonical SQL schema table registry: %v", err)
	}
	if slices.Contains(SQLAuthoritySchemaTables(), "ci_unregistered_second_authority") {
		t.Fatal("unregistered SQLite schema table was accepted")
	}
}

func TestRetentionRootsBindExactlyThreeAcceptedGenerations(t *testing.T) {
	if err := ValidateRetentionGenerations(); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		DurationSamplesTable:              true,
		DurationShardOverheadsTable:       true,
		DurationShardOverheadSamplesTable: true,
		CatalogObservationsTable:          true,
		RemoteRunsTable:                   true,
		WorkloadPassEvidenceTable:         true,
		CalibrationCheckpointsTable:       true,
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

// TestGenerationOneAuthoritySupportingTablesAreCanonical 锁定首代 schema-only 边界的
// 非历史根表，并确保它们不会悄然加入三代 retention 根集合。
func TestGenerationOneAuthoritySupportingTablesAreCanonical(t *testing.T) {
	want := map[string]bool{
		DurationCalibrationsTable: true,
		WorkloadCatalogsTable:     true,
		LiveTimingWarningsTable:   true,
	}
	for _, table := range GenerationOneAuthoritySupportingTables() {
		if !want[table] {
			t.Fatalf("unexpected generation-one supporting table %q", table)
		}
		delete(want, table)
		for _, binding := range RetentionRootBindings() {
			if binding.Table == table {
				t.Fatalf("generation-one supporting table %q became a retention root", table)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing generation-one supporting tables: %v", want)
	}
}

func TestECIEphemeralStorageIsFixedAtOneHundredGiB(t *testing.T) {
	if ECIEphemeralStorageGiB != 100 {
		t.Fatalf("ECIEphemeralStorageGiB = %d, want 100", ECIEphemeralStorageGiB)
	}
}
