//go:build windows

package main

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
	"strings"
)

const runtimeServerWindowsGoplsManifestMaxSize = 1 << 20

// runtimeServerWindowsGoplsTrustProof 是 Windows daemon 启动前取得的包内 gopls 身份证明。
type runtimeServerWindowsGoplsTrustProof struct {
	Path       string
	Version    string
	SHA256     string
	BundleRoot string
}

type runtimeServerWindowsLSPManifest struct {
	SchemaVersion int                                      `json:"schema_version"`
	BundlePath    string                                   `json:"bundle_path"`
	Profile       string                                   `json:"profile"`
	Servers       map[string]runtimeServerWindowsLSPServer `json:"servers"`
}

type runtimeServerWindowsLSPServer struct {
	Path      string   `json:"path"`
	Version   string   `json:"version"`
	SHA256    string   `json:"sha256"`
	Languages []string `json:"languages"`
}

type runtimeServerWindowsGoplsLayout struct {
	BundleRoot   string
	ManifestPath string
}

// runtimeServerTrustedWindowsGopls 严格重读当前包清单，并验证即将启动的 gopls 实际内容摘要。
func runtimeServerTrustedWindowsGopls(binary string) (runtimeServerWindowsGoplsTrustProof, error) {
	layout, err := runtimeServerResolveWindowsGoplsLayout()
	if err != nil {
		return runtimeServerWindowsGoplsTrustProof{}, err
	}
	manifest, err := runtimeServerReadWindowsLSPManifest(layout.ManifestPath)
	if err != nil {
		return runtimeServerWindowsGoplsTrustProof{}, err
	}
	server, ok := manifest.Servers["gopls"]
	if !ok {
		return runtimeServerWindowsGoplsTrustProof{}, errors.New("Windows LSP manifest is missing the gopls server")
	}
	path, version, expectedSHA, err := runtimeServerResolveWindowsGoplsServer(layout.BundleRoot, server)
	if err != nil {
		return runtimeServerWindowsGoplsTrustProof{}, err
	}
	if err := runtimeServerRequireSameWindowsFile(binary, path); err != nil {
		return runtimeServerWindowsGoplsTrustProof{}, err
	}
	if err := runtimeServerVerifyWindowsGoplsSHA256(path, expectedSHA); err != nil {
		return runtimeServerWindowsGoplsTrustProof{}, err
	}
	return runtimeServerWindowsGoplsTrustProof{
		Path:       path,
		Version:    version,
		SHA256:     expectedSHA,
		BundleRoot: layout.BundleRoot,
	}, nil
}

// runtimeServerResolveWindowsGoplsLayout 把 self、bundle 与 manifest 收敛到同一安装根的固定布局。
func runtimeServerResolveWindowsGoplsLayout() (runtimeServerWindowsGoplsLayout, error) {
	installRoot, err := runtimeServerWindowsSelfInstallRoot()
	if err != nil {
		return runtimeServerWindowsGoplsLayout{}, err
	}
	bundleRoot, manifestPath, err := runtimeServerWindowsLSPEnvPaths()
	if err != nil {
		return runtimeServerWindowsGoplsLayout{}, err
	}
	bundleRoot, err = runtimeServerCanonicalWindowsDirectory(bundleRoot)
	if err != nil {
		return runtimeServerWindowsGoplsLayout{}, fmt.Errorf("resolve Windows LSP bundle root: %w", err)
	}
	if !runtimeServerSameWindowsPath(bundleRoot, filepath.Join(installRoot, "lsp")) {
		return runtimeServerWindowsGoplsLayout{}, errors.New("Windows LSP bundle is outside the mcp-lsp installation root")
	}
	manifestPath, err = runtimeServerCanonicalWindowsFile(manifestPath)
	if err != nil {
		return runtimeServerWindowsGoplsLayout{}, fmt.Errorf("resolve Windows LSP manifest: %w", err)
	}
	if !runtimeServerSameWindowsPath(manifestPath, filepath.Join(bundleRoot, "lsp-manifest.json")) {
		return runtimeServerWindowsGoplsLayout{}, errors.New("Windows LSP manifest is outside the trusted bundle location")
	}
	return runtimeServerWindowsGoplsLayout{BundleRoot: bundleRoot, ManifestPath: manifestPath}, nil
}

// runtimeServerWindowsSelfInstallRoot 从真实 self 路径确认旧 bin 或 skill bin\LSP 固定布局。
func runtimeServerWindowsSelfInstallRoot() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve Windows mcp-lsp executable: %w", err)
	}
	self, err = runtimeServerCanonicalWindowsFile(self)
	if err != nil {
		return "", fmt.Errorf("resolve Windows mcp-lsp executable: %w", err)
	}
	installDir := filepath.Dir(self)
	if strings.EqualFold(filepath.Base(installDir), "bin") {
		return filepath.Dir(installDir), nil
	}
	if !strings.EqualFold(filepath.Base(installDir), "LSP") ||
		!strings.EqualFold(filepath.Base(filepath.Dir(installDir)), "bin") {
		return "", errors.New("Windows mcp-lsp executable must be installed under the package bin directory")
	}
	if runtimeServerWindowsSkillDeliveryNameAllowed(filepath.Base(self)) {
		return installDir, nil
	}
	return "", errors.New("Windows skill delivery mcp-lsp executable name is invalid")
}

// runtimeServerWindowsSkillDeliveryNameAllowed 要求跨平台包文件名一眼可见 Windows 平台及 ARM64、x64 或 x86 架构。
func runtimeServerWindowsSkillDeliveryNameAllowed(name string) bool {
	switch strings.ToLower(name) {
	case "mcp-lsp-windows-arm64.exe", "mcp-lsp-windows-x64.exe", "mcp-lsp-windows-x86.exe":
		return true
	default:
		return false
	}
}

// runtimeServerWindowsLSPEnvPaths 读取完整的 Windows bundle 环境契约，不接受 PATH 推断。
func runtimeServerWindowsLSPEnvPaths() (string, string, error) {
	bundleRoot := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR"))
	manifestPath := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_LSP_MANIFEST"))
	if bundleRoot == "" || manifestPath == "" {
		return "", "", errors.New("Windows shared gopls requires the packaged LSP bundle contract")
	}
	if !filepath.IsAbs(bundleRoot) || !filepath.IsAbs(manifestPath) {
		return "", "", errors.New("Windows LSP bundle paths must be absolute")
	}
	return bundleRoot, manifestPath, nil
}

// runtimeServerReadWindowsLSPManifest 使用有界严格 JSON 解码器拒绝未知字段和尾随值。
func runtimeServerReadWindowsLSPManifest(path string) (runtimeServerWindowsLSPManifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return runtimeServerWindowsLSPManifest{}, fmt.Errorf("stat Windows LSP manifest: %w", err)
	}
	if info.Size() <= 0 || info.Size() > runtimeServerWindowsGoplsManifestMaxSize {
		return runtimeServerWindowsLSPManifest{}, errors.New("Windows LSP manifest size is invalid")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return runtimeServerWindowsLSPManifest{}, fmt.Errorf("read Windows LSP manifest: %w", err)
	}
	var manifest runtimeServerWindowsLSPManifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("decode Windows LSP manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest, errors.New("Windows LSP manifest has trailing JSON payload")
	}
	if err := runtimeServerValidateWindowsLSPManifest(manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// runtimeServerValidateWindowsLSPManifest 校验 Windows 打包器当前发布的顶层清单版本与轮廓。
func runtimeServerValidateWindowsLSPManifest(manifest runtimeServerWindowsLSPManifest) error {
	if manifest.SchemaVersion != 1 || manifest.BundlePath != "lsp" {
		return errors.New("Windows LSP manifest schema is invalid")
	}
	if manifest.Profile != "standard" && manifest.Profile != "full" {
		return errors.New("Windows LSP manifest profile is invalid")
	}
	if len(manifest.Servers) == 0 {
		return errors.New("Windows LSP manifest has no servers")
	}
	return nil
}

// runtimeServerResolveWindowsGoplsServer 校验 gopls 清单字段并返回固定包内目标。
func runtimeServerResolveWindowsGoplsServer(bundleRoot string, server runtimeServerWindowsLSPServer) (string, string, string, error) {
	version := strings.TrimSpace(server.Version)
	if version == "" || version != server.Version {
		return "", "", "", errors.New("Windows gopls manifest version is invalid")
	}
	expectedSHA, err := runtimeServerCanonicalWindowsSHA256(server.SHA256)
	if err != nil {
		return "", "", "", err
	}
	relative, err := runtimeServerCanonicalWindowsGoplsRelativePath(server.Path)
	if err != nil {
		return "", "", "", err
	}
	path, err := runtimeServerCanonicalWindowsFile(filepath.Join(bundleRoot, relative))
	if err != nil {
		return "", "", "", fmt.Errorf("resolve Windows gopls executable: %w", err)
	}
	if !runtimeServerSameWindowsPath(path, filepath.Join(bundleRoot, "bin", "gopls.exe")) {
		return "", "", "", errors.New("Windows gopls manifest path is not the fixed bundled executable")
	}
	return path, version, expectedSHA, nil
}

// runtimeServerCanonicalWindowsGoplsRelativePath 拒绝绝对路径、卷路径与目录逃逸。
func runtimeServerCanonicalWindowsGoplsRelativePath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || filepath.IsAbs(trimmed) || filepath.VolumeName(trimmed) != "" {
		return "", errors.New("Windows gopls manifest path is invalid")
	}
	clean := filepath.Clean(filepath.FromSlash(trimmed))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("Windows gopls manifest path escapes the bundle")
	}
	return clean, nil
}

// runtimeServerCanonicalWindowsSHA256 要求清单摘要为规范的小写 64 位十六进制。
func runtimeServerCanonicalWindowsSHA256(value string) (string, error) {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) || value != strings.TrimSpace(value) {
		return "", errors.New("Windows gopls manifest sha256 is invalid")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("Windows gopls manifest sha256 is invalid")
	}
	return value, nil
}

// runtimeServerVerifyWindowsGoplsSHA256 流式计算真实可执行文件摘要并与清单恒等比较。
func runtimeServerVerifyWindowsGoplsSHA256(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Windows gopls executable: %w", err)
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, file)
	closeErr := file.Close()
	if copyErr != nil {
		return errors.Join(fmt.Errorf("hash Windows gopls executable: %w", copyErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close Windows gopls executable after hashing: %w", closeErr)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if actual != expected {
		return errors.New("Windows gopls executable sha256 does not match the manifest")
	}
	return nil
}

// runtimeServerRequireSameWindowsFile 拒绝 PATH 名称、替代文件和链接目标。
func runtimeServerRequireSameWindowsFile(binary, expected string) error {
	binary, err := runtimeServerCanonicalWindowsFile(binary)
	if err != nil {
		return fmt.Errorf("resolve selected Windows gopls executable: %w", err)
	}
	if !runtimeServerSameWindowsPath(binary, expected) {
		return errors.New("selected Windows gopls executable does not match the trusted bundle")
	}
	return nil
}

// runtimeServerCanonicalWindowsFile 返回拒绝符号链接后的真实绝对普通文件路径。
func runtimeServerCanonicalWindowsFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("path must name a regular non-link file")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

// runtimeServerCanonicalWindowsDirectory 返回存在目录的真实绝对路径。
func runtimeServerCanonicalWindowsDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path must name a directory")
	}
	return filepath.Clean(resolved), nil
}

// runtimeServerSameWindowsPath 按 Windows 路径大小写语义比较两个规范路径。
func runtimeServerSameWindowsPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
