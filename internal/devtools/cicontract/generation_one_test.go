package cicontract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestGenerationOneProvisionReceiptRoundTrip 覆盖首代回执的 canonical 编解码。
func TestGenerationOneProvisionReceiptRoundTrip(t *testing.T) {
	receipt := testGenerationOneReceipt(t)
	encoded, digest, err := EncodeGenerationOneProvisionReceipt(receipt)
	if err != nil {
		t.Fatalf("encode generation-one receipt: %v", err)
	}
	if receipt.ReceiptSHA256 != "" || digest == "" {
		t.Fatalf("encoder must return computed digest without mutating input: receipt=%q digest=%q", receipt.ReceiptSHA256, digest)
	}
	decoded, err := DecodeGenerationOneProvisionReceipt(encoded)
	if err != nil {
		t.Fatalf("decode generation-one receipt: %v", err)
	}
	if decoded.ReceiptSHA256 != digest || decoded.StateSHA256 != receipt.StateSHA256 {
		t.Fatalf("decoded receipt digest mismatch: %#v", decoded)
	}
}

// TestGenerationOneProvisionReceiptRejectsStrictJSON 覆盖未知字段、缺字段和重复 JSON 值。
func TestGenerationOneProvisionReceiptRejectsStrictJSON(t *testing.T) {
	receipt := testGenerationOneReceipt(t)
	encoded, _, err := EncodeGenerationOneProvisionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	assertGenerationOneDecodeError(t, unknown)
	delete(object, "unexpected")
	delete(object, "image_cache_status")
	missing, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	assertGenerationOneDecodeError(t, missing)
	object["image_cache_status"] = "Ready"
	delete(object, "provision_checks")
	missingChecks, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	assertGenerationOneDecodeError(t, missingChecks)
	assertGenerationOneDecodeError(t, append(append([]byte(nil), encoded...), []byte("{}")...))
}

// TestGenerationOneProvisionReceiptRejectsMutableImage 覆盖 tag 镜像和重复 ImageCache image。
func TestGenerationOneProvisionReceiptRejectsMutableImage(t *testing.T) {
	receipt := testGenerationOneReceipt(t)
	receipt.Image = "registry.example/runtime:latest"
	if _, _, err := EncodeGenerationOneProvisionReceipt(receipt); err == nil {
		t.Fatal("mutable runtime image must be rejected")
	}
	receipt = testGenerationOneReceipt(t)
	receipt.ImageCacheImages = []string{receipt.Image, receipt.Image}
	if _, _, err := EncodeGenerationOneProvisionReceipt(receipt); err == nil {
		t.Fatal("duplicate ImageCache images must be rejected")
	}
}

// TestGenerationOneProvisionReceiptRejectsNonECIExecution 确保首代内容证据只能来自阿里云 ECI。
func TestGenerationOneProvisionReceiptRejectsNonECIExecution(t *testing.T) {
	for name, mutate := range map[string]func(*GenerationOneProvisionReceipt){
		"provider": func(receipt *GenerationOneProvisionReceipt) { receipt.ExecutionProvider = "docker/v1" },
		"region":   func(receipt *GenerationOneProvisionReceipt) { receipt.RegionID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			receipt := testGenerationOneReceipt(t)
			mutate(&receipt)
			if _, _, err := EncodeGenerationOneProvisionReceipt(receipt); err == nil {
				t.Fatalf("generation-one receipt accepted non-ECI %s", name)
			}
		})
	}
}

// TestGenerationOneProvisionReceiptRejectsMissingProvisionChecks 确保首代回执不接受未绑定内容检查的证据。
func TestGenerationOneProvisionReceiptRejectsMissingProvisionChecks(t *testing.T) {
	receipt := testGenerationOneReceipt(t)
	receipt.ProvisionChecks = nil
	if _, _, err := EncodeGenerationOneProvisionReceipt(receipt); err == nil {
		t.Fatal("generation-one receipt without provision checks must be rejected")
	}
}

// TestGenerationOneProvisionReceiptBindsProvisionCheckContents 确保每个内容检查与首代 receipt 身份及摘要绑定。
func TestGenerationOneProvisionReceiptBindsProvisionCheckContents(t *testing.T) {
	for name, mutate := range map[string]func(*ProvisionCheckObservation){
		"provider":          func(check *ProvisionCheckObservation) { check.ExecutionProvider = "docker/v1" },
		"region":            func(check *ProvisionCheckObservation) { check.RegionID = "cn-hangzhou" },
		"container group":   func(check *ProvisionCheckObservation) { check.ContainerGroupID = "" },
		"empty ECI suffix":  func(check *ProvisionCheckObservation) { check.ContainerGroupID = "eci-" },
		"container name":    func(check *ProvisionCheckObservation) { check.ContainerName = "" },
		"source tree":       func(check *ProvisionCheckObservation) { check.SourceTree = strings.Repeat("d", 40) },
		"snapshot":          func(check *ProvisionCheckObservation) { check.ProvisionSnapshotID = "wrong-snapshot" },
		"candidate compile": func(check *ProvisionCheckObservation) { check.CandidateCompileNotApplicable = true },
		"test body":         func(check *ProvisionCheckObservation) { check.TestBodyNotApplicable = false },
		"receipt digest":    func(check *ProvisionCheckObservation) { check.ReceiptSHA256 = "sha256:" + strings.Repeat("f", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			receipt := testGenerationOneReceipt(t)
			mutate(&receipt.ProvisionChecks[0])
			if _, _, err := EncodeGenerationOneProvisionReceipt(receipt); err == nil {
				t.Fatalf("generation-one receipt accepted unbound provision check %s", name)
			}
		})
	}
}

func TestGenerationOneProvisionReceiptBindsNormalProvisionResources(t *testing.T) {
	for name, mutate := range map[string]func(*ProvisionCheckObservation){
		"missing class": func(check *ProvisionCheckObservation) { check.ResourceClassID = "" },
		"zero CPU":      func(check *ProvisionCheckObservation) { check.ResourceCPU = 0 },
		"invalid pair":  func(check *ProvisionCheckObservation) { check.ResourceMemoryGiB = 16 },
	} {
		t.Run(name, func(t *testing.T) {
			receipt := testGenerationOneReceipt(t)
			mutate(&receipt.ProvisionChecks[0])
			digest, err := ProvisionCheckObservationReceiptDigest(receipt.ProvisionChecks[0])
			if err != nil {
				t.Fatal(err)
			}
			receipt.ProvisionChecks[0].ReceiptSHA256 = digest
			if _, _, err := EncodeGenerationOneProvisionReceipt(receipt); err == nil {
				t.Fatalf("generation-one receipt accepted invalid provision resources %s", name)
			}
		})
	}
}

// TestGenerationOneProvisionReceiptRequiresDependencyCompileNA 确保依赖检查不伪造 candidate compile 耗时。
func TestGenerationOneProvisionReceiptRequiresDependencyCompileNA(t *testing.T) {
	receipt := testGenerationOneReceipt(t)
	for index := range receipt.ProvisionChecks {
		check := &receipt.ProvisionChecks[index]
		if check.Check != ProvisionCheckDependency {
			continue
		}
		check.CandidateCompileNotApplicable = false
		check.CandidateCompileMS = 1
		check.ReceiptSHA256, _ = ProvisionCheckObservationReceiptDigest(*check)
		if _, _, err := EncodeGenerationOneProvisionReceipt(receipt); err == nil {
			t.Fatal("dependency check with fabricated candidate compile timing must be rejected")
		}
		return
	}
	t.Fatal("dependency provision check is missing")
}

// TestGenerationOneProvisionReceiptRejectsZeroDurationAndSharedGroup 锁定真实 ECI 运行和一组一检查语义。
func TestGenerationOneProvisionReceiptRejectsZeroDurationAndSharedGroup(t *testing.T) {
	for name, mutate := range map[string]func(*GenerationOneProvisionReceipt){
		"zero duration": func(receipt *GenerationOneProvisionReceipt) {
			receipt.ProvisionChecks[0].CompletedAtUnixMS = receipt.ProvisionChecks[0].StartedAtUnixMS
			receipt.ProvisionChecks[0].DurationMS = 0
			receipt.ProvisionChecks[0].ReceiptSHA256, _ = ProvisionCheckObservationReceiptDigest(receipt.ProvisionChecks[0])
		},
		"shared group": func(receipt *GenerationOneProvisionReceipt) {
			receipt.ProvisionChecks[1].ContainerGroupID = receipt.ProvisionChecks[0].ContainerGroupID
			receipt.ProvisionChecks[1].ReceiptSHA256, _ = ProvisionCheckObservationReceiptDigest(receipt.ProvisionChecks[1])
		},
	} {
		t.Run(name, func(t *testing.T) {
			receipt := testGenerationOneReceipt(t)
			mutate(&receipt)
			if _, _, err := EncodeGenerationOneProvisionReceipt(receipt); err == nil {
				t.Fatalf("generation-one receipt accepted %s", name)
			}
		})
	}
}

// assertGenerationOneDecodeError 统一断言 strict decoder 失败。
func assertGenerationOneDecodeError(t *testing.T, data []byte) {
	t.Helper()
	if _, err := DecodeGenerationOneProvisionReceipt(data); err == nil {
		t.Fatal("strict receipt decoder unexpectedly accepted invalid JSON")
	}
}

// testGenerationOneReceipt 构造不含 secret 的最小合法首代回执。
func testGenerationOneReceipt(t *testing.T) GenerationOneProvisionReceipt {
	t.Helper()
	image := "ac2-registry.cn-hangzhou.cr.aliyuncs.com/ac2/base@sha256:" + strings.Repeat("a", 64)
	state := json.RawMessage(`{"state":"external-eci"}`)
	stateDigest := sha256.Sum256(state)
	digest := func(letter string) string { return "sha256:" + strings.Repeat(letter, 64) }
	return GenerationOneProvisionReceipt{
		SchemaVersion: GenerationOneProvisionReceiptSchemaVersion, Authority: GenerationOneProvisionAuthority,
		ExecutionProvider: ExecutionProviderID, RegionID: "cn-shenzhen", Generation: 1,
		StateJSON: state, StateSHA256: fmt.Sprintf("sha256:%x", stateDigest), ImageCacheID: "imc-generation-one",
		ImageCacheSnapshotID: "snapshot-generation-one", ImageCacheName: "generation-one", ImageCacheStatus: "Ready",
		Image: image, ImageCacheImages: []string{image}, MainCommit: strings.Repeat("b", 40), MainTree: strings.Repeat("c", 40),
		Platform: TargetPlatform, PolicyDigest: digest("d"), ToolchainDigest: digest("e"), RuntimeImage: image,
		GateBinarySHA256: digest("f"), RuntimeSeedSHA256: digest("1"), BaselineManifestDigest: digest("2"),
		CalibrationClassID: "calibration", CalibrationCPU: 4, CalibrationMemoryGiB: 8,
		ProvisionChecks: testGenerationOneProvisionChecks(t, strings.Repeat("c", 40), "snapshot-generation-one"),
	}
}

func testGenerationOneProvisionChecks(t *testing.T, sourceTree, snapshotID string) []ProvisionCheckObservation {
	t.Helper()
	checks := make([]ProvisionCheckObservation, 0, len(RequiredProvisionChecks()))
	for index, check := range RequiredProvisionChecks() {
		startedAt := int64(1_000 + index*100)
		resourceClassID, resourceCPU, resourceMemoryGiB := testGenerationOneResource(index)
		observation := ProvisionCheckObservation{
			Check: check, ExecutionProvider: ExecutionProviderID, RegionID: "cn-shenzhen", ContainerGroupID: fmt.Sprintf("eci-generation-one-%d", index), ContainerName: "provision-check",
			ResourceClassID: resourceClassID, ResourceCPU: resourceCPU, ResourceMemoryGiB: resourceMemoryGiB,
			Executed: true, Passed: true, SourceTree: sourceTree, ProvisionSnapshotID: snapshotID,
			PlanDigest: "sha256:" + strings.Repeat(string(rune('a'+index)), 64), StartedAtUnixMS: startedAt,
			CompletedAtUnixMS: startedAt + 50, DurationMS: 50, CandidateCompileMS: 25, TestBodyNotApplicable: true,
		}
		if check == ProvisionCheckDependency {
			observation.CandidateCompileMS = 0
			observation.CandidateCompileNotApplicable = true
		}
		digest, err := ProvisionCheckObservationReceiptDigest(observation)
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
