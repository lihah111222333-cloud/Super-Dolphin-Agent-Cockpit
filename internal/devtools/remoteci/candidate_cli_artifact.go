package remoteci

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// CandidateCLIArtifactSchemaVersion 是唯一接受的候选 CLI 制品清单格式。
const CandidateCLIArtifactSchemaVersion uint32 = 1

// CandidateCLIArtifactManifest 将不可变 Linux/amd64 gate CLI 绑定到其候选编译闭包。
// producer 必须先发布二进制，再发布清单，且不得覆盖任一对象。
type CandidateCLIArtifactManifest struct {
	SchemaVersion   uint32 `json:"schema_version"`
	CandidateTree   string `json:"candidate_tree"`
	SourceSHA256    string `json:"source_sha256"`
	ToolchainSHA256 string `json:"toolchain_sha256"`
	Platform        string `json:"platform"`
	BinaryKey       string `json:"binary_key"`
	BinarySHA256    string `json:"binary_sha256"`
	BinarySize      int64  `json:"binary_size"`
	CLIIdentity     string `json:"cli_identity"`
}

// Validate 拒绝不完整、看似可变或跨候选的制品元数据。
func (manifest CandidateCLIArtifactManifest) Validate() error {
	if manifest.SchemaVersion != CandidateCLIArtifactSchemaVersion {
		return fmt.Errorf("candidate CLI artifact schema_version must equal %d", CandidateCLIArtifactSchemaVersion)
	}
	if !validCandidateCLIArtifactIdentity(manifest) {
		return errors.New("candidate CLI artifact identity is invalid")
	}
	if !validCandidateCLIArtifactBinary(manifest) {
		return errors.New("candidate CLI artifact platform or binary size is invalid")
	}
	if !validCandidateCLIArtifactKey(manifest.BinaryKey) {
		return errors.New("candidate CLI artifact binary key is invalid")
	}
	if manifest.CLIIdentity != CandidateCLIIdentity(manifest.SourceSHA256, manifest.ToolchainSHA256) {
		return errors.New("candidate CLI artifact cli identity is invalid")
	}
	return nil
}

// validCandidateCLIArtifactIdentity 判断清单是否绑定不可变候选与摘要。
func validCandidateCLIArtifactIdentity(manifest CandidateCLIArtifactManifest) bool {
	return remoteOIDPattern.MatchString(manifest.CandidateTree) &&
		remoteDigestPattern.MatchString(manifest.SourceSHA256) &&
		remoteDigestPattern.MatchString(manifest.ToolchainSHA256) &&
		remoteDigestPattern.MatchString(manifest.BinarySHA256)
}

// validCandidateCLIArtifactBinary 判断候选 CLI 的目标平台和文件大小是否可接受。
func validCandidateCLIArtifactBinary(manifest CandidateCLIArtifactManifest) bool {
	return manifest.Platform == "linux/amd64" && manifest.BinarySize > 0 && manifest.BinarySize <= 512<<20
}

// ValidateForManifestKey binds the binary key to the manifest's immutable job directory.
func (manifest CandidateCLIArtifactManifest) ValidateForManifestKey(manifestKey string) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	manifestDirectory := path.Dir(manifestKey)
	if manifestDirectory == "." || path.Dir(manifest.BinaryKey) != manifestDirectory {
		return errors.New("candidate CLI artifact binary key does not match manifest job prefix")
	}
	return nil
}

// CandidateCLIIdentity 返回下载后二进制必须由 worker 报告的精确身份。
func CandidateCLIIdentity(sourceSHA256, toolchainSHA256 string) string {
	return fmt.Sprintf("gate_source_sha256=%s\nplatform=linux/amd64\ntoolchain_digest=%s", sourceSHA256, toolchainSHA256)
}

// EncodeCandidateCLIArtifactManifest 返回规范 JSON 与其 sha256 摘要。
func EncodeCandidateCLIArtifactManifest(manifest CandidateCLIArtifactManifest) ([]byte, string, error) {
	if err := manifest.Validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", fmt.Errorf("encode candidate CLI artifact manifest: %w", err)
	}
	return data, "sha256:" + fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// DecodeCandidateCLIArtifactManifest 严格解码单个不可变清单。
func DecodeCandidateCLIArtifactManifest(data []byte) (CandidateCLIArtifactManifest, error) {
	var manifest CandidateCLIArtifactManifest
	if err := gate.DecodeStrictJSON(data, &manifest); err != nil {
		return CandidateCLIArtifactManifest{}, fmt.Errorf("decode candidate CLI artifact manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return CandidateCLIArtifactManifest{}, err
	}
	return manifest, nil
}

func validCandidateCLIArtifactKey(key string) bool {
	return key != "" && len(key) <= 1023 && !strings.HasPrefix(key, "/") &&
		!strings.ContainsAny(key, "\\\\\x00\r\n?#") && path.Clean(key) == key && strings.HasSuffix(key, ".candidate-cli")
}
