package codexapp

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
	codexManagedManifestName = "codex-manifest.json"
	codexManifestMaxBytes    = 1 << 20

	codexManifestKindManaged = "managed"
	codexManifestKindBundled = "bundled"
)

type codexManifest struct {
	Codex codexManifestEntry `json:"codex"`
}

type codexManifestEntry struct {
	Path          string `json:"path"`
	Version       string `json:"version"`
	AssetName     string `json:"asset_name,omitempty"`
	SourceSHA256  string `json:"source_sha256"`
	PackageSHA256 string `json:"package_sha256"`
}

func writeManagedCodexManifest(target, version, assetName, sourceSHA256, codexPath string) error {
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
	if rel != managedCodexManifestBinaryPath() {
		return fmt.Errorf("managed Codex binary path %q does not match expected %q", rel, managedCodexManifestBinaryPath())
	}
	manifest := codexManifest{Codex: codexManifestEntry{
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
	if err := os.WriteFile(filepath.Join(target, codexManagedManifestName), raw, 0o600); err != nil {
		return fmt.Errorf("write managed Codex manifest: %w", err)
	}
	return nil
}

func verifyManagedCodexInstall(ctx context.Context, target, expectedSourceSHA256 string) (string, error) {
	manifest, expectedSourceSHA256, err := readExpectedCodexManifest(target, expectedSourceSHA256)
	if err != nil {
		return "", err
	}
	relPath, packageSHA256, err := validateManagedCodexManifest(manifest, expectedSourceSHA256)
	if err != nil {
		return "", err
	}
	binaryPath := filepath.Join(target, filepath.FromSlash(relPath))
	if err := verifyCodexBinary(ctx, codexManifestKindManaged, binaryPath, packageSHA256); err != nil {
		return "", err
	}
	return binaryPath, nil
}

func verifyBundledCodexInstall(ctx context.Context, binaryPath string) error {
	manifestPath, relPath, err := bundledCodexManifestInfo(binaryPath)
	if err != nil {
		return err
	}
	manifest, err := readCodexManifest(manifestPath, codexManifestKindBundled)
	if err != nil {
		return err
	}
	packageSHA256, err := validateBundledCodexManifest(manifest, relPath)
	if err != nil {
		return err
	}
	return verifyCodexBinary(ctx, codexManifestKindBundled, binaryPath, packageSHA256)
}

func bundledCodexManifestInfo(binaryPath string) (string, string, error) {
	binaryPath = filepath.Clean(binaryPath)
	resourcesRoot := filepath.Dir(filepath.Dir(binaryPath))
	rel, err := filepath.Rel(resourcesRoot, binaryPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve bundled Codex binary path: %w", err)
	}
	return filepath.Join(resourcesRoot, codexManagedManifestName), filepath.ToSlash(rel), nil
}

func readExpectedCodexManifest(target, expectedSourceSHA256 string) (codexManifest, string, error) {
	expectedSourceSHA256, err := normalizeSHA256(expectedSourceSHA256)
	if err != nil {
		return codexManifest{}, "", fmt.Errorf("managed Codex requires pinned source checksum: %w", err)
	}
	manifest, err := readCodexManifest(filepath.Join(target, codexManagedManifestName), codexManifestKindManaged)
	if err != nil {
		return codexManifest{}, "", err
	}
	return manifest, expectedSourceSHA256, nil
}

func validateManagedCodexManifest(manifest codexManifest, expectedSourceSHA256 string) (string, string, error) {
	relPath := strings.TrimSpace(manifest.Codex.Path)
	if relPath != managedCodexManifestBinaryPath() {
		return "", "", fmt.Errorf("managed Codex manifest path = %q, want %q", relPath, managedCodexManifestBinaryPath())
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

func validateBundledCodexManifest(manifest codexManifest, expectedRelPath string) (string, error) {
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

func verifyCodexBinary(ctx context.Context, kind, binaryPath, packageSHA256 string) error {
	if !isExecutable(binaryPath) {
		return fmt.Errorf("%s Codex binary is not executable: %s", kind, binaryPath)
	}
	actualSHA256, err := sha256FileHex(binaryPath)
	if err != nil {
		return err
	}
	if actualSHA256 != packageSHA256 {
		return fmt.Errorf("%s Codex package digest mismatch for %s: expected %s, got %s", kind, binaryPath, packageSHA256, actualSHA256)
	}
	if !validCodexCLI(ctx, binaryPath) {
		return fmt.Errorf("%s Codex CLI failed app-server validation: %s", kind, binaryPath)
	}
	return nil
}

func readCodexManifest(path, kind string) (codexManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return codexManifest{}, fmt.Errorf("read %s Codex manifest: %w", kind, err)
	}
	defer func() { _ = file.Close() }()
	var manifest codexManifest
	if err := json.NewDecoder(io.LimitReader(file, codexManifestMaxBytes)).Decode(&manifest); err != nil {
		return codexManifest{}, fmt.Errorf("decode %s Codex manifest: %w", kind, err)
	}
	return manifest, nil
}

func managedCodexManifestBinaryPath() string {
	return filepath.ToSlash(filepath.Join("codex_cli_bin", "bin", codexExecutableFileName()))
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
