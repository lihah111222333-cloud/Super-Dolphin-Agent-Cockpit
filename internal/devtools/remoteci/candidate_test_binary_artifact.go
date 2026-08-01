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

// CandidateTestBinaryArtifactSchemaVersion 是唯一接受的候选测试二进制清单格式。
const CandidateTestBinaryArtifactSchemaVersion uint32 = 1

// CandidateTestBinaryArtifactManifest 将一个 go test -c 输出绑定到精确候选、编译闭包和工具链。
type CandidateTestBinaryArtifactManifest struct {
	SchemaVersion        uint32   `json:"schema_version"`
	CandidateTree        string   `json:"candidate_tree"`
	Package              string   `json:"package"`
	Mode                 string   `json:"mode"`
	Platform             string   `json:"platform"`
	GoToolchain          string   `json:"go_toolchain"`
	CGOEnabled           bool     `json:"cgo_enabled"`
	ToolchainSHA256      string   `json:"toolchain_sha256"`
	BuildFlags           []string `json:"build_flags"`
	CompileClosureSHA256 string   `json:"compile_closure_sha256"`
	BinaryKey            string   `json:"binary_key"`
	BinarySHA256         string   `json:"binary_sha256"`
	BinarySize           int64    `json:"binary_size"`
}

// Validate 拒绝不能复现或不能归属到精确候选的测试二进制。
func (manifest CandidateTestBinaryArtifactManifest) Validate() error {
	if manifest.SchemaVersion != CandidateTestBinaryArtifactSchemaVersion {
		return fmt.Errorf("candidate test binary artifact schema_version must equal %d", CandidateTestBinaryArtifactSchemaVersion)
	}
	if !remoteOIDPattern.MatchString(manifest.CandidateTree) || !validGoTestBinaryBuild(manifest.Package, manifest.Mode, manifest.Platform, manifest.GoToolchain, manifest.CGOEnabled, manifest.BuildFlags) ||
		!remoteDigestPattern.MatchString(manifest.ToolchainSHA256) || !remoteDigestPattern.MatchString(manifest.CompileClosureSHA256) || !remoteDigestPattern.MatchString(manifest.BinarySHA256) {
		return errors.New("candidate test binary artifact identity is invalid")
	}
	if !validCandidateTestBinaryKey(manifest.BinaryKey) || manifest.BinarySize <= 0 || manifest.BinarySize > 512<<20 {
		return errors.New("candidate test binary artifact binary binding is invalid")
	}
	return nil
}

func validGoTestBinaryBuild(pkg, mode, platform, toolchain string, cgoEnabled bool, flags []string) bool {
	cleanPackage := path.Clean(pkg)
	if pkg == "" || len(pkg) > 512 || strings.HasPrefix(pkg, "/") || (cleanPackage != pkg && pkg != "./"+cleanPackage) || (mode != "test" && mode != "race") || platform != "linux/amd64" || toolchain != "go1.25.7" || !cgoEnabled {
		return false
	}
	for _, flag := range flags {
		if flag == "" || !strings.HasPrefix(flag, "-") || strings.ContainsAny(flag, "\x00\r\n") {
			return false
		}
	}
	return true
}

func validCandidateTestBinaryKey(key string) bool {
	return key != "" && len(key) <= 1023 && !strings.HasPrefix(key, "/") && !strings.ContainsAny(key, "\\\\\x00\r\n?#") && path.Clean(key) == key && strings.HasSuffix(key, ".test-bin")
}

// EncodeCandidateTestBinaryArtifactManifest 返回规范 JSON 与其 sha256 摘要。
func EncodeCandidateTestBinaryArtifactManifest(manifest CandidateTestBinaryArtifactManifest) ([]byte, string, error) {
	if err := manifest.Validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", fmt.Errorf("encode candidate test binary artifact manifest: %w", err)
	}
	return data, "sha256:" + fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// DecodeCandidateTestBinaryArtifactManifest 严格解码单个不可变清单。
func DecodeCandidateTestBinaryArtifactManifest(data []byte) (CandidateTestBinaryArtifactManifest, error) {
	var manifest CandidateTestBinaryArtifactManifest
	if err := gate.DecodeStrictJSON(data, &manifest); err != nil {
		return CandidateTestBinaryArtifactManifest{}, fmt.Errorf("decode candidate test binary artifact manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return CandidateTestBinaryArtifactManifest{}, err
	}
	return manifest, nil
}
