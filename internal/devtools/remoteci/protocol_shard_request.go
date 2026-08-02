// Package remoteci 定义协调器与弹性 CI worker 共享的严格协议。
package remoteci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

const ShardRequestSchemaVersion uint32 = 12

var (
	remoteDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	remoteIDPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,127}$`)
	remoteOIDPattern    = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

// ShardRequest binds one ECI container to an immutable OCI project cache,
// exact target tree, and canonical gate shard.
type ShardRequest struct {
	SchemaVersion                uint32                   `json:"schema_version"`
	JobID                        string                   `json:"job_id"`
	ShardIdentity                string                   `json:"shard_identity"`
	Profile                      gate.Profile             `json:"profile"`
	PlanDigest                   string                   `json:"plan_digest"`
	BaselineManifest             string                   `json:"runner_manifest_digest"`
	ImageCacheSnapshotID         string                   `json:"image_cache_snapshot_id"`
	OCIProjectCache              *BaselineOCIProjectCache `json:"oci_project_cache"`
	RunnerBaseCommit             string                   `json:"runner_base_commit"`
	RunnerBaseTree               string                   `json:"runner_base_tree"`
	BaselineRuntimeImage         string                   `json:"baseline_runtime_image,omitempty"`
	BaselineToolchainDigest      string                   `json:"baseline_toolchain_digest,omitempty"`
	SourceTreeSHA                string                   `json:"source_tree_sha"`
	PatchFormat                  string                   `json:"patch_format"`
	PatchKey                     string                   `json:"patch_key"`
	PatchSHA256                  string                   `json:"patch_sha256"`
	PatchSize                    int64                    `json:"patch_size"`
	ManifestKey                  string                   `json:"manifest_key"`
	ManifestSHA256               string                   `json:"manifest_sha256"`
	CandidateGateSourceSHA256    string                   `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string                   `json:"candidate_gate_toolchain_sha256"`
	GateIDs                      []gate.GateID            `json:"gate_ids"`
	Calibration                  bool                     `json:"calibration"`
	CalibrationResource          *shardresource.Class     `json:"calibration_resource,omitempty"`
}

// Validate 拒绝缺字段、可变身份、路径逃逸和重复 gate。
func (request ShardRequest) Validate() error {
	if err := request.validateIdentity(); err != nil {
		return err
	}
	if err := request.validateOCIProjectCache(); err != nil {
		return err
	}
	if err := request.validateSource(); err != nil {
		return err
	}
	if err := request.validateObjects(); err != nil {
		return err
	}
	if err := request.validateCalibrationResource(); err != nil {
		return err
	}
	return validateGateIDs(request.GateIDs)
}

func (request ShardRequest) validateCalibrationResource() error {
	if !request.Calibration {
		if request.CalibrationResource != nil {
			return errors.New("non-calibration remote shard request carries calibration_resource")
		}
		return nil
	}
	if request.CalibrationResource == nil {
		return errors.New("calibration remote shard request requires calibration_resource")
	}
	class := *request.CalibrationResource
	policy := shardresource.Policy{Classes: []shardresource.Class{class}, Bootstrap: shardresource.BootstrapClasses{Guard: class.ID, NodeTest: class.ID, GoTest: class.ID}, CalibrationClass: class.ID, HeadroomPercent: 1, MinSamplesToDownsize: 1}
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("calibration remote shard resource: %w", err)
	}
	return cicontract.ValidateCalibrationResources(class.ID, class.VCPU, class.MemoryGiB)
}

// validateOCIProjectCache binds the required image seed to the exact baseline
// tree consumed by this shard. The materializer must still verify the image
// filesystem is read-only before handing its cache path to GOCACHEPROG.
func (request ShardRequest) validateOCIProjectCache() error {
	if !validImageCacheIdentifier(request.ImageCacheSnapshotID) {
		return errors.New("remote shard accepted ImageCacheSnapshotID is required")
	}
	if request.OCIProjectCache == nil {
		return errors.New("remote shard OCI project cache is required")
	}
	if err := request.OCIProjectCache.validate(); err != nil {
		return err
	}
	if err := request.OCIProjectCache.ValidateForBaseline(request.RunnerBaseTree, request.BaselineToolchainDigest, cicontract.TargetPlatform, request.BaselineRuntimeImage); err != nil {
		return err
	}
	return nil
}

// validateIdentity 校验请求模式、任务号、摘要和 gate profile。
func (request ShardRequest) validateIdentity() error {
	if request.SchemaVersion != ShardRequestSchemaVersion {
		return fmt.Errorf("remote shard request schema_version must equal %d", ShardRequestSchemaVersion)
	}
	if !remoteIDPattern.MatchString(request.JobID) {
		return errors.New("remote shard request job_id is invalid")
	}
	if !validRequestDigests(request) {
		return errors.New("remote shard request identity digest is invalid")
	}
	if err := request.Profile.Validate(); err != nil {
		return fmt.Errorf("remote shard request profile: %w", err)
	}
	return nil
}

// validRequestDigests 判断所有不可变请求摘要是否为 canonical SHA-256。
func validRequestDigests(request ShardRequest) bool {
	return remoteDigestPattern.MatchString(request.ShardIdentity) &&
		remoteDigestPattern.MatchString(request.PlanDigest) &&
		remoteDigestPattern.MatchString(request.BaselineManifest) &&
		remoteDigestPattern.MatchString(request.CandidateGateSourceSHA256) &&
		remoteDigestPattern.MatchString(request.CandidateGateToolchainSHA256)
}

// validateSource 校验源 Git 对象和二进制补丁格式。
func (request ShardRequest) validateSource() error {
	if !validSourceObjects(request) {
		return errors.New("remote shard request source object identity is invalid")
	}
	if request.PatchFormat != "git-binary-v1" {
		return errors.New("remote shard request patch_format is invalid")
	}
	return nil
}

// validSourceObjects 判断 runner 基线与目标树对象是否为 canonical Git ID。
func validSourceObjects(request ShardRequest) bool {
	return remoteOIDPattern.MatchString(request.RunnerBaseCommit) && remoteOIDPattern.MatchString(request.RunnerBaseTree) && remoteOIDPattern.MatchString(request.SourceTreeSHA)
}

// validateObjects 校验 patch 和 manifest 的 OSS 绑定与容量。
func (request ShardRequest) validateObjects() error {
	prefix, err := request.sourceObjectPrefix()
	if err != nil {
		return err
	}
	if err := validateObjectBinding(request.PatchKey, request.PatchSHA256, ".patch", prefix); err != nil {
		return fmt.Errorf("remote shard patch: %w", err)
	}
	if request.PatchSize < 0 || request.PatchSize > 1<<30 {
		return errors.New("remote shard request patch_size is invalid")
	}
	if err := validateObjectBinding(request.ManifestKey, request.ManifestSHA256, ".manifest.json", prefix); err != nil {
		return fmt.Errorf("remote shard manifest: %w", err)
	}
	return nil
}

// sourceObjectPrefix 绑定 patch、manifest 与请求 JobID 的同一对象目录。
func (request ShardRequest) sourceObjectPrefix() (string, error) {
	patchDirectory := path.Dir(request.PatchKey)
	if patchDirectory == "." || path.Base(patchDirectory) != request.JobID || path.Dir(request.ManifestKey) != patchDirectory {
		return "", errors.New("remote shard source object directories do not match job_id")
	}
	return patchDirectory + "/", nil
}

// validateGateIDs 校验 worker 分片中的 gate 集合非空且不重复。
func validateGateIDs(ids []gate.GateID) error {
	if len(ids) == 0 || len(ids) > 64 {
		return errors.New("remote shard request gate_ids count is invalid")
	}
	seen := make(map[gate.GateID]struct{}, len(ids))
	for index, id := range ids {
		if strings.TrimSpace(string(id)) == "" {
			return fmt.Errorf("remote shard request gate_ids[%d] is empty", index)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("remote shard request gate_ids[%d] is duplicated", index)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// DecodeShardRequest 严格解码并校验 worker 请求。
func DecodeShardRequest(data []byte) (ShardRequest, error) {
	var request ShardRequest
	if err := gate.DecodeStrictJSON(data, &request); err != nil {
		return ShardRequest{}, fmt.Errorf("decode remote shard request: %w", err)
	}
	if err := request.Validate(); err != nil {
		return ShardRequest{}, fmt.Errorf("validate remote shard request: %w", err)
	}
	return request, nil
}

// EncodeShardRequest 生成 canonical JSON 和用于 ECI admission 的内容摘要。
func EncodeShardRequest(request ShardRequest) ([]byte, string, error) {
	if err := request.Validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, "", fmt.Errorf("encode remote shard request: %w", err)
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// validateObjectBinding 校验单个 OSS 键与其小写 SHA-256 摘要的绑定。
func validateObjectBinding(key string, digest string, suffix string, prefix string) error {
	if !validObjectKey(key, suffix, prefix) {
		return errors.New("object key is invalid")
	}
	if !validObjectDigest(digest) {
		return errors.New("object SHA-256 is invalid")
	}
	return nil
}

// validObjectKey 判断 OSS 对象键没有路径逃逸或协议字符。
func validObjectKey(key string, suffix string, prefix string) bool {
	return key != "" && len(key) <= 1023 && !strings.HasPrefix(key, "/") && !strings.ContainsAny(key, "\\\x00\r\n?#") && path.Clean(key) == key && strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix)
}

// validObjectPrefix 判断配置前缀可安全组成相对 OSS 对象键。
func validObjectPrefix(prefix string) bool {
	return prefix != "" && strings.HasSuffix(prefix, "/") && validObjectKey(prefix+"object", "object", prefix)
}

// validObjectDigest 判断对象摘要长度、编码和大小写均为 canonical。
func validObjectDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil && strings.ToLower(digest) == digest
}
