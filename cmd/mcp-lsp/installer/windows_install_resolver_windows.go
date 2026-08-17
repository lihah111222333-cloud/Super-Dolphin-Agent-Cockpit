//go:build windows

package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

var (
	// ErrWindowsLSPInstallCacheMiss 表示 Windows 锁定资产 ready 树缺失或不可信；
	// 该只读解析错误禁止转向 PATH，只有 Windows InstallAction 可以创建/下载 cache。
	ErrWindowsLSPInstallCacheMiss = errors.New("Windows LSP install cache entry is missing or invalid")
	// ErrWindowsNodeRuntimeInstallCacheMiss 表示 Windows Node/npm cohort 尚未发布完整
	// 资产或精确包元数据；它不会触发 resolver 的联网、建目录或 PATH 回退。
	ErrWindowsNodeRuntimeInstallCacheMiss = errors.New("Windows Node runtime install cache entry is missing or invalid")
	// ErrWindowsVCLibsDesktopInstallCacheMiss 表示按原生架构锁定的应用本地 VC++
	// ready tree 缺失或身份无效；只有 InstallAction 可以下载或修复它。
	ErrWindowsVCLibsDesktopInstallCacheMiss = errors.New("Windows VCLibs Desktop install cache entry is missing or invalid")
)

// ResolveWindowsLSPAssetPath 只读解析 Windows 原生 catalog 的精确 ready-tree 路径。
// 它按 DetectWindowsHostPlatform 的 NativeArch、WindowsVersion 和 build 选择资产，不联网、不
// 建目录、不写 cache；缺失、篡改或 typed unsupported/evidence 错误直接返回。
func ResolveWindowsLSPAssetPath(productRoot string, product WindowsLSPProduct) (string, error) {
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return "", err
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	entry, err := WindowsLSPCatalogEntryForProduct(product)
	if err != nil {
		return "", err
	}
	asset, err := entry.Manifest.AssetForPlatform(platform)
	if err != nil {
		return "", err
	}
	readyPath := filepath.Join(
		root,
		"cache",
		WindowsLSPAssetCacheSubdir,
		cacheSegment(entry.Manifest.Name),
		cacheSegment(asset.Version),
		asset.Architecture,
		strings.ToLower(asset.SHA256),
		"ready",
		filepath.FromSlash(asset.BinaryPath),
	)
	return requireWindowsResolverFile(readyPath, ErrWindowsLSPInstallCacheMiss)
}

// ResolveWindowsVCLibsDesktopAppLocalPath 只读解析并复验当前 Windows 原生架构的
// VC++ Desktop ready 目录。它不联网、不建目录、不写 cache，也不查询系统 PATH。
func ResolveWindowsVCLibsDesktopAppLocalPath(productRoot string) (string, error) {
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	return resolveWindowsVCLibsDesktopAppLocalPathForPlatform(productRoot, platform, windowsVCLibsDesktopManifest())
}

// resolveWindowsVCLibsDesktopAppLocalPathForPlatform 允许测试以固定平台和 manifest
// 复验只读缓存路径；生产入口始终传入真实宿主事实和锁定 manifest。
func resolveWindowsVCLibsDesktopAppLocalPathForPlatform(productRoot string, platform WindowsHostPlatform, manifest WindowsLockedAssetManifest) (string, error) {
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return "", err
	}
	asset, err := SelectWindowsLockedAsset(manifest, platform)
	if err != nil {
		return "", err
	}
	cacheRoot := filepath.Join(root, "cache", WindowsLSPAssetCacheSubdir)
	if err := validateWindowsInstallerPathWithinRoot(root, cacheRoot, false); err != nil {
		return "", fmt.Errorf("%w: validate Windows VCLibs cache root: %w", ErrWindowsVCLibsDesktopInstallCacheMiss, securefs.WrapErrorForPath(err, cacheRoot))
	}
	assetRoot := filepath.Join(
		cacheRoot,
		cacheSegment(manifest.Name),
		cacheSegment(asset.Version),
		asset.Architecture,
		strings.ToLower(asset.SHA256),
	)
	payloadPath := filepath.Join(assetRoot, "payload.zip")
	payloadValid, err := verifyAssetPayloadWithinRoot(cacheRoot, payloadPath, asset.SHA256)
	if err != nil {
		return "", fmt.Errorf("%w: verify locked Appx payload: %w", ErrWindowsVCLibsDesktopInstallCacheMiss, securefs.WrapErrorForPath(err, payloadPath))
	}
	if !payloadValid {
		return "", fmt.Errorf("%w: %w", ErrWindowsVCLibsDesktopInstallCacheMiss, ErrWindowsAssetChecksumMismatch)
	}
	readyRoot := filepath.Join(assetRoot, "ready")
	if err := validateWindowsInstallerPathWithinRoot(cacheRoot, readyRoot, false); err != nil {
		return "", fmt.Errorf("%w: validate Windows VCLibs ready parent chain: %w", ErrWindowsVCLibsDesktopInstallCacheMiss, securefs.WrapErrorForPath(err, readyRoot))
	}
	if err := validateWindowsVCLibsDesktopReadyRoot(readyRoot, platform.NativeArch); err != nil {
		return "", fmt.Errorf("%w: %w", ErrWindowsVCLibsDesktopInstallCacheMiss, err)
	}
	if err := validateWindowsVCLibsDesktopReadyRootAgainstPayload(payloadPath, readyRoot); err != nil {
		return "", fmt.Errorf("%w: %w", ErrWindowsVCLibsDesktopInstallCacheMiss, err)
	}
	return filepath.Clean(readyRoot), nil
}

// ResolveWindowsVCLibsDesktopAppLocalProcessPath 在只读复验后返回同一目录身份的
// 8.3 DLL 搜索路径；完整 SHA ready 路径仍是持久化事实，短路径只进入子进程环境。
func ResolveWindowsVCLibsDesktopAppLocalProcessPath(productRoot string) (string, error) {
	readyRoot, err := ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		return "", err
	}
	processRoot, err := WindowsShortProcessPathWithinRoot(productRoot, readyRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Windows VCLibs Desktop process directory: %w", err)
	}
	return processRoot, nil
}

// ResolveWindowsNodeRuntimeBinaryPath 只读解析 Windows 锁定 Node/npm cohort 的
// 精确 .bin 目标，不调用 NewWindowsAssetCache/Ensure，不联网、不写盘，也不检查 PATH。
func ResolveWindowsNodeRuntimeBinaryPath(productRoot, binaryName string) (string, error) {
	paths, err := resolveWindowsNodeRuntimePaths(productRoot)
	if err != nil {
		return "", err
	}
	binaryName = strings.TrimSpace(binaryName)
	if binaryName == "" {
		return "", errors.New("Windows Node runtime binary name is empty")
	}
	if filepath.Ext(binaryName) == "" {
		binaryName += ".cmd"
	}
	return requireWindowsResolverFile(filepath.Join(paths.BinDir, binaryName), ErrWindowsNodeRuntimeInstallCacheMiss)
}

// ResolveWindowsNodeRuntimeExecutablePath 只读解析同一 Windows Node/npm cohort 的
// node.exe 绝对路径；它只检查已发布 ready tree，禁止联网、建目录、写 cache 或 PATH 回退。
func ResolveWindowsNodeRuntimeExecutablePath(productRoot string) (string, error) {
	paths, err := resolveWindowsNodeRuntimePaths(productRoot)
	if err != nil {
		return "", err
	}
	return requireWindowsResolverFile(paths.NodePath, ErrWindowsNodeRuntimeInstallCacheMiss)
}

// ValidateWindowsNodeRuntimeExactPackages 只读检查 Windows npm cohort 的顶层 package.json
// 版本。它不下载、不建目录、不修改 PATH；包名或版本不符会阻断既有 cache 的复用。
func ValidateWindowsNodeRuntimeExactPackages(productRoot string, packages []string) error {
	paths, err := resolveWindowsNodeRuntimePaths(productRoot)
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return errors.New("exact Windows npm package list is empty")
	}
	for _, specification := range packages {
		name, wantVersion, err := parseExactNPMPackageSpecification(specification)
		if err != nil {
			return err
		}
		packageJSONPath := filepath.Join(paths.Prefix, "node_modules", filepath.FromSlash(name), "package.json")
		if err := requireNodeRuntimeFile(packageJSONPath, "npm package metadata "+name); err != nil {
			return fmt.Errorf("%w: %w", ErrWindowsNodeRuntimeInstallCacheMiss, securefs.WrapErrorForPath(err, packageJSONPath))
		}
		contents, err := os.ReadFile(packageJSONPath)
		if err != nil {
			return fmt.Errorf("read Windows npm package metadata %s: %w", securefs.RedactPath(packageJSONPath), securefs.WrapErrorForPath(err, packageJSONPath))
		}
		var metadata struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(contents, &metadata); err != nil {
			return fmt.Errorf("decode Windows npm package metadata %s: %w", securefs.RedactPath(packageJSONPath), securefs.WrapErrorForPath(err, packageJSONPath))
		}
		if strings.TrimSpace(metadata.Version) != wantVersion {
			return fmt.Errorf("%w: npm package %s version %q does not match exact %q", ErrWindowsNodeRuntimeInstallCacheMiss, name, metadata.Version, wantVersion)
		}
	}
	return nil
}

func resolveWindowsNodeRuntimePaths(productRoot string) (WindowsNodeRuntimePaths, error) {
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	asset, err := WindowsNodeRuntimeAssetForPlatform(platform)
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	manifest := WindowsNodeRuntimeManifest()
	cacheRoot := filepath.Join(root, "cache", WindowsLSPAssetCacheSubdir)
	assetRoot := filepath.Join(cacheRoot, cacheSegment(manifest.Name), cacheSegment(asset.Version), asset.Architecture, strings.ToLower(asset.SHA256))
	nodePath := filepath.Join(assetRoot, "ready", filepath.FromSlash(asset.BinaryPath))
	nodeDir := filepath.Dir(nodePath)
	prefix := filepath.Join(cacheRoot, "npm-cohort", cacheSegment(asset.Version), asset.Architecture, strings.ToLower(asset.SHA256))
	return WindowsNodeRuntimePaths{NodePath: nodePath, NPMPath: filepath.Join(nodeDir, "npm.cmd"), NodeDir: nodeDir, Prefix: prefix, BinDir: filepath.Join(prefix, "node_modules", ".bin")}, nil
}

func resolveWindowsInstallProductRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Windows install product root is required")
	}
	root, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve Windows install product root: %w", err)
	}
	return filepath.Clean(root), nil
}

func requireWindowsResolverFile(path string, sentinel error) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", sentinel, securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("%w: %q is not a regular non-empty file", sentinel, path)
	}
	return filepath.Clean(path), nil
}

// WindowsNodeRuntimeResolverContextCheck 只检查 Windows bridge resolver 的取消信号，
// 不允许 resolver 因此进行联网、建目录或 cache 写盘。
func WindowsNodeRuntimeResolverContextCheck(ctx context.Context) error {
	if ctx == nil {
		return errors.New("Windows install resolver context is nil")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
