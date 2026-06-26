// Package appupdate 提供应用自动更新的检查、下载和安装能力，支持 GitHub Releases 和自定义 manifest 两种更新源。
package appupdate

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// ErrNoUpdate 表示 manifest 版本不高于当前版本，没有可安装更新。
var ErrNoUpdate = errors.New("no update available")

// ManifestPayload 是签名 manifest 中参与签名的更新元数据。
type ManifestPayload struct {
	AppID          string           `json:"app_id"`
	Channel        string           `json:"channel"`
	Version        string           `json:"version"`
	MinimumVersion string           `json:"minimum_version,omitempty"`
	Artifacts      []UpdateArtifact `json:"artifacts"`
}

// SignedManifest 是服务端发布的签名 manifest 外层结构。
type SignedManifest struct {
	Payload   ManifestPayload `json:"payload"`
	Signature string          `json:"signature"`
}

// UpdateArtifact 描述单个平台的更新产物下载地址、hash 和大小。
type UpdateArtifact struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// VerifyOptions 是校验 signed manifest 时由本地运行时提供的可信约束。
type VerifyOptions struct {
	PublicKey      []byte
	AppID          string
	Channel        string
	Platform       string
	CurrentVersion string
}

// VerifySignedManifest 验证signedmanifest。
func VerifySignedManifest(raw []byte, opts VerifyOptions) (ManifestPayload, UpdateArtifact, error) {
	if len(opts.PublicKey) != ed25519.PublicKeySize {
		return ManifestPayload{}, UpdateArtifact{}, fmt.Errorf("app update public key length = %d, want %d", len(opts.PublicKey), ed25519.PublicKeySize)
	}
	signed, err := decodeSignedManifest(raw)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := verifyManifestSignature(signed, opts.PublicKey); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := validatePayloadTarget(signed.Payload, opts); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := validateVersionWindow(signed.Payload, opts.CurrentVersion); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	artifact, err := artifactForPlatform(signed.Payload.Artifacts, opts.Platform)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := validateArtifact(artifact); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	return signed.Payload, artifact, nil
}

// decodeSignedManifest 反序列化并严格校验签名 manifest，不允许有尾随 JSON 数据。
func decodeSignedManifest(raw []byte) (SignedManifest, error) {
	var signed SignedManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&signed); err != nil {
		return SignedManifest{}, fmt.Errorf("decode app update manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SignedManifest{}, errors.New("decode app update manifest: trailing JSON data")
	}
	return signed, nil
}

// verifyManifestSignature 校验 ed25519 签名，公钥由调用方提供。
func verifyManifestSignature(signed SignedManifest, publicKey []byte) error {
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		return fmt.Errorf("decode app update signature: %w", err)
	}
	canonicalPayload, err := json.Marshal(signed.Payload)
	if err != nil {
		return fmt.Errorf("canonicalize app update payload: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), canonicalPayload, signature) {
		return errors.New("verify app update signature: invalid signature")
	}
	return nil
}

// validatePayloadTarget 校验 manifest 的 app_id/channel 与期望值一致，且 platform 非空。
func validatePayloadTarget(payload ManifestPayload, opts VerifyOptions) error {
	if payload.AppID != opts.AppID {
		return fmt.Errorf("app update app_id = %q, want %q", payload.AppID, opts.AppID)
	}
	if payload.Channel != opts.Channel {
		return fmt.Errorf("app update channel = %q, want %q", payload.Channel, opts.Channel)
	}
	if strings.TrimSpace(opts.Platform) == "" {
		return errors.New("app update platform option is required")
	}
	return nil
}

// validateVersionWindow 校验 manifest 版本窗口，旧版本或同版本返回 ErrNoUpdate。
func validateVersionWindow(payload ManifestPayload, current string) error {
	updateVersion, err := parseManifestVersion(payload.Version)
	if err != nil {
		return fmt.Errorf("app update version: %w", err)
	}
	currentVersion, err := parseManifestVersion(current)
	if err != nil {
		return fmt.Errorf("current app version: %w", err)
	}
	if payload.MinimumVersion != "" {
		if err := validateMinimumVersion(payload.MinimumVersion, currentVersion); err != nil {
			return err
		}
	}
	if compareManifestVersions(updateVersion, currentVersion) <= 0 {
		return ErrNoUpdate
	}
	return nil
}

// validateMinimumVersion 校验当前版本不低于 manifest 声明的最低升级版本。
func validateMinimumVersion(minimum string, current manifestVersion) error {
	minimumVersion, err := parseManifestVersion(minimum)
	if err != nil {
		return fmt.Errorf("minimum app update version: %w", err)
	}
	if compareManifestVersions(current, minimumVersion) < 0 {
		return fmt.Errorf("current app version is below minimum update version %q", minimum)
	}
	return nil
}

// artifactForPlatform 从 artifacts 列表中查找匹配 platform 的条目。
func artifactForPlatform(artifacts []UpdateArtifact, platform string) (UpdateArtifact, error) {
	for _, artifact := range artifacts {
		if artifact.Platform == platform {
			return artifact, nil
		}
	}
	return UpdateArtifact{}, fmt.Errorf("app update artifact for platform %q not found", platform)
}

// validateArtifact 校验更新产物 URL、sha256 和 size，失败时阻止下载。
func validateArtifact(artifact UpdateArtifact) error {
	artifactURL, err := url.Parse(artifact.URL)
	if err != nil {
		return fmt.Errorf("parse app update artifact URL: %w", err)
	}
	if artifactURL.Scheme != "https" || artifactURL.Host == "" {
		return fmt.Errorf("app update artifact URL must be HTTPS with host: %q", artifact.URL)
	}
	if err := validateSHA256Hex(artifact.SHA256); err != nil {
		return err
	}
	if artifact.Size <= 0 {
		return fmt.Errorf("app update artifact size = %d, want > 0", artifact.Size)
	}
	return nil
}

// validateSHA256Hex 校验十六进制 sha256 字符串长度为 64 且可解码。
func validateSHA256Hex(value string) error {
	if len(value) != 64 {
		return fmt.Errorf("app update artifact sha256 length = %d, want 64", len(value))
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return fmt.Errorf("decode app update artifact sha256: %w", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("app update artifact sha256 decoded length = %d, want 32", len(decoded))
	}
	return nil
}

// manifestVersion 是内部版本比较用的三段数字版本号。
type manifestVersion [3]int

// parseManifestVersion 将版本字符串（如 v1.2.3）解析为 [3]int 数组。
func parseManifestVersion(raw string) (manifestVersion, error) {
	value := strings.TrimPrefix(strings.TrimPrefix(raw, "v"), "V")
	if value == "" {
		return manifestVersion{}, errors.New("empty version")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 3 {
		return manifestVersion{}, fmt.Errorf("version %q has %d segments, want 1 to 3", raw, len(parts))
	}
	var version manifestVersion
	for i, part := range parts {
		number, err := parseVersionSegment(raw, part)
		if err != nil {
			return manifestVersion{}, err
		}
		version[i] = number
	}
	return version, nil
}

// parseVersionSegment 将单个版本段字符串解析为非负整数。
func parseVersionSegment(raw, part string) (int, error) {
	if part == "" {
		return 0, fmt.Errorf("version %q contains an empty segment", raw)
	}
	if !isDigitString(part) {
		return 0, fmt.Errorf("version %q segment %q is not numeric", raw, part)
	}
	number, err := strconv.Atoi(part)
	if err != nil {
		return 0, fmt.Errorf("parse version %q segment %q: %w", raw, part, err)
	}
	return number, nil
}

// isDigitString 判断字符串是否全为 ASCII 数字。
func isDigitString(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// compareManifestVersions 比较两个版本，left > right 返回 1，相等返回 0，less 返回 -1。
func compareManifestVersions(left, right manifestVersion) int {
	for i := range left {
		if left[i] > right[i] {
			return 1
		}
		if left[i] < right[i] {
			return -1
		}
	}
	return 0
}
