package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/sync/errgroup"
)

// TestInitializeRemoteBaselineGenerationOneConvergesOnIdenticalState 覆盖空表首写和同态重复初始化。
func TestInitializeRemoteBaselineGenerationOneConvergesOnIdenticalState(t *testing.T) {
	store := newGenerationOneTestStore(t)
	receipt := generationOneGateReceipt(t)
	first, err := store.InitializeRemoteBaselineGenerationOne(receipt)
	if err != nil {
		t.Fatalf("initialize first generation-one state: %v", err)
	}
	if first.Generation != 1 || first.StateSHA256 == "" {
		t.Fatalf("unexpected first record: %#v", first)
	}
	second, err := store.InitializeRemoteBaselineGenerationOne(receipt)
	if err != nil {
		t.Fatalf("identical duplicate initialize error = %v, want idempotent success", err)
	}
	assertGenerationOneRecordsEqual(t, first, second, "identical duplicate")
	assertGenerationOneInitializedAuthority(t, store, first)
	if third, err := store.InitializeRemoteBaselineGenerationOne(receipt); err != nil {
		t.Fatalf("identical duplicate after retained history error = %v", err)
	} else {
		assertGenerationOneRecordsEqual(t, first, third, "identical duplicate after retained history")
	}
}

// assertGenerationOneRecordsEqual 验证幂等初始化返回完全相同的 accepted state。
func assertGenerationOneRecordsEqual(t *testing.T, want, got RemoteBaselineStateRecord, operation string) {
	t.Helper()
	if got.Generation != want.Generation || got.StateSHA256 != want.StateSHA256 || string(got.StateJSON) != string(want.StateJSON) {
		t.Fatalf("%s returned drifted state: want=%#v got=%#v", operation, want, got)
	}
}

// TestInitializeRemoteBaselineGenerationOneRejectsDivergentState 保证已有 singleton 与异态回执不能静默收敛。
func TestInitializeRemoteBaselineGenerationOneRejectsDivergentState(t *testing.T) {
	store := newGenerationOneTestStore(t)
	receipt := generationOneGateReceipt(t)
	if _, err := store.InitializeRemoteBaselineGenerationOne(receipt); err != nil {
		t.Fatalf("initialize first generation-one state: %v", err)
	}
	divergent := divergentGenerationOneGateReceipt(t, receipt)
	if _, err := store.InitializeRemoteBaselineGenerationOne(divergent); !errors.Is(err, ErrRemoteBaselineGenerationOneAlreadyInitialized) {
		t.Fatalf("divergent initialize error = %v, want ErrRemoteBaselineGenerationOneAlreadyInitialized", err)
	}
}

func assertGenerationOneInitializedAuthority(t *testing.T, store *DurationLedgerStore, first RemoteBaselineStateRecord) {
	t.Helper()
	loaded, err := store.LoadRemoteBaselineState()
	if err != nil {
		t.Fatalf("load initialized state: %v", err)
	}
	if loaded.Generation != 1 || loaded.StateSHA256 != first.StateSHA256 {
		t.Fatalf("loaded state drifted: %#v", loaded)
	}
	metadata, err := store.LoadMetadata()
	if err != nil {
		t.Fatalf("load generation-one duration metadata: %v", err)
	}
	if metadata.Generation != 1 || metadata.Ledger.Version != durationLedgerVersion {
		t.Fatalf("generation-one duration metadata = %#v", metadata)
	}
	if _, err := store.AppendSamplesFast(1, []DurationSample{testDurationSample("generation-one", testWorkloadDigest, true, 1)}); err != nil {
		t.Fatalf("append first accepted-generation sample: %v", err)
	}
}

// TestInitializeRemoteBaselineGenerationOneRejectsStrictReceiptFailures 覆盖坏 SHA 和重复 JSON 值。
func TestInitializeRemoteBaselineGenerationOneRejectsStrictReceiptFailures(t *testing.T) {
	store := newGenerationOneTestStore(t)
	receipt := generationOneGateReceipt(t)
	var object map[string]any
	if err := json.Unmarshal(receipt, &object); err != nil {
		t.Fatal(err)
	}
	object["state_sha256"] = "sha256:" + strings.Repeat("f", 64)
	badDigest, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitializeRemoteBaselineGenerationOne(badDigest); err == nil {
		t.Fatal("bad state SHA must be rejected")
	}
	if _, err := store.InitializeRemoteBaselineGenerationOne(append(receipt, []byte("{}")...)); err == nil {
		t.Fatal("duplicate JSON values must be rejected")
	}
	if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("invalid receipt left accepted state: %v", err)
	}
	if _, err := store.LoadMetadata(); !errors.Is(err, ErrDurationLedgerMetadataMissing) {
		t.Fatalf("invalid receipt left duration metadata: %v", err)
	}
}

// TestInitializeRemoteBaselineGenerationOneRollsBackOnMetadataConflict 锁定 baseline 与账本元数据的原子首写。
func TestInitializeRemoteBaselineGenerationOneRollsBackOnMetadataConflict(t *testing.T) {
	store := newGenerationOneTestStore(t)
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO duration_ledger_meta(singleton,authority_id,schema_version,generation,ledger_version) VALUES(1,?,1,'2',?)`, cicontract.SQLAuthorityID, durationLedgerVersion); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitializeRemoteBaselineGenerationOne(generationOneGateReceipt(t)); err == nil {
		t.Fatal("generation-one initialization accepted conflicting duration metadata")
	}
	if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("metadata conflict left accepted state: %v", err)
	}
}

// TestInitializeRemoteBaselineGenerationOneRejectsOrphanHistory 锁定 schema-only 空库是唯一首代入口。
func TestInitializeRemoteBaselineGenerationOneRejectsOrphanHistory(t *testing.T) {
	store := newGenerationOneTestStore(t)
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms) VALUES ('1','orphan','orphan','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','linux/amd64','eci','go','normal','small',2,4,1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitializeRemoteBaselineGenerationOne(generationOneGateReceipt(t)); err == nil || !strings.Contains(err.Error(), "orphan history") {
		t.Fatalf("generation-one initialization orphan-history error = %v", err)
	}
	if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("orphan history left accepted state: %v", err)
	}
	if _, err := store.LoadMetadata(); !errors.Is(err, ErrDurationLedgerMetadataMissing) {
		t.Fatalf("orphan history left duration metadata: %v", err)
	}
}

// TestInitializeRemoteBaselineGenerationOneRejectsEveryAuthorityHistoryTable
// 回归首代检查不得漏掉任何 retention root 或 supporting table。
func TestInitializeRemoteBaselineGenerationOneRejectsEveryAuthorityHistoryTable(t *testing.T) {
	tables := make([]string, 0, len(cicontract.RetentionRootBindings())+len(cicontract.GenerationOneAuthoritySupportingTables()))
	for _, binding := range cicontract.RetentionRootBindings() {
		tables = append(tables, binding.Table)
	}
	tables = append(tables, cicontract.GenerationOneAuthoritySupportingTables()...)
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			store := newGenerationOneTestStore(t)
			database, err := store.openSQLiteAuthority(true)
			if err != nil {
				t.Fatal(err)
			}
			insertGenerationOneAuthorityHistoryForTest(t, database, table)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			expectedTable := table
			if table == cicontract.WorkloadPassEvidenceTable {
				// PASS evidence has a mandatory ci_runs origin FK; that parent is the
				// first canonical root rejected before the dependent row is reached.
				expectedTable = cicontract.RemoteRunsTable
			}
			if _, err := store.InitializeRemoteBaselineGenerationOne(generationOneGateReceipt(t)); err == nil || !strings.Contains(err.Error(), "orphan history in "+expectedTable) {
				t.Fatalf("generation-one initialization table %s error = %v", table, err)
			}
			if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
				t.Fatalf("%s pollution left accepted state: %v", table, err)
			}
			if _, err := store.LoadMetadata(); !errors.Is(err, ErrDurationLedgerMetadataMissing) {
				t.Fatalf("%s pollution left duration metadata: %v", table, err)
			}
		})
	}
}

// TestInitializeRemoteBaselineGenerationOneRejectsHistoryWithExistingMetadata
// 确保预存 generation=1 metadata 也不能绕过完整空历史证明。
func TestInitializeRemoteBaselineGenerationOneRejectsHistoryWithExistingMetadata(t *testing.T) {
	store := newGenerationOneTestStore(t)
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO duration_ledger_meta(singleton,authority_id,schema_version,generation,ledger_version) VALUES(1,?,1,'1',?)`, cicontract.SQLAuthorityID, durationLedgerVersion); err != nil {
		t.Fatal(err)
	}
	insertGenerationOneAuthorityHistoryForTest(t, database, cicontract.DurationShardOverheadsTable)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitializeRemoteBaselineGenerationOne(generationOneGateReceipt(t)); err == nil || !strings.Contains(err.Error(), "orphan history in "+cicontract.DurationShardOverheadsTable) {
		t.Fatalf("generation-one initialization with existing metadata error = %v", err)
	}
	if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("existing metadata pollution left accepted state: %v", err)
	}
}

// TestInitializeRemoteBaselineGenerationOneConcurrentConvergence 确认同态并发首写都收敛到同一 state。
func TestInitializeRemoteBaselineGenerationOneConcurrentConvergence(t *testing.T) {
	store := newGenerationOneTestStore(t)
	receipt := generationOneGateReceipt(t)
	results := make(chan error, 2)
	var group errgroup.Group
	for range 2 {
		group.Go(func() error {
			_, err := store.InitializeRemoteBaselineGenerationOne(receipt)
			results <- err
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent initializer runner: %v", err)
	}
	close(results)
	success, duplicate := countGenerationOneInitializationResults(t, results)
	if success != 2 || duplicate != 0 {
		t.Fatalf("concurrent initializer results: success=%d duplicate=%d", success, duplicate)
	}
}

func countGenerationOneInitializationResults(t *testing.T, results <-chan error) (int, int) {
	t.Helper()
	var success, duplicate int
	for err := range results {
		if err == nil {
			success++
			continue
		}
		if !errors.Is(err, ErrRemoteBaselineGenerationOneAlreadyInitialized) {
			t.Fatalf("concurrent initializer returned unexpected error: %v", err)
		}
		duplicate++
	}
	return success, duplicate
}

// newGenerationOneTestStore 创建一个独立的 SQLite authority。
func newGenerationOneTestStore(t *testing.T) *DurationLedgerStore {
	t.Helper()
	store, err := NewDurationLedgerStore(t.TempDir() + "/duration-ledger.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// generationOneGateReceipt 构造 gate projection 可消费的首代回执。
func generationOneGateReceipt(t *testing.T) []byte {
	t.Helper()
	image := "registry.example/runtime@sha256:" + strings.Repeat("a", 64)
	digest := func(letter string) string { return "sha256:" + strings.Repeat(letter, 64) }
	state := generationOneStateProjection{
		SchemaVersion: cicontract.BaselineStateSchemaVersion, Generation: 1, ExecutionProvider: cicontract.ExecutionProviderID, RegionID: "cn-shenzhen", MainCommit: strings.Repeat("b", 40), MainTree: strings.Repeat("c", 40),
		Platform: cicontract.TargetPlatform, PolicyDigest: digest("d"), ToolchainDigest: digest("e"), RuntimeImage: image,
		ImageCacheID: "imc-generation-one", ImageCacheSnapshotID: "snapshot-generation-one", ImageCacheReady: true, ImageDigest: digest("a"),
		OCIProjectCache:  &generationOneOCIProjectCache{Image: image, ContentManifestSHA256: digest("5"), MainTree: strings.Repeat("c", 40), ToolchainDigest: digest("e"), Platform: cicontract.TargetPlatform, CachePath: "/opt/super-dolphin/cache/go-build"},
		GateBinarySHA256: digest("f"), RuntimeSeedSHA256: digest("1"), BaselineManifestDigest: digest("2"),
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), AcceptedAt: time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC), RenewedAt: time.Date(2026, 1, 1, 0, 2, 0, 0, time.UTC),
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	stateDigest := sha256.Sum256(stateJSON)
	receipt := cicontract.GenerationOneProvisionReceipt{
		SchemaVersion: cicontract.GenerationOneProvisionReceiptSchemaVersion, Authority: cicontract.GenerationOneProvisionAuthority,
		ExecutionProvider: cicontract.ExecutionProviderID, RegionID: "cn-shenzhen", Generation: 1,
		StateJSON: stateJSON, StateSHA256: fmt.Sprintf("sha256:%x", stateDigest), ImageCacheID: "imc-generation-one",
		ImageCacheSnapshotID: "snapshot-generation-one", ImageCacheName: "generation-one", ImageCacheStatus: "Ready", Image: image,
		ImageCacheImages: []string{image}, MainCommit: strings.Repeat("b", 40), MainTree: strings.Repeat("c", 40), Platform: cicontract.TargetPlatform,
		PolicyDigest: digest("d"), ToolchainDigest: digest("e"), RuntimeImage: image, GateBinarySHA256: digest("f"), RuntimeSeedSHA256: digest("1"),
		BaselineManifestDigest: digest("2"), CalibrationClassID: "calibration", CalibrationCPU: 4, CalibrationMemoryGiB: 8,
		ProvisionChecks: generationOneProvisionChecks(t, strings.Repeat("c", 40), "snapshot-generation-one"),
	}
	encoded, _, err := cicontract.EncodeGenerationOneProvisionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func divergentGenerationOneGateReceipt(t *testing.T, encoded []byte) []byte {
	t.Helper()
	receipt, err := cicontract.DecodeGenerationOneProvisionReceipt(encoded)
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeGenerationOneStateProjection(receipt.StateJSON)
	if err != nil {
		t.Fatal(err)
	}
	state.PolicyDigest = "sha256:" + strings.Repeat("9", 64)
	receipt.PolicyDigest = state.PolicyDigest
	receipt.StateJSON, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	stateDigest := sha256.Sum256(receipt.StateJSON)
	receipt.StateSHA256 = fmt.Sprintf("sha256:%x", stateDigest)
	receipt.ReceiptSHA256 = ""
	divergent, _, err := cicontract.EncodeGenerationOneProvisionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return divergent
}

func generationOneProvisionChecks(t *testing.T, sourceTree, snapshotID string) []cicontract.ProvisionCheckObservation {
	t.Helper()
	checks := make([]cicontract.ProvisionCheckObservation, 0, len(cicontract.RequiredProvisionChecks()))
	for index, check := range cicontract.RequiredProvisionChecks() {
		startedAt := int64(1_000 + index*100)
		resourceClassID, resourceCPU, resourceMemoryGiB := testGenerationOneResource(index)
		observation := cicontract.ProvisionCheckObservation{
			Check: check, ExecutionProvider: cicontract.ExecutionProviderID, RegionID: "cn-shenzhen", ContainerGroupID: fmt.Sprintf("eci-generation-one-%d", index), ContainerName: "provision-check",
			ResourceClassID: resourceClassID, ResourceCPU: resourceCPU, ResourceMemoryGiB: resourceMemoryGiB,
			Executed: true, Passed: true, SourceTree: sourceTree, ProvisionSnapshotID: snapshotID,
			PlanDigest: "sha256:" + strings.Repeat(string(rune('a'+index)), 64), StartedAtUnixMS: startedAt,
			CompletedAtUnixMS: startedAt + 50, DurationMS: 50, CandidateCompileMS: 25, TestBodyNotApplicable: true,
		}
		if check == cicontract.ProvisionCheckDependency {
			observation.CandidateCompileMS = 0
			observation.CandidateCompileNotApplicable = true
		}
		digest, err := cicontract.ProvisionCheckObservationReceiptDigest(observation)
		if err != nil {
			t.Fatalf("digest provision check %q: %v", check, err)
		}
		observation.ReceiptSHA256 = digest
		checks = append(checks, observation)
	}
	return checks
}

func testGenerationOneResource(index int) (string, float64, float64) {
	resources := [...]struct {
		classID string
		cpu     float64
		memory  float64
	}{
		{classID: "small", cpu: 2, memory: 4},
		{classID: "medium", cpu: 4, memory: 8},
		{classID: "maximum", cpu: 8, memory: 16},
	}
	resource := resources[index%len(resources)]
	return resource.classID, resource.cpu, resource.memory
}
