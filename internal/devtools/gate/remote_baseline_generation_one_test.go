package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/sync/errgroup"
)

// TestInitializeRemoteBaselineGenerationOneRejectsDuplicate 覆盖空表首写和重复初始化门禁。
func TestInitializeRemoteBaselineGenerationOneRejectsDuplicate(t *testing.T) {
	store := newGenerationOneTestStore(t)
	receipt := generationOneGateReceipt(t)
	first, err := store.InitializeRemoteBaselineGenerationOne(receipt)
	if err != nil {
		t.Fatalf("initialize first generation-one state: %v", err)
	}
	if first.Generation != 1 || first.StateSHA256 == "" {
		t.Fatalf("unexpected first record: %#v", first)
	}
	if _, err := store.InitializeRemoteBaselineGenerationOne(receipt); !errors.Is(err, ErrRemoteBaselineGenerationOneAlreadyInitialized) {
		t.Fatalf("duplicate initialize error = %v, want ErrRemoteBaselineGenerationOneAlreadyInitialized", err)
	}
	loaded, err := store.LoadRemoteBaselineState()
	if err != nil {
		t.Fatalf("load initialized state: %v", err)
	}
	if loaded.Generation != 1 || loaded.StateSHA256 != first.StateSHA256 {
		t.Fatalf("loaded state drifted: %#v", loaded)
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
}

// TestInitializeRemoteBaselineGenerationOneConcurrentWinner 确认并发首写只有一个成功者。
func TestInitializeRemoteBaselineGenerationOneConcurrentWinner(t *testing.T) {
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
	var success, duplicate int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrRemoteBaselineGenerationOneAlreadyInitialized):
			duplicate++
		default:
			t.Fatalf("concurrent initializer returned unexpected error: %v", err)
		}
	}
	if success != 1 || duplicate != 1 {
		t.Fatalf("concurrent initializer results: success=%d duplicate=%d", success, duplicate)
	}
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
	state := map[string]any{
		"schema_version": cicontract.GenerationOneBaselineStateSchemaVersion, "generation": 1, "main_commit": strings.Repeat("b", 40), "main_tree": strings.Repeat("c", 40),
		"platform": cicontract.TargetPlatform, "policy_digest": digest("d"), "toolchain_digest": digest("e"), "runtime_image": image,
		"image_cache_id": "imc-generation-one", "image_cache_snapshot_id": "snapshot-generation-one", "image_cache_ready": true,
		"image_digest": digest("a"), "gate_binary_sha256": digest("f"), "runtime_seed_manifest_sha256": digest("1"),
		"baseline_manifest_digest": digest("2"),
		"oci_project_cache":        map[string]any{"image": image, "content_manifest_sha256": digest("5"), "main_tree": strings.Repeat("c", 40), "toolchain_digest": digest("e"), "platform": cicontract.TargetPlatform, "cache_path": "/opt/super-dolphin/cache/go-build"},
		"created_at":               "2026-01-01T00:00:00Z", "accepted_at": "2026-01-01T00:01:00Z", "renewed_at": "2026-01-01T00:02:00Z",
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	stateDigest := sha256.Sum256(stateJSON)
	receipt := cicontract.GenerationOneProvisionReceipt{
		SchemaVersion: cicontract.GenerationOneProvisionReceiptSchemaVersion, Authority: cicontract.GenerationOneProvisionAuthority, Generation: 1,
		StateJSON: stateJSON, StateSHA256: fmt.Sprintf("sha256:%x", stateDigest), ImageCacheID: "imc-generation-one",
		ImageCacheSnapshotID: "snapshot-generation-one", ImageCacheName: "generation-one", ImageCacheStatus: "Ready", Image: image,
		ImageCacheImages: []string{image}, MainCommit: strings.Repeat("b", 40), MainTree: strings.Repeat("c", 40), Platform: cicontract.TargetPlatform,
		PolicyDigest: digest("d"), ToolchainDigest: digest("e"), RuntimeImage: image, GateBinarySHA256: digest("f"), RuntimeSeedSHA256: digest("1"),
		BaselineManifestDigest: digest("2"), CalibrationClassID: "fixed", CalibrationCPU: 2, CalibrationMemoryGiB: 4,
		ProvisionChecks: generationOneProvisionChecks(t, strings.Repeat("c", 40), "snapshot-generation-one"),
	}
	encoded, _, err := cicontract.EncodeGenerationOneProvisionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func generationOneProvisionChecks(t *testing.T, sourceTree, snapshotID string) []cicontract.ProvisionCheckObservation {
	t.Helper()
	checks := make([]cicontract.ProvisionCheckObservation, 0, len(cicontract.RequiredProvisionChecks()))
	for index, check := range cicontract.RequiredProvisionChecks() {
		startedAt := int64(1_000 + index*100)
		observation := cicontract.ProvisionCheckObservation{
			Check: check, Executed: true, Passed: true, SourceTree: sourceTree, ProvisionSnapshotID: snapshotID,
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
