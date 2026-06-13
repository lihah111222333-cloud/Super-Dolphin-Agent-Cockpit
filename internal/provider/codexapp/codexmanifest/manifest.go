package codexmanifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	Name     = "codex-manifest.json"
	maxBytes = 1 << 20

	kindManaged = "managed"
	kindBundled = "bundled"
)

type Verifier interface {
	IsExecutable(path string) bool
	ValidCLI(ctx context.Context, path string) bool
}

type File struct {
	Codex manifestEntry `json:"codex"`
}

type manifestEntry struct {
	Path          string `json:"path"`
	Version       string `json:"version"`
	AssetName     string `json:"asset_name,omitempty"`
	SourceSHA256  string `json:"source_sha256"`
	PackageSHA256 string `json:"package_sha256"`
}

// WriteManaged 写入managed。
func WriteManaged(target, version, assetName, sourceSHA256, codexPath, executableName string) error {
	sourceSHA256, err := normalizeSHA256(sourceSHA256)
	if err != nil {
		return fmt.Errorf("managed Codex source checksum: %w", err)
	}
	packageSHA256, err := sha256FileHex(codexPath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(target, codexPath)
	if err != nil {
		return fmt.Errorf("resolve managed Codex binary path: %w", err)
	}
	rel = filepath.ToSlash(rel)
	if rel != ManagedBinaryPath(executableName) {
		return fmt.Errorf("managed Codex binary path %q does not match expected %q", rel, ManagedBinaryPath(executableName))
	}
	manifest := File{Codex: manifestEntry{
		Path:          rel,
		Version:       strings.TrimSpace(version),
		AssetName:     strings.TrimSpace(assetName),
		SourceSHA256:  sourceSHA256,
		PackageSHA256: packageSHA256,
	}}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed Codex manifest: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(target, Name), raw, 0o600); err != nil {
		return fmt.Errorf("write managed Codex manifest: %w", err)
	}
	return nil
}

// VerifyManaged 验证managed。
func VerifyManaged(ctx context.Context, target, expectedSourceSHA256, executableName string, verifier Verifier) (string, error) {
	manifest, expectedSourceSHA256, err := readExpected(target, expectedSourceSHA256)
	if err != nil {
		return "", err
	}
	relPath, packageSHA256, err := validateManaged(manifest, expectedSourceSHA256, executableName)
	if err != nil {
		return "", err
	}
	binaryPath := filepath.Join(target, filepath.FromSlash(relPath))
	if err := verifyBinary(ctx, kindManaged, binaryPath, packageSHA256, verifier); err != nil {
		return "", err
	}
	return binaryPath, nil
}

// VerifyBundled 验证bundled。
func VerifyBundled(ctx context.Context, binaryPath string, verifier Verifier) error {
	manifestPath, relPath, err := bundledInfo(binaryPath)
	if err != nil {
		return err
	}
	manifest, err := read(manifestPath, kindBundled)
	if err != nil {
		return err
	}
	packageSHA256, err := validateBundled(manifest, relPath)
	if err != nil {
		return err
	}
	return verifyBinary(ctx, kindBundled, binaryPath, packageSHA256, verifier)
}

func bundledInfo(binaryPath string) (string, string, error) {
	binaryPath = filepath.Clean(binaryPath)
	resourcesRoot := filepath.Dir(filepath.Dir(binaryPath))
	rel, err := filepath.Rel(resourcesRoot, binaryPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve bundled Codex binary path: %w", err)
	}
	return filepath.Join(resourcesRoot, Name), filepath.ToSlash(rel), nil
}

func readExpected(target, expectedSourceSHA256 string) (File, string, error) {
	expectedSourceSHA256, err := normalizeSHA256(expectedSourceSHA256)
	if err != nil {
		return File{}, "", fmt.Errorf("managed Codex requires pinned source checksum: %w", err)
	}
	manifest, err := read(filepath.Join(target, Name), kindManaged)
	if err != nil {
		return File{}, "", err
	}
	return manifest, expectedSourceSHA256, nil
}

func validateManaged(manifest File, expectedSourceSHA256, executableName string) (string, string, error) {
	relPath := strings.TrimSpace(manifest.Codex.Path)
	if relPath != ManagedBinaryPath(executableName) {
		return "", "", fmt.Errorf("managed Codex manifest path = %q, want %q", relPath, ManagedBinaryPath(executableName))
	}
	sourceSHA256, err := normalizeSHA256(manifest.Codex.SourceSHA256)
	if err != nil {
		return "", "", fmt.Errorf("managed Codex manifest source_sha256: %w", err)
	}
	if sourceSHA256 != expectedSourceSHA256 {
		return "", "", fmt.Errorf("managed Codex manifest source checksum mismatch: expected %s, got %s", expectedSourceSHA256, sourceSHA256)
	}
	packageSHA256, err := normalizeSHA256(manifest.Codex.PackageSHA256)
	if err != nil {
		return "", "", fmt.Errorf("managed Codex manifest package_sha256: %w", err)
	}
	return relPath, packageSHA256, nil
}

func validateBundled(manifest File, expectedRelPath string) (string, error) {
	relPath := strings.TrimSpace(manifest.Codex.Path)
	if relPath != expectedRelPath {
		return "", fmt.Errorf("bundled Codex manifest path = %q, want %q", relPath, expectedRelPath)
	}
	packageSHA256, err := normalizeSHA256(manifest.Codex.PackageSHA256)
	if err != nil {
		return "", fmt.Errorf("bundled Codex manifest package_sha256: %w", err)
	}
	return packageSHA256, nil
}

// verifyBinary 验证二进制。
func verifyBinary(ctx context.Context, kind, binaryPath, packageSHA256 string, verifier Verifier) error {
	if verifier == nil {
		return fmt.Errorf("%s Codex verifier is required", kind)
	}
	if !verifier.IsExecutable(binaryPath) {
		return fmt.Errorf("%s Codex binary is not executable: %s", kind, binaryPath)
	}
	actualSHA256, err := sha256FileHex(binaryPath)
	if err != nil {
		return err
	}
	if actualSHA256 != packageSHA256 {
		return fmt.Errorf("%s Codex package digest mismatch for %s: expected %s, got %s", kind, binaryPath, packageSHA256, actualSHA256)
	}
	if !verifier.ValidCLI(ctx, binaryPath) {
		return fmt.Errorf("%s Codex CLI failed app-server validation: %s", kind, binaryPath)
	}
	return nil
}

func read(path, kind string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return File{}, fmt.Errorf("read %s Codex manifest: %w", kind, err)
	}
	defer func() { _ = file.Close() }()
	var manifest File
	if err := json.NewDecoder(io.LimitReader(file, maxBytes)).Decode(&manifest); err != nil {
		return File{}, fmt.Errorf("decode %s Codex manifest: %w", kind, err)
	}
	return manifest, nil
}

// ManagedBinaryPath 处理managed二进制路径。
func ManagedBinaryPath(executableName string) string {
	return filepath.ToSlash(filepath.Join("codex_cli_bin", "bin", executableName))
}

func normalizeSHA256(value string) (string, error) {
	checksum := strings.ToLower(strings.TrimSpace(value))
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("must be a 64-character hex SHA-256 checksum")
	}
	return checksum, nil
}

func sha256FileHex(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open SHA-256 input %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash SHA-256 input %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
