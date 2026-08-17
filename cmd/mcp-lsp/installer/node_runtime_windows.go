//go:build windows

package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

const (
	// WindowsNodeRuntimeVersion 是 Windows Node/npm cohort 使用的固定 Node 版本；安装不解析 latest。
	WindowsNodeRuntimeVersion  = "22.22.0"
	nodeRuntimeMinWindowsVer   = "10.0"
	nodeRuntimeMinWindowsBuild = uint32(19041)
)

// WindowsNodeRuntimeAssetFact 描述一个 Windows 原生 Node 归档及其校验和、ready-tree 路径。
type WindowsNodeRuntimeAssetFact struct {
	// PlatformKey 是 windows-arm64、windows-x64 或 windows-x86 的锁定键。
	PlatformKey string
	// Archive 是 Node 官方 Windows ZIP 归档文件名。
	Archive string
	// SHA256 是归档下载内容的固定 SHA-256 摘要。
	SHA256 string
	// NodePath 是归档内 node.exe 的相对路径。
	NodePath string
	// NPMPath 是归档内 npm.cmd 的相对路径。
	NPMPath string
}

// WindowsNodeRuntimeAssetFacts 返回固定 Node 版本的 Windows 架构资产事实；它只读，不联网、不写盘。
func WindowsNodeRuntimeAssetFacts() map[string]WindowsNodeRuntimeAssetFact {
	return map[string]WindowsNodeRuntimeAssetFact{
		"windows-arm64": {
			PlatformKey: "windows-arm64",
			Archive:     "node-v22.22.0-win-arm64.zip",
			SHA256:      "5b44fd410df7b4cd0a1891a05a7b606f8fb7d8786a94997b996a372e82478d7a",
			NodePath:    "node-v22.22.0-win-arm64/node.exe",
			NPMPath:     "node-v22.22.0-win-arm64/npm.cmd",
		},
		"windows-x64": {
			PlatformKey: "windows-x64",
			Archive:     "node-v22.22.0-win-x64.zip",
			SHA256:      "c97fa376d2becdc8863fcd3ca2dd9a83a9f3468ee7ccf7a6d076ec66a645c77a",
			NodePath:    "node-v22.22.0-win-x64/node.exe",
			NPMPath:     "node-v22.22.0-win-x64/npm.cmd",
		},
		"windows-x86": {
			PlatformKey: "windows-x86",
			Archive:     "node-v22.22.0-win-x86.zip",
			SHA256:      "5d7f6cfc50474cf784027ce9ddabf47a0198ea4b588301ab8675de8c56217247",
			NodePath:    "node-v22.22.0-win-x86/node.exe",
			NPMPath:     "node-v22.22.0-win-x86/npm.cmd",
		},
	}
}

// WindowsNodeRuntimeManifest 返回不变的 Windows Node locked manifest；resolver 只读它，不联网、不写 cache。
func WindowsNodeRuntimeManifest() WindowsLockedAssetManifest {
	assets := make(map[string]WindowsLockedAsset, 3)
	facts := WindowsNodeRuntimeAssetFacts()
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
		fact := facts["windows-"+architecture]
		assets[architecture] = WindowsLockedAsset{
			Architecture:      architecture,
			Version:           WindowsNodeRuntimeVersion,
			URL:               "https://nodejs.org/dist/v" + WindowsNodeRuntimeVersion + "/" + fact.Archive,
			SHA256:            fact.SHA256,
			Format:            WindowsLockedAssetFormatZip,
			BinaryPath:        fact.NodePath,
			MinWindowsVersion: nodeRuntimeMinWindowsVer,
			MinWindowsBuild:   nodeRuntimeMinWindowsBuild,
		}
	}
	return WindowsLockedAssetManifest{Name: "node-runtime", Assets: assets}
}

// WindowsNodeRuntimeAssetForPlatform 按 Windows NativeArch 精确选择 Node 资产；不读 PATH、不跨架构回退。
func WindowsNodeRuntimeAssetForPlatform(platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	return WindowsNodeRuntimeManifest().AssetForPlatform(platform)
}

// WindowsNodeRuntimePaths 保存 Windows Node/npm ready-tree、cohort prefix 与显式 shim 目录的绝对路径。
type WindowsNodeRuntimePaths struct {
	// NodePath 是已校验 ready tree 中 node.exe 的绝对路径。
	NodePath string
	// NPMPath 是已校验 ready tree 中 npm.cmd 的绝对路径。
	NPMPath string
	// NodeDir 是 node.exe 所在的不可变 ready-tree 目录。
	NodeDir string
	// Prefix 是 npm exact package cohort 的可变安装前缀；写盘只由 InstallAction 负责。
	Prefix string
	// BinDir 是 npm cohort 的 .bin 目录；它不能被当作 PATH fallback。
	BinDir string
}

// WindowsNodeRuntime 负责 Windows Node 归档的确定性校验、ready-tree 复验和 npm cohort 生命周期。
type WindowsNodeRuntime struct {
	cache    *WindowsAssetCache
	manifest WindowsLockedAssetManifest

	mu    sync.Mutex
	paths WindowsNodeRuntimePaths
}

// NewWindowsNodeRuntime 创建绑定 Windows LSP asset cache 的 Node runtime；创建失败立即返回，不联网。
func NewWindowsNodeRuntime(productRoot string, client *http.Client) (*WindowsNodeRuntime, error) {
	cache, err := NewWindowsLSPAssetCache(productRoot, client)
	if err != nil {
		return nil, err
	}
	return NewWindowsNodeRuntimeWithAssetCache(cache)
}

// NewWindowsNodeRuntimeWithAssetCache 创建绑定显式 Windows asset cache 的 Node runtime；nil cache 立即失败。
func NewWindowsNodeRuntimeWithAssetCache(cache *WindowsAssetCache) (*WindowsNodeRuntime, error) {
	if cache == nil {
		return nil, errors.New("Windows Node runtime asset cache is nil")
	}
	manifest := WindowsNodeRuntimeManifest()
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate Windows Node runtime manifest: %w", err)
	}
	return &WindowsNodeRuntime{cache: cache, manifest: manifest}, nil
}

// Ensure 校验并按 Windows NativeArch materialize Node asset；联网、写盘、npm 安装均在受控生命周期内完成。
func (r *WindowsNodeRuntime) Ensure(ctx context.Context) (WindowsNodeRuntimePaths, error) {
	if r == nil || r.cache == nil {
		return WindowsNodeRuntimePaths{}, errors.New("Windows Node runtime is nil")
	}
	if ctx == nil {
		return WindowsNodeRuntimePaths{}, errors.New("Windows Node runtime context is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.paths.NodePath != "" {
		platform, err := DetectWindowsHostPlatform()
		if err != nil {
			return WindowsNodeRuntimePaths{}, fmt.Errorf("detect host platform for cached Node runtime: %w", err)
		}
		expected, err := r.expectedPathsForPlatform(platform)
		if err != nil {
			return WindowsNodeRuntimePaths{}, err
		}
		verifiedNodePath, err := r.cache.EnsureForPlatform(ctx, r.manifest, platform)
		if err != nil {
			return WindowsNodeRuntimePaths{}, fmt.Errorf("verify cached locked Node asset: %w", err)
		}
		if filepath.Clean(verifiedNodePath) != filepath.Clean(expected.NodePath) || filepath.Clean(verifiedNodePath) != filepath.Clean(r.paths.NodePath) {
			return WindowsNodeRuntimePaths{}, fmt.Errorf("cached locked Node asset path changed from %q to %q", r.paths.NodePath, verifiedNodePath)
		}
		if err := r.validateCachedPaths(r.paths); err != nil {
			return WindowsNodeRuntimePaths{}, err
		}
		nodeProcessDir, err := windowsShortProcessPath(r.paths.NodeDir)
		if err != nil {
			return WindowsNodeRuntimePaths{}, err
		}
		binProcessDir, err := windowsShortProcessPath(r.paths.BinDir)
		if err != nil {
			return WindowsNodeRuntimePaths{}, err
		}
		if err := runtimeenv.PrependWindowsRuntimePathEntries(nodeProcessDir, binProcessDir); err != nil {
			return WindowsNodeRuntimePaths{}, fmt.Errorf("republish explicit Node runtime PATH entries: %w", err)
		}
		return r.paths, nil
	}

	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return WindowsNodeRuntimePaths{}, fmt.Errorf("detect host platform for Node runtime: %w", err)
	}
	expected, err := r.expectedPathsForPlatform(platform)
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	asset, err := WindowsNodeRuntimeAssetForPlatform(platform)
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	nodePath, err := r.cache.EnsureForPlatform(ctx, r.manifest, platform)
	if err != nil {
		return WindowsNodeRuntimePaths{}, fmt.Errorf("ensure locked Node %s asset: %w", asset.Architecture, err)
	}
	if filepath.Clean(nodePath) != filepath.Clean(expected.NodePath) {
		return WindowsNodeRuntimePaths{}, fmt.Errorf("locked Node asset path changed from expected %q to %q", expected.NodePath, nodePath)
	}
	nodeDir := filepath.Dir(nodePath)
	npmPath := filepath.Join(nodeDir, "npm.cmd")
	if err := requireNodeRuntimeFile(nodePath, "node.exe"); err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	if err := requireNodeRuntimeFile(npmPath, "npm.cmd"); err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	prefix := expected.Prefix
	if err := ensureDirectoryNoSymlink(filepath.Join(prefix, "node_modules", ".bin")); err != nil {
		return WindowsNodeRuntimePaths{}, fmt.Errorf("prepare Node npm cohort prefix %q: %w", prefix, err)
	}
	binDir := filepath.Join(prefix, "node_modules", ".bin")
	nodeProcessDir, err := windowsShortProcessPath(nodeDir)
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	binProcessDir, err := windowsShortProcessPath(binDir)
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	if err := runtimeenv.PrependWindowsRuntimePathEntries(nodeProcessDir, binProcessDir); err != nil {
		return WindowsNodeRuntimePaths{}, fmt.Errorf("publish explicit Node runtime PATH entries: %w", err)
	}
	r.paths = WindowsNodeRuntimePaths{NodePath: nodePath, NPMPath: npmPath, NodeDir: nodeDir, Prefix: prefix, BinDir: binDir}
	return r.paths, nil
}

// ExpectedPaths 只计算 Windows Node/npm 的绝对路径，不下载、不建目录、不改 PATH；适用于 check-only resolver。
func (r *WindowsNodeRuntime) ExpectedPaths() (WindowsNodeRuntimePaths, error) {
	if r == nil || r.cache == nil {
		return WindowsNodeRuntimePaths{}, errors.New("Windows Node runtime is nil")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return WindowsNodeRuntimePaths{}, fmt.Errorf("detect host platform for expected Node runtime paths: %w", err)
	}
	return r.expectedPathsForPlatform(platform)
}

// ValidateExactPackages 只读校验 npm cohort 的顶层 package.json 精确版本；不会联网、写盘或修改 PATH。
func (r *WindowsNodeRuntime) ValidateExactPackages(ctx context.Context, packages []string) error {
	if r == nil || r.cache == nil {
		return errors.New("Windows Node runtime is nil")
	}
	if ctx == nil {
		return errors.New("Windows Node runtime context is nil")
	}
	if len(packages) == 0 {
		return errors.New("exact npm package list is empty")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	paths, err := r.ExpectedPaths()
	if err != nil {
		return err
	}
	for _, specification := range packages {
		name, wantVersion, err := parseExactNPMPackageSpecification(specification)
		if err != nil {
			return err
		}
		packageJSONPath := filepath.Join(paths.Prefix, "node_modules", filepath.FromSlash(name), "package.json")
		if err := requireNodeRuntimeFile(packageJSONPath, "npm package metadata "+name); err != nil {
			return err
		}
		contents, err := os.ReadFile(packageJSONPath)
		if err != nil {
			return fmt.Errorf("read npm package metadata %q: %w", packageJSONPath, err)
		}
		var metadata struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(contents, &metadata); err != nil {
			return fmt.Errorf("decode npm package metadata %q: %w", packageJSONPath, err)
		}
		if strings.TrimSpace(metadata.Version) != wantVersion {
			return fmt.Errorf("npm package %s version %q does not match exact %q", name, metadata.Version, wantVersion)
		}
	}
	return nil
}

func parseExactNPMPackageSpecification(specification string) (string, string, error) {
	specification = strings.TrimSpace(specification)
	if specification == "" {
		return "", "", errors.New("exact npm package specification is empty")
	}
	separator := strings.LastIndexByte(specification, '@')
	if separator <= 0 || separator == len(specification)-1 || (specification[0] == '@' && separator == 1) {
		return "", "", fmt.Errorf("exact npm package specification %q is invalid", specification)
	}
	name := specification[:separator]
	version := specification[separator+1:]
	if strings.HasPrefix(name, "@") {
		if strings.Count(name, "/") != 1 || strings.ContainsAny(name, "\\\t\r\n") {
			return "", "", fmt.Errorf("exact scoped npm package specification %q is invalid", specification)
		}
	} else if strings.ContainsAny(name, "/\\\t\r\n") {
		return "", "", fmt.Errorf("exact npm package specification %q is invalid", specification)
	}
	if strings.ContainsAny(version, "\\/\t\r\n") {
		return "", "", fmt.Errorf("exact npm package version in %q is invalid", specification)
	}
	return name, version, nil
}

func (r *WindowsNodeRuntime) expectedPathsForPlatform(platform WindowsHostPlatform) (WindowsNodeRuntimePaths, error) {
	asset, err := WindowsNodeRuntimeAssetForPlatform(platform)
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	assetRoot := filepath.Join(r.cache.Root(), cacheSegment(r.manifest.Name), cacheSegment(asset.Version), asset.Architecture, strings.ToLower(asset.SHA256))
	nodePath := filepath.Join(assetRoot, "ready", filepath.FromSlash(asset.BinaryPath))
	nodeDir := filepath.Dir(nodePath)
	prefix := filepath.Join(r.cache.Root(), "npm-cohort", cacheSegment(asset.Version), asset.Architecture, strings.ToLower(asset.SHA256))
	return WindowsNodeRuntimePaths{
		NodePath: nodePath,
		NPMPath:  filepath.Join(nodeDir, "npm.cmd"),
		NodeDir:  nodeDir,
		Prefix:   prefix,
		BinDir:   filepath.Join(prefix, "node_modules", ".bin"),
	}, nil
}

func (r *WindowsNodeRuntime) validateCachedPaths(paths WindowsNodeRuntimePaths) error {
	if err := requireNodeRuntimeFile(paths.NodePath, "node.exe"); err != nil {
		return fmt.Errorf("cached Node runtime is no longer valid: %w", err)
	}
	if err := requireNodeRuntimeFile(paths.NPMPath, "npm.cmd"); err != nil {
		return fmt.Errorf("cached Node runtime is no longer valid: %w", err)
	}
	if err := requireNodeRuntimeDirectory(paths.BinDir, "npm cohort bin"); err != nil {
		return fmt.Errorf("cached Node npm cohort bin directory is no longer valid: %w", err)
	}
	return nil
}

// NPMCommand 返回已校验的 npm.cmd 绝对路径，并发布显式 Node runtime 环境；不通过 PATH 解析。
func (r *WindowsNodeRuntime) NPMCommand(ctx context.Context) (string, error) {
	paths, err := r.Ensure(ctx)
	if err != nil {
		return "", err
	}
	return windowsShortProcessPath(paths.NPMPath)
}

// BinaryPath 返回指定 LSP 可执行 shim 的绝对 .bin 路径；只读计算，不联网、不写盘。
func (r *WindowsNodeRuntime) BinaryPath(ctx context.Context, binaryName string) (string, error) {
	binaryName = strings.TrimSpace(binaryName)
	if binaryName == "" {
		return "", errors.New("Node npm cohort binary name is empty")
	}
	if ctx == nil {
		return "", errors.New("Windows Node runtime context is nil")
	}
	paths, err := r.ExpectedPaths()
	if err != nil {
		return "", err
	}
	if filepath.Ext(binaryName) == "" {
		binaryName += ".cmd"
	}
	return filepath.Join(paths.BinDir, binaryName), nil
}

func requireNodeRuntimeFile(path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("locked Node runtime is missing %s at %q: %w", name, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("locked Node runtime %s is not a regular non-empty file: %q", name, path)
	}
	return nil
}

func requireNodeRuntimeDirectory(path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("locked Node runtime is missing %s directory at %q: %w", name, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("locked Node runtime %s path is not a real directory: %q", name, path)
	}
	return nil
}
