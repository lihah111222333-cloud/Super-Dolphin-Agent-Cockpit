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
	"time"
)

const (
	// AcceptedBaselineOCIProjectCachePath 是 accepted baseline 镜像内唯一的
	// immutable Go build cache 路径；所有协议消费者复用此 authority。
	AcceptedBaselineOCIProjectCachePath = "/opt/super-dolphin/cache/go-build"
	// GenerationOneProvisionReceiptSchemaVersion is the only receipt schema for
	// normal run/hook 消费的 configured strict generation-one receipt。
	GenerationOneProvisionReceiptSchemaVersion uint32 = 5
	// GenerationOneProvisionAuthority 区分 external cloud evidence 与仓库的
	// normal run/hook bootstrap consumer（仓库侧消费者）。
	GenerationOneProvisionAuthority = "external-aliyun-eci-imagecache-generation-one/v1"
)

// AcceptedBaselineState 是 accepted baseline 的 canonical JSON authority；SQLite
// 读取方直接依赖 cicontract，不反向导入任何 producer package。
type AcceptedBaselineState struct {
	SchemaVersion          uint32                           `json:"schema_version"`
	Generation             uint64                           `json:"generation"`
	ExecutionProvider      string                           `json:"execution_provider"`
	RegionID               string                           `json:"region_id"`
	MainCommit             string                           `json:"main_commit"`
	MainTree               string                           `json:"main_tree"`
	Platform               string                           `json:"platform"`
	PolicyDigest           string                           `json:"policy_digest"`
	ToolchainDigest        string                           `json:"toolchain_digest"`
	RuntimeImage           string                           `json:"runtime_image"`
	ImageCacheID           string                           `json:"image_cache_id"`
	ImageCacheSnapshotID   string                           `json:"image_cache_snapshot_id"`
	ImageCacheReady        bool                             `json:"image_cache_ready"`
	ImageDigest            string                           `json:"image_digest"`
	OCIProjectCache        *AcceptedBaselineOCIProjectCache `json:"oci_project_cache"`
	GateBinarySHA256       string                           `json:"gate_binary_sha256"`
	RuntimeSeedSHA256      string                           `json:"runtime_seed_manifest_sha256"`
	BaselineManifestDigest string                           `json:"baseline_manifest_digest"`
	CreatedAt              time.Time                        `json:"created_at"`
	AcceptedAt             time.Time                        `json:"accepted_at"`
	RenewedAt              time.Time                        `json:"renewed_at"`
}

// AcceptedBaselineOCIProjectCache 是嵌套的 immutable project cache 身份。
type AcceptedBaselineOCIProjectCache struct {
	Image                 string `json:"image"`
	ContentManifestSHA256 string `json:"content_manifest_sha256"`
	MainTree              string `json:"main_tree"`
	ToolchainDigest       string `json:"toolchain_digest"`
	Platform              string `json:"platform"`
	CachePath             string `json:"cache_path"`
}

// DecodeAcceptedBaselineState 严格解码并校验 accepted baseline 的规范状态 JSON。
func DecodeAcceptedBaselineState(data []byte) (AcceptedBaselineState, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state AcceptedBaselineState
	if err := decoder.Decode(&state); err != nil {
		return AcceptedBaselineState{}, fmt.Errorf("decode accepted baseline state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return AcceptedBaselineState{}, errors.New("accepted baseline state contains multiple JSON values")
		}
		return AcceptedBaselineState{}, fmt.Errorf("decode accepted baseline state trailer: %w", err)
	}
	if err := state.Validate(); err != nil {
		return AcceptedBaselineState{}, err
	}
	canonical, err := json.Marshal(state)
	if err != nil {
		return AcceptedBaselineState{}, fmt.Errorf("marshal accepted baseline state: %w", err)
	}
	if !bytes.Equal(canonical, data) {
		return AcceptedBaselineState{}, errors.New("accepted baseline state is not canonical JSON")
	}
	return state, nil
}

// Validate 校验 accepted baseline 的完整 ECI 身份、缓存绑定和时间戳。
func (state AcceptedBaselineState) Validate() error {
	if state.SchemaVersion != BaselineStateSchemaVersion || state.Generation == 0 {
		return errors.New("accepted baseline state schema or generation is invalid")
	}
	if err := state.validateIdentity(); err != nil {
		return err
	}
	if err := state.validateDigests(); err != nil {
		return err
	}
	if err := state.validateImageCache(); err != nil {
		return err
	}
	if err := state.validateProjectCache(); err != nil {
		return err
	}
	if !validAcceptedBaselineTimes(state.CreatedAt, state.AcceptedAt, state.RenewedAt) {
		return errors.New("accepted baseline timestamps are invalid")
	}
	return nil
}

// validateIdentity 校验 accepted baseline 的 schema、代数、源码和执行位置。
func (state AcceptedBaselineState) validateIdentity() error {
	if err := ValidateAcceptedBaselineProjection(state.SchemaVersion, state.ExecutionProvider, state.RegionID); err != nil {
		return err
	}
	if !baselineGenerationOneOID(state.MainCommit) || !baselineGenerationOneOID(state.MainTree) {
		return errors.New("accepted baseline Git identity is invalid")
	}
	if state.Platform != TargetPlatform {
		return errors.New("accepted baseline platform is invalid")
	}
	return nil
}

// validateDigests 校验 accepted baseline 全部不可变摘要字段。
func (state AcceptedBaselineState) validateDigests() error {
	for _, digest := range []string{state.PolicyDigest, state.ToolchainDigest, state.GateBinarySHA256, state.RuntimeSeedSHA256, state.BaselineManifestDigest} {
		if !isCanonicalSHA256(digest) {
			return errors.New("accepted baseline digest is invalid")
		}
	}
	return nil
}

// validateImageCache 校验 ECI ImageCache 和 immutable runtime image 的绑定。
func (state AcceptedBaselineState) validateImageCache() error {
	if !validGenerationOneOCIImage(state.RuntimeImage) {
		return errors.New("accepted baseline runtime image is invalid")
	}
	if !validAcceptedBaselineIdentifier(state.ImageCacheID) || !validAcceptedBaselineIdentifier(state.ImageCacheSnapshotID) {
		return errors.New("accepted baseline image cache identifier is invalid")
	}
	if !state.ImageCacheReady {
		return errors.New("accepted baseline image cache is not ready")
	}
	if state.ImageDigest != acceptedBaselineImageDigest(state.RuntimeImage) {
		return errors.New("accepted baseline image digest does not match runtime image")
	}
	return nil
}

// validateProjectCache 校验不可变镜像内项目构建缓存的完整绑定。
func (state AcceptedBaselineState) validateProjectCache() error {
	cache := state.OCIProjectCache
	if cache == nil {
		return errors.New("accepted baseline OCI project cache is required")
	}
	return cache.ValidateForBaseline(state.MainTree, state.ToolchainDigest, state.Platform, state.RuntimeImage)
}

// Validate 校验 OCI project cache 的 immutable 内容身份。
func (cache AcceptedBaselineOCIProjectCache) Validate() error {
	if !validGenerationOneOCIImage(cache.Image) || !isCanonicalSHA256(cache.ContentManifestSHA256) {
		return errors.New("accepted baseline OCI project cache digest is invalid")
	}
	if !baselineGenerationOneOID(cache.MainTree) || cache.Platform != TargetPlatform || cache.CachePath != AcceptedBaselineOCIProjectCachePath {
		return errors.New("accepted baseline OCI project cache metadata is invalid")
	}
	if !isCanonicalSHA256(cache.ToolchainDigest) {
		return errors.New("accepted baseline OCI project cache toolchain digest is invalid")
	}
	return nil
}

// ValidateForBaseline 校验缓存与 accepted baseline 主体身份逐字段一致。
func (cache AcceptedBaselineOCIProjectCache) ValidateForBaseline(mainTree, toolchainDigest, platform, runtimeImage string) error {
	if err := cache.Validate(); err != nil {
		return err
	}
	if cache.MainTree != mainTree || cache.ToolchainDigest != toolchainDigest || cache.Platform != platform || cache.Image != runtimeImage {
		return errors.New("accepted baseline OCI project cache does not match baseline identity")
	}
	return nil
}

func validAcceptedBaselineIdentifier(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n\t /\\?#")
}

// ValidAcceptedBaselineIdentifier 暴露唯一的 ImageCache 身份格式守卫给协议 mapper。
func ValidAcceptedBaselineIdentifier(value string) bool {
	return validAcceptedBaselineIdentifier(value)
}

func acceptedBaselineImageDigest(value string) string {
	_, digest, _ := strings.Cut(value, "@")
	return digest
}

// validAcceptedBaselineTimes 校验 accepted baseline 时间戳的 UTC 单调关系。
func validAcceptedBaselineTimes(createdAt, acceptedAt, renewedAt time.Time) bool {
	if createdAt.IsZero() || acceptedAt.IsZero() || renewedAt.IsZero() {
		return false
	}
	if createdAt.Location() != time.UTC || acceptedAt.Location() != time.UTC || renewedAt.Location() != time.UTC {
		return false
	}
	return !acceptedAt.Before(createdAt) && !renewedAt.Before(acceptedAt)
}

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
	if _, err := DecodeGenerationOneProvisionState(receipt); err != nil {
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

// DecodeGenerationOneProvisionState 严格解码 receipt 内嵌的 canonical state，
// 并逐字段绑定 receipt header；调用方必须使用返回的 state 作为 accepted 写入依据。
func DecodeGenerationOneProvisionState(receipt GenerationOneProvisionReceipt) (AcceptedBaselineState, error) {
	if err := validateGenerationOneProvisionStateDigest(receipt); err != nil {
		return AcceptedBaselineState{}, err
	}
	state, err := DecodeAcceptedBaselineState(receipt.StateJSON)
	if err != nil {
		return AcceptedBaselineState{}, fmt.Errorf("decode generation-one provision accepted state: %w", err)
	}
	if err := ValidateGenerationOneProvisionStateBinding(receipt, state); err != nil {
		return AcceptedBaselineState{}, err
	}
	return state, nil
}

// GenerationOneProvisionStateBindingJSONFields 返回 receipt header 必须与
// accepted state 逐字段相等的 canonical JSON role owner。
func GenerationOneProvisionStateBindingJSONFields() []string {
	return []string{
		"generation", "execution_provider", "region_id", "main_commit", "main_tree", "platform",
		"policy_digest", "toolchain_digest", "runtime_image", "image_cache_id", "image_cache_snapshot_id",
		"gate_binary_sha256", "runtime_seed_manifest_sha256", "baseline_manifest_digest",
	}
}

// ValidateGenerationOneProvisionStateBinding 校验已解码 state 与 receipt header 的完整绑定。
func ValidateGenerationOneProvisionStateBinding(receipt GenerationOneProvisionReceipt, state AcceptedBaselineState) error {
	if err := validateGenerationOneProvisionStateDigest(receipt); err != nil {
		return err
	}
	bindings := []struct {
		name         string
		stateValue   string
		receiptValue string
	}{
		{"generation", fmt.Sprint(state.Generation), fmt.Sprint(receipt.Generation)},
		{"execution_provider", state.ExecutionProvider, receipt.ExecutionProvider},
		{"region_id", state.RegionID, receipt.RegionID},
		{"main_commit", state.MainCommit, receipt.MainCommit},
		{"main_tree", state.MainTree, receipt.MainTree},
		{"platform", state.Platform, receipt.Platform},
		{"policy_digest", state.PolicyDigest, receipt.PolicyDigest},
		{"toolchain_digest", state.ToolchainDigest, receipt.ToolchainDigest},
		{"runtime_image", state.RuntimeImage, receipt.RuntimeImage},
		{"image_cache_id", state.ImageCacheID, receipt.ImageCacheID},
		{"image_cache_snapshot_id", state.ImageCacheSnapshotID, receipt.ImageCacheSnapshotID},
		{"gate_binary_sha256", state.GateBinarySHA256, receipt.GateBinarySHA256},
		{"runtime_seed_manifest_sha256", state.RuntimeSeedSHA256, receipt.RuntimeSeedSHA256},
		{"baseline_manifest_digest", state.BaselineManifestDigest, receipt.BaselineManifestDigest},
	}
	roles := GenerationOneProvisionStateBindingJSONFields()
	if len(bindings) != len(roles) {
		return errors.New("generation-one provision accepted state binding roles are incomplete")
	}
	for index, field := range bindings {
		if field.name != roles[index] {
			return fmt.Errorf("generation-one provision accepted state binding role %d drifted", index)
		}
		if field.stateValue != field.receiptValue {
			return fmt.Errorf("generation-one provision accepted state field %s is not bound to receipt", field.name)
		}
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

// validateGenerationOneProvisionCheckObservation 校验首代内容检查的完整身份和结果。
func validateGenerationOneProvisionCheckObservation(receipt GenerationOneProvisionReceipt, observation ProvisionCheckObservation) error {
	if !isRequiredProvisionCheck(observation.Check) || !observation.Executed || !observation.Passed {
		return fmt.Errorf("generation-one provision check %q did not execute and pass", observation.Check)
	}
	if err := validateGenerationOneProvisionCheckIdentity(receipt, observation); err != nil {
		return err
	}
	if err := ValidateNormalResourceIdentity(observation.ResourceClassID, observation.ResourceCPU, observation.ResourceMemoryGiB); err != nil {
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

// validateGenerationOneProvisionCheckContent 校验首代检查的内容观测边界。
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
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return GenerationOneProvisionReceipt{}, fmt.Errorf("marshal canonical generation-one provision receipt: %w", err)
	}
	if !bytes.Equal(canonical, data) {
		return GenerationOneProvisionReceipt{}, errors.New("generation-one provision receipt is not canonical JSON")
	}
	return receipt, nil
}
