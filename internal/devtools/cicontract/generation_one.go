package cicontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	// GenerationOneProvisionReceiptSchemaVersion is the only receipt schema for
	// normal run/hook 消费的 configured strict generation-one receipt。
	GenerationOneProvisionReceiptSchemaVersion uint32 = 5
	// GenerationOneProvisionAuthority 区分 external cloud evidence 与仓库的
	// normal run/hook bootstrap consumer（仓库侧消费者）。
	GenerationOneProvisionAuthority = "external-aliyun-eci-imagecache-generation-one/v1"
)

// GenerationOneProvisionReceipt is the strict, secret-free proof emitted by
// external ECI operator 生成并由 remote run config 携带。内嵌 state 是 protocol data；
// receipt 消费后 SQLite 仍是唯一的 accepted-baseline authority。
type GenerationOneProvisionReceipt struct {
	SchemaVersion          uint32                      `json:"schema_version"`
	Authority              string                      `json:"authority"`
	ExecutionProvider      string                      `json:"execution_provider"`
	RegionID               string                      `json:"region_id"`
	Generation             uint64                      `json:"generation"`
	StateJSON              json.RawMessage             `json:"state_json"`
	StateSHA256            string                      `json:"state_sha256"`
	ImageCacheID           string                      `json:"image_cache_id"`
	ImageCacheSnapshotID   string                      `json:"image_cache_snapshot_id"`
	ImageCacheName         string                      `json:"image_cache_name"`
	ImageCacheStatus       string                      `json:"image_cache_status"`
	Image                  string                      `json:"image"`
	ImageCacheImages       []string                    `json:"image_cache_images"`
	MainCommit             string                      `json:"main_commit"`
	MainTree               string                      `json:"main_tree"`
	Platform               string                      `json:"platform"`
	PolicyDigest           string                      `json:"policy_digest"`
	ToolchainDigest        string                      `json:"toolchain_digest"`
	RuntimeImage           string                      `json:"runtime_image"`
	GateBinarySHA256       string                      `json:"gate_binary_sha256"`
	RuntimeSeedSHA256      string                      `json:"runtime_seed_manifest_sha256"`
	BaselineManifestDigest string                      `json:"baseline_manifest_digest"`
	CalibrationClassID     string                      `json:"calibration_class_id"`
	CalibrationCPU         float64                     `json:"calibration_cpu"`
	CalibrationMemoryGiB   float64                     `json:"calibration_memory_gib"`
	ProvisionChecks        []ProvisionCheckObservation `json:"provision_checks"`
	ReceiptSHA256          string                      `json:"receipt_sha256"`
}

// Validate 拒绝过期、可变、不完整或非 ECI 首代证据。
func (receipt GenerationOneProvisionReceipt) Validate() error {
	if err := validateGenerationOneProvisionHeader(receipt); err != nil {
		return err
	}
	if err := validateGenerationOneProvisionStateDigest(receipt); err != nil {
		return err
	}
	if err := validateGenerationOneProvisionImageCache(receipt); err != nil {
		return err
	}
	if err := validateGenerationOneProvisionIdentity(receipt); err != nil {
		return err
	}
	if err := ValidateGenerationOneProvisionChecks(receipt); err != nil {
		return err
	}
	if err := ValidateCalibrationResources(receipt.CalibrationClassID, receipt.CalibrationCPU, receipt.CalibrationMemoryGiB); err != nil {
		return fmt.Errorf("generation-one provision calibration resources: %w", err)
	}
	want, err := GenerationOneProvisionReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptSHA256 != want {
		return errors.New("generation-one provision receipt digest does not match payload")
	}
	return nil
}

// RequiredProvisionChecks 返回外部首代供给必须实际通过的内容检查目录。
func RequiredProvisionChecks() []ProvisionCheck {
	return []ProvisionCheck{
		ProvisionCheckGateBuild,
		ProvisionCheckNormalCompile,
		ProvisionCheckE2ECompile,
		ProvisionCheckRaceCompile,
		ProvisionCheckFrontendBuild,
		ProvisionCheckDependency,
	}
}

// ValidateGenerationOneProvisionChecks 校验首代回执的完整内容检查及其与 receipt 身份的绑定。
func ValidateGenerationOneProvisionChecks(receipt GenerationOneProvisionReceipt) error {
	required := RequiredProvisionChecks()
	if len(receipt.ProvisionChecks) != len(required) {
		return fmt.Errorf("generation-one provision checks = %d, want %d", len(receipt.ProvisionChecks), len(required))
	}
	seen, err := validateUniqueGenerationOneProvisionChecks(receipt)
	if err != nil {
		return err
	}
	return validateCompleteGenerationOneProvisionChecks(required, seen)
}

// validateUniqueGenerationOneProvisionChecks 校验每项检查和容器组身份均唯一。
func validateUniqueGenerationOneProvisionChecks(receipt GenerationOneProvisionReceipt) (map[ProvisionCheck]struct{}, error) {
	seen := make(map[ProvisionCheck]struct{}, len(receipt.ProvisionChecks))
	seenGroups := make(map[string]struct{}, len(receipt.ProvisionChecks))
	for _, observation := range receipt.ProvisionChecks {
		if err := validateGenerationOneProvisionCheckObservation(receipt, observation); err != nil {
			return nil, err
		}
		if _, duplicate := seen[observation.Check]; duplicate {
			return nil, fmt.Errorf("generation-one provision check %q is duplicated", observation.Check)
		}
		seen[observation.Check] = struct{}{}
		if _, duplicate := seenGroups[observation.ContainerGroupID]; duplicate {
			return nil, fmt.Errorf("generation-one ECI container group %q is reused by multiple checks", observation.ContainerGroupID)
		}
		seenGroups[observation.ContainerGroupID] = struct{}{}
	}
	return seen, nil
}

// validateCompleteGenerationOneProvisionChecks 确认规范目录没有缺项。
func validateCompleteGenerationOneProvisionChecks(required []ProvisionCheck, seen map[ProvisionCheck]struct{}) error {
	for _, check := range required {
		if _, found := seen[check]; !found {
			return fmt.Errorf("generation-one provision check %q is missing", check)
		}
	}
	return nil
}

func validateGenerationOneProvisionCheckObservation(receipt GenerationOneProvisionReceipt, observation ProvisionCheckObservation) error {
	if !isRequiredProvisionCheck(observation.Check) || !observation.Executed || !observation.Passed {
		return fmt.Errorf("generation-one provision check %q did not execute and pass", observation.Check)
	}
	if err := validateGenerationOneProvisionCheckIdentity(receipt, observation); err != nil {
		return err
	}
	if err := ValidateNormalResources(observation.ResourceCPU, observation.ResourceMemoryGiB); err != nil {
		return fmt.Errorf("generation-one provision check %q resources: %w", observation.Check, err)
	}
	if observation.ResourceClassID == "" || observation.ResourceClassID != strings.TrimSpace(observation.ResourceClassID) {
		return fmt.Errorf("generation-one provision check %q resource class is required", observation.Check)
	}
	if err := validateGenerationOneProvisionCheckTiming(observation); err != nil {
		return err
	}
	if err := validateGenerationOneProvisionCheckContent(observation); err != nil {
		return err
	}
	return validateGenerationOneProvisionCheckReceiptDigest(observation)
}

func validateGenerationOneProvisionCheckIdentity(receipt GenerationOneProvisionReceipt, observation ProvisionCheckObservation) error {
	if err := validateGenerationOneProvisionCheckECIIdentity(receipt, observation); err != nil {
		return err
	}
	if observation.SourceTree != receipt.MainTree || observation.ProvisionSnapshotID != receipt.ImageCacheSnapshotID || !isCanonicalSHA256(observation.PlanDigest) {
		return fmt.Errorf("generation-one provision check %q identity is not bound to receipt", observation.Check)
	}
	return nil
}

// validateGenerationOneProvisionCheckECIIdentity 校验 provider、region、group 和 container 身份。
func validateGenerationOneProvisionCheckECIIdentity(receipt GenerationOneProvisionReceipt, observation ProvisionCheckObservation) error {
	if observation.ExecutionProvider != ExecutionProviderID || observation.ExecutionProvider != receipt.ExecutionProvider ||
		observation.RegionID == "" || observation.RegionID != receipt.RegionID ||
		observation.ContainerGroupID != strings.TrimSpace(observation.ContainerGroupID) || len(observation.ContainerGroupID) <= len("eci-") || !strings.HasPrefix(observation.ContainerGroupID, "eci-") ||
		observation.ContainerName == "" || observation.ContainerName != strings.TrimSpace(observation.ContainerName) {
		return fmt.Errorf("generation-one provision check %q identity is not bound to receipt", observation.Check)
	}
	return nil
}

func validateGenerationOneProvisionCheckTiming(observation ProvisionCheckObservation) error {
	if observation.StartedAtUnixMS <= 0 || observation.CompletedAtUnixMS <= observation.StartedAtUnixMS || observation.DurationMS <= 0 || observation.DurationMS != observation.CompletedAtUnixMS-observation.StartedAtUnixMS {
		return fmt.Errorf("generation-one provision check %q timing is invalid", observation.Check)
	}
	return nil
}

func validateGenerationOneProvisionCheckContent(observation ProvisionCheckObservation) error {
	if !observation.TestBodyNotApplicable {
		return fmt.Errorf("generation-one provision check %q content observations are incomplete", observation.Check)
	}
	compileNotApplicable := observation.Check == ProvisionCheckDependency
	if observation.CandidateCompileNotApplicable != compileNotApplicable ||
		compileNotApplicable && observation.CandidateCompileMS != 0 ||
		!compileNotApplicable && observation.CandidateCompileMS <= 0 {
		return fmt.Errorf("generation-one provision check %q candidate compile observation is invalid", observation.Check)
	}
	return nil
}

func validateGenerationOneProvisionCheckReceiptDigest(observation ProvisionCheckObservation) error {
	if !isCanonicalSHA256(observation.ReceiptSHA256) {
		return fmt.Errorf("generation-one provision check %q receipt digest is invalid", observation.Check)
	}
	want, err := ProvisionCheckObservationReceiptDigest(observation)
	if err != nil {
		return err
	}
	if observation.ReceiptSHA256 != want {
		return fmt.Errorf("generation-one provision check %q receipt digest does not match payload", observation.Check)
	}
	return nil
}

func isRequiredProvisionCheck(check ProvisionCheck) bool {
	return slices.Contains(RequiredProvisionChecks(), check)
}

// ProvisionCheckObservationReceiptDigest 计算首代内容检查回执的 canonical SHA-256。
func ProvisionCheckObservationReceiptDigest(observation ProvisionCheckObservation) (string, error) {
	observation.ReceiptSHA256 = ""
	payload, err := json.Marshal(observation)
	if err != nil {
		return "", fmt.Errorf("marshal generation-one provision check observation: %w", err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(payload)), nil
}

// validateGenerationOneProvisionHeader 校验首代回执 schema、authority、代次和 digest 格式。
func validateGenerationOneProvisionHeader(receipt GenerationOneProvisionReceipt) error {
	if receipt.SchemaVersion != GenerationOneProvisionReceiptSchemaVersion || receipt.Authority != GenerationOneProvisionAuthority || receipt.Generation != 1 {
		return errors.New("generation-one provision receipt schema, authority, or generation is invalid")
	}
	if len(receipt.StateJSON) == 0 || !isCanonicalSHA256(receipt.StateSHA256) || !isCanonicalSHA256(receipt.ReceiptSHA256) {
		return errors.New("generation-one provision receipt state or receipt digest is invalid")
	}
	if receipt.ExecutionProvider != ExecutionProviderID || strings.TrimSpace(receipt.RegionID) == "" || receipt.RegionID != strings.TrimSpace(receipt.RegionID) {
		return errors.New("generation-one provision receipt must be executed by Alibaba Cloud ECI in one explicit region")
	}
	return nil
}

// validateGenerationOneProvisionStateDigest 校验 state_json 的 sha256 绑定。
func validateGenerationOneProvisionStateDigest(receipt GenerationOneProvisionReceipt) error {
	if got := fmt.Sprintf("sha256:%x", sha256.Sum256(receipt.StateJSON)); got != receipt.StateSHA256 {
		return errors.New("generation-one provision state digest does not match state_json")
	}
	return nil
}

// validateGenerationOneProvisionImageCache 校验 receipt 中 Ready cache 的完整身份。
func validateGenerationOneProvisionImageCache(receipt GenerationOneProvisionReceipt) error {
	if err := validateGenerationOneImageCacheHeader(receipt); err != nil {
		return err
	}
	return validateGenerationOneImageList(receipt.ImageCacheImages, receipt.Image)
}

// validateGenerationOneImageCacheHeader 校验 cache ID、snapshot、状态和 runtime image。
func validateGenerationOneImageCacheHeader(receipt GenerationOneProvisionReceipt) error {
	if strings.TrimSpace(receipt.ImageCacheID) == "" || strings.TrimSpace(receipt.ImageCacheSnapshotID) == "" || strings.TrimSpace(receipt.ImageCacheName) == "" {
		return errors.New("generation-one provision ECI ImageCache identity is invalid")
	}
	if receipt.ImageCacheStatus != "Ready" || !validGenerationOneOCIImage(receipt.Image) || receipt.Image != receipt.RuntimeImage || len(receipt.ImageCacheImages) == 0 {
		return errors.New("generation-one provision ECI ImageCache identity is invalid")
	}
	return nil
}

// validateGenerationOneImageList 校验镜像列表均 immutable、唯一且包含 runtime image。
func validateGenerationOneImageList(images []string, expected string) error {
	if len(images) == 0 {
		return errors.New("generation-one provision ImageCache image list is empty")
	}
	seen := make(map[string]struct{}, len(images))
	found := false
	for _, image := range images {
		if !validGenerationOneOCIImage(image) {
			return errors.New("generation-one provision ImageCache contains a mutable or forbidden image")
		}
		if _, duplicate := seen[image]; duplicate {
			return errors.New("generation-one provision ImageCache contains duplicate images")
		}
		seen[image] = struct{}{}
		found = found || image == expected
	}
	if !found {
		return errors.New("generation-one provision ImageCache does not contain the runtime image")
	}
	return nil
}

// validateGenerationOneProvisionIdentity 校验源码、平台和所有策略/工具链摘要。
func validateGenerationOneProvisionIdentity(receipt GenerationOneProvisionReceipt) error {
	if !baselineGenerationOneOID(receipt.MainCommit) || !baselineGenerationOneOID(receipt.MainTree) || receipt.Platform != TargetPlatform {
		return errors.New("generation-one provision source identity is invalid")
	}
	for _, digest := range []string{receipt.PolicyDigest, receipt.ToolchainDigest, receipt.GateBinarySHA256, receipt.RuntimeSeedSHA256, receipt.BaselineManifestDigest} {
		if !isCanonicalSHA256(digest) {
			return errors.New("generation-one provision source digest is invalid")
		}
	}
	return nil
}

// baselineGenerationOneOID 校验 commit/tree 使用小写十六进制 Git identity。
func baselineGenerationOneOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

// validGenerationOneOCIImage 校验 ECI 返回的 immutable OCI digest 身份。
func validGenerationOneOCIImage(value string) bool {
	repository, digest, ok := strings.Cut(value, "@")
	if !ok || !validGenerationOneOCIRepository(repository) || !isCanonicalSHA256(digest) {
		return false
	}
	return true
}

// validGenerationOneOCIRepository 校验 OCI repository 不含协议、查询或 tag。
func validGenerationOneOCIRepository(repository string) bool {
	if repository == "" || repository != strings.ToLower(repository) || strings.ContainsAny(repository, " @\t\r\n\\?#") || strings.Contains(repository, "://") {
		return false
	}
	last := repository
	if slash := strings.LastIndexByte(repository, '/'); slash >= 0 {
		last = repository[slash+1:]
	}
	return last != "" && !strings.Contains(last, ":")
}

// GenerationOneProvisionReceiptDigest computes the canonical receipt SHA-256
// while excluding the receipt's own digest field.
// GenerationOneProvisionReceiptDigest 计算不包含自身摘要字段的 canonical SHA-256。
func GenerationOneProvisionReceiptDigest(receipt GenerationOneProvisionReceipt) (string, error) {
	receipt.ReceiptSHA256 = ""
	payload, err := json.Marshal(receipt)
	if err != nil {
		return "", fmt.Errorf("marshal generation-one provision receipt: %w", err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(payload)), nil
}

// EncodeGenerationOneProvisionReceipt 校验并输出 strict canonical JSON。
func EncodeGenerationOneProvisionReceipt(receipt GenerationOneProvisionReceipt) ([]byte, string, error) {
	if receipt.ReceiptSHA256 == "" {
		digest, err := GenerationOneProvisionReceiptDigest(receipt)
		if err != nil {
			return nil, "", err
		}
		receipt.ReceiptSHA256 = digest
	}
	if err := receipt.Validate(); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return nil, "", fmt.Errorf("marshal generation-one provision receipt: %w", err)
	}
	return payload, receipt.ReceiptSHA256, nil
}

// DecodeGenerationOneProvisionReceipt 严格解码并校验一份首代回执。
func DecodeGenerationOneProvisionReceipt(data []byte) (GenerationOneProvisionReceipt, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt GenerationOneProvisionReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return GenerationOneProvisionReceipt{}, fmt.Errorf("decode generation-one provision receipt: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return GenerationOneProvisionReceipt{}, errors.New("generation-one provision receipt contains multiple JSON values")
		}
		return GenerationOneProvisionReceipt{}, fmt.Errorf("decode generation-one provision receipt trailer: %w", err)
	}
	if err := receipt.Validate(); err != nil {
		return GenerationOneProvisionReceipt{}, err
	}
	return receipt, nil
}
