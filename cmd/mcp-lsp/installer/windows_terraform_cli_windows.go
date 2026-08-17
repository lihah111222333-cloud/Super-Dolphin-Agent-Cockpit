//go:build windows

package installer

import (
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
)

const (
	windowsTerraformCLIProductRoot = "terraform-cli-assets"
	windowsTerraformCLIVersion     = "1.15.6"
)

// WindowsTerraformCLIAssetForPlatform 返回 HashiCorp 官方 Terraform CLI 锁定资产。
// CLI 是 terraform-ls 的 product-private companion，不是 MCP/LSP server，且只按 NativeArch 选源。
func WindowsTerraformCLIAssetForPlatform(platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	manifest := WindowsLockedAssetManifest{Name: "terraform-cli", Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: catalogAsset(WindowsHostArchARM64, windowsTerraformCLIVersion, "https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_arm64.zip", "02820bcae3725c9c4e91deb6656e9b96ca8af9f395fc5faccc0820dd3295d6e0", WindowsLockedAssetFormatZip, "terraform.exe"),
		WindowsHostArchX64:   catalogAsset(WindowsHostArchX64, windowsTerraformCLIVersion, "https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_amd64.zip", "56b4d3a157e346f8fc1e94254d0a944e6fec81f58ddd43eb274b8e0ebb56e334", WindowsLockedAssetFormatZip, "terraform.exe"),
		WindowsHostArchX86:   catalogAsset(WindowsHostArchX86, windowsTerraformCLIVersion, "https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_386.zip", "00d51ccf53664f68bd6fb7dfa7edbc7bbff4032ff048787c096d23ece2dcc092", WindowsLockedAssetFormatZip, "terraform.exe"),
	}}
	return manifest.AssetForPlatform(platform)
}

// EnsureWindowsTerraformCLI 为 product root 物化 Terraform CLI companion；缺失时只走锁定官方资产。
func EnsureWindowsTerraformCLI(ctx context.Context, productRoot string, client *http.Client) (string, error) {
	if productRoot == "" {
		return "", errors.New("Terraform CLI product root is empty")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	asset, err := WindowsTerraformCLIAssetForPlatform(platform)
	if err != nil {
		return "", err
	}
	cache, err := NewWindowsAssetCache(filepath.Join(productRoot, "cache", windowsTerraformCLIProductRoot), client)
	if err != nil {
		return "", fmt.Errorf("create Terraform CLI companion cache: %w", err)
	}
	manifest := WindowsLockedAssetManifest{Name: "terraform-cli", Assets: map[string]WindowsLockedAsset{asset.Architecture: asset}}
	path, err := cache.EnsureForPlatform(ctx, manifest, platform)
	if err != nil {
		return "", fmt.Errorf("materialize Terraform CLI companion: %w", err)
	}
	if err := ValidateWindowsTerraformCLIExecutable(path, platform.NativeArch); err != nil {
		return "", err
	}
	return path, nil
}

// ResolveWindowsTerraformCLIPath 只读解析已发布的 product-private CLI，不联网、不创建目录、不查 PATH。
func ResolveWindowsTerraformCLIPath(productRoot string) (string, error) {
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	asset, err := WindowsTerraformCLIAssetForPlatform(platform)
	if err != nil {
		return "", err
	}
	assetDir := filepath.Join(productRoot, "cache", windowsTerraformCLIProductRoot, cacheSegment("terraform-cli"), cacheSegment(asset.Version), asset.Architecture, asset.SHA256)
	path := filepath.Join(assetDir, "ready", asset.BinaryPath)
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return "", fmt.Errorf("Terraform CLI companion is not ready: %w", err)
	}
	if err := ValidateWindowsTerraformCLIExecutable(path, platform.NativeArch); err != nil {
		return "", err
	}
	return path, nil
}

// ValidateWindowsTerraformCLIExecutable 校验 product-private Terraform CLI 的普通文件和 PE NativeArch。
func ValidateWindowsTerraformCLIExecutable(path, architecture string) error {
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return fmt.Errorf("Terraform CLI executable is invalid: %w", err)
	}
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("read Terraform CLI PE: %w", err)
	}
	defer file.Close()
	want := map[string]uint16{WindowsHostArchARM64: WindowsImageFileMachineARM64, WindowsHostArchX64: WindowsImageFileMachineAMD64, WindowsHostArchX86: WindowsImageFileMachineI386}[architecture]
	if want == 0 || file.FileHeader.Machine != want {
		return fmt.Errorf("Terraform CLI PE machine mismatch: want architecture %q machine 0x%04x, got 0x%04x", architecture, want, file.FileHeader.Machine)
	}
	return nil
}
