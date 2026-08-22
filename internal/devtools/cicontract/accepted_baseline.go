package cicontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// AcceptedBaselineOCIProjectCachePath 是 accepted baseline 镜像内唯一的
	// immutable Go build cache 路径；所有协议消费者复用此 authority。
	AcceptedBaselineOCIProjectCachePath = "/opt/super-dolphin/cache/go-build"
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
	if !baselineOID(state.MainCommit) || !baselineOID(state.MainTree) {
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
	if !validOCIImage(state.RuntimeImage) {
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
	if !validOCIImage(cache.Image) || !isCanonicalSHA256(cache.ContentManifestSHA256) {
		return errors.New("accepted baseline OCI project cache digest is invalid")
	}
	if !baselineOID(cache.MainTree) || cache.Platform != TargetPlatform || cache.CachePath != AcceptedBaselineOCIProjectCachePath {
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

func baselineOID(value string) bool {
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

func validOCIImage(value string) bool {
	repository, digest, ok := strings.Cut(value, "@")
	if !ok || repository == "" || repository != strings.ToLower(repository) || strings.ContainsAny(repository, " @\t\r\n\\?#") || strings.Contains(repository, "://") || !isCanonicalSHA256(digest) {
		return false
	}
	last := repository
	if slash := strings.LastIndexByte(repository, '/'); slash >= 0 {
		last = repository[slash+1:]
	}
	return last != "" && !strings.Contains(last, ":")
}
