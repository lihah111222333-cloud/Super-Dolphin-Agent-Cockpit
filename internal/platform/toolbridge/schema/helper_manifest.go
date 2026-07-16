package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

// buildAppCommit 由桌面应用与 schema helper 的构建命令以同一 Git commit 注入。
// archguard:ignore global_vars -- 该值仅允许 go link -X 在构建期设置，运行期只读。
var buildAppCommit string

const (
	HelperBaseName       = "mcp-schema-compiler-helper"
	HelperManifestSuffix = ".manifest.json"
	maxHelperBytes       = 64 << 20
	maxManifestBytes     = 32 << 10
)

// HelperManifest binds the package-owned helper to the application build identity.
type HelperManifest struct {
	Protocol         string `json:"protocol"`
	Helper           string `json:"helper"`
	SHA256           string `json:"sha256"`
	ExecutablePolicy string `json:"executable_policy"`
	AppCommit        string `json:"app_commit"`
	GoVersion        string `json:"go_version"`
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
}

// HelperIdentity is the application identity that a helper package must match.
type HelperIdentity struct {
	AppCommit string
	GoVersion string
	GOOS      string
	GOARCH    string
}

// HelperFileName 返回目标平台唯一允许的 helper 文件名。
func HelperFileName(goos string) string {
	if goos == "windows" {
		return HelperBaseName + ".exe"
	}
	return HelperBaseName
}

// HelperManifestFileName 返回与目标 helper 同目录绑定的 manifest 文件名。
func HelperManifestFileName(goos string) string {
	return HelperFileName(goos) + HelperManifestSuffix
}

func executablePolicy(goos string) string {
	if goos == "windows" {
		return "windows_pe"
	}
	return "owner_execute"
}

// WriteHelperManifest 为已构建 helper 写入完整且精确的 package identity。
func WriteHelperManifest(helperPath, manifestPath string, identity HelperIdentity) error {
	image, err := readRegularNoSymlink(helperPath, maxHelperBytes)
	if err != nil {
		return fmt.Errorf("read helper: %w", err)
	}
	if err := validateIdentity(identity); err != nil {
		return err
	}
	if err := verifyExecutable(helperPath, identity.GOOS, image); err != nil {
		return err
	}
	digest := sha256.Sum256(image)
	manifest := HelperManifest{
		Protocol: ProtocolID, Helper: filepath.Base(helperPath), SHA256: hex.EncodeToString(digest[:]),
		ExecutablePolicy: executablePolicy(identity.GOOS), AppCommit: identity.AppCommit,
		GoVersion: identity.GoVersion, GOOS: identity.GOOS, GOARCH: identity.GOARCH,
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal helper manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		return fmt.Errorf("write helper manifest: %w", err)
	}
	return nil
}

func verifyHelperPackage(helperPath, manifestPath string, expected HelperIdentity) ([]byte, error) {
	manifest, err := readHelperManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if err := validateHelperManifest(manifest, helperPath, manifestPath, expected); err != nil {
		return nil, err
	}
	return readVerifiedHelperImage(helperPath, manifest, expected.GOOS)
}

func readHelperManifest(manifestPath string) (HelperManifest, error) {
	manifestBytes, err := readRegularNoSymlink(manifestPath, maxManifestBytes)
	if err != nil {
		return HelperManifest{}, fmt.Errorf("read package-owned helper manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest HelperManifest
	if err := decoder.Decode(&manifest); err != nil {
		return HelperManifest{}, fmt.Errorf("decode package-owned helper manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return HelperManifest{}, fmt.Errorf("decode package-owned helper manifest: %w", err)
	}
	return manifest, nil
}

// validateHelperManifest 校验 manifest identity 与 helper 的同目录 canonical layout。
func validateHelperManifest(manifest HelperManifest, helperPath, manifestPath string, expected HelperIdentity) error {
	if err := validateIdentity(expected); err != nil {
		return fmt.Errorf("expected helper identity: %w", err)
	}
	wantName := HelperFileName(expected.GOOS)
	actual := HelperIdentity{AppCommit: manifest.AppCommit, GoVersion: manifest.GoVersion, GOOS: manifest.GOOS, GOARCH: manifest.GOARCH}
	if manifest.Protocol != ProtocolID || manifest.Helper != wantName || manifest.ExecutablePolicy != executablePolicy(expected.GOOS) || actual != expected {
		return fmt.Errorf("package-owned helper manifest identity mismatch")
	}
	if filepath.Base(helperPath) != manifest.Helper || filepath.Dir(helperPath) != filepath.Dir(manifestPath) {
		return fmt.Errorf("package-owned helper and manifest layout mismatch")
	}
	return nil
}

func readVerifiedHelperImage(helperPath string, manifest HelperManifest, goos string) ([]byte, error) {
	image, err := readRegularNoSymlink(helperPath, maxHelperBytes)
	if err != nil {
		return nil, fmt.Errorf("read package-owned helper: %w", err)
	}
	if err := verifyExecutable(helperPath, goos, image); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(image)
	if !strings.EqualFold(manifest.SHA256, hex.EncodeToString(digest[:])) {
		return nil, fmt.Errorf("package-owned helper SHA-256 mismatch")
	}
	return image, nil
}

// VerifyHelperPackage 校验 package identity，并在 helper bytes 已固定后返回。
func VerifyHelperPackage(helperPath, manifestPath string, expected HelperIdentity) error {
	_, err := verifyHelperPackage(helperPath, manifestPath, expected)
	return err
}

func validateIdentity(identity HelperIdentity) error {
	if strings.TrimSpace(identity.AppCommit) == "" || strings.TrimSpace(identity.GoVersion) == "" ||
		strings.TrimSpace(identity.GOOS) == "" || strings.TrimSpace(identity.GOARCH) == "" {
		return errors.New("helper identity fields are required")
	}
	return nil
}

func readRegularNoSymlink(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular non-symlink file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return os.ReadFile(path)
}

// verifyExecutable 按目标平台执行 owner-execute 或 Windows PE 策略校验。
func verifyExecutable(path, goos string, image []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if goos == "windows" {
		if filepath.Ext(path) != ".exe" || len(image) < 2 || image[0] != 'M' || image[1] != 'Z' {
			return fmt.Errorf("package-owned helper violates windows executable policy")
		}
		return nil
	}
	if info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("package-owned helper violates owner-execute policy")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func writeExecutableSnapshot(image []byte, goos string) (string, func() error, error) {
	dir, err := os.MkdirTemp("", "reasonix-schema-helper.")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	path := filepath.Join(dir, HelperFileName(goos))
	if err := os.WriteFile(path, image, 0o700); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func currentRuntimeIdentity(appCommit, goVersion string) HelperIdentity {
	return HelperIdentity{AppCommit: appCommit, GoVersion: goVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

// CurrentBuildIdentity 返回当前 Go binary 内嵌的 VCS 与 toolchain identity。
func CurrentBuildIdentity() (HelperIdentity, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok || strings.TrimSpace(info.GoVersion) == "" {
		return HelperIdentity{}, errors.New("running Go build identity is unavailable")
	}
	commit := strings.TrimSpace(buildAppCommit)
	if commit == "" {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				commit = strings.TrimSpace(setting.Value)
				break
			}
		}
	}
	if commit == "" {
		return HelperIdentity{}, errors.New("running application commit identity is unavailable")
	}
	return currentRuntimeIdentity(commit, info.GoVersion), nil
}
