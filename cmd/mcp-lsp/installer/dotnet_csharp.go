package installer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const defaultDotnetCSharpMaxArtifactBytes int64 = 2 << 30

// DotnetCSharpInstallerConfig 描述 Linux C# 语言服务所需的受管 .NET 归档。
// 三个 artifact 必须由调用方 manifest 固定版本、HTTPS URL 和 SHA256。
type DotnetCSharpInstallerConfig struct {
	InstallRoot            string
	HTTPClient             *http.Client
	MaxArtifactBytes       int64
	RuntimeArtifact        NativeArtifactSpec
	SDKArtifact            NativeArtifactSpec
	LanguageServerArtifact NativeArtifactSpec
}

// DotnetCSharpInstaller 安装独立 .NET runtime、SDK 和 csharp-ls NuGet 包。
// 它只返回不依赖 PATH 或系统 dotnet 的绝对 launcher。
type DotnetCSharpInstaller struct {
	mu     sync.Mutex
	root   string
	native *NativeArtifactInstaller
	cfg    DotnetCSharpInstallerConfig
}

// NewDotnetCSharpInstaller 创建一个要求绝对安装根的 C# 受管安装器。
func NewDotnetCSharpInstaller(cfg DotnetCSharpInstallerConfig) (*DotnetCSharpInstaller, error) {
	root := strings.TrimSpace(cfg.InstallRoot)
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("managed dotnet C# install root must be absolute: %q", cfg.InstallRoot)
	}
	if cfg.MaxArtifactBytes == 0 {
		cfg.MaxArtifactBytes = defaultDotnetCSharpMaxArtifactBytes
	}
	if cfg.MaxArtifactBytes < 1 {
		return nil, errors.New("managed dotnet C# max artifact bytes must be positive")
	}
	native, err := NewNativeArtifactInstaller(NativeArtifactInstallerConfig{
		InstallRoot:      root,
		HTTPClient:       cfg.HTTPClient,
		MaxArtifactBytes: cfg.MaxArtifactBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("create managed dotnet C# artifact installer: %w", err)
	}
	return &DotnetCSharpInstaller{root: filepath.Clean(root), native: native, cfg: cfg}, nil
}

// Install 下载、校验、解包并发布 .NET runtime/SDK 与 csharp-ls launcher。
func (i *DotnetCSharpInstaller) Install(ctx context.Context) (string, error) {
	if i == nil || i.native == nil {
		return "", errors.New("managed dotnet C# installer is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	if _, _, err := i.installOrReuseArtifact(ctx, i.cfg.RuntimeArtifact, true); err != nil {
		return "", fmt.Errorf("install managed .NET runtime: %w", err)
	}
	sdk, _, err := i.installOrReuseArtifact(ctx, i.cfg.SDKArtifact, true)
	if err != nil {
		return "", fmt.Errorf("install managed .NET SDK: %w", err)
	}
	languageServer, _, err := i.installOrReuseArtifact(ctx, i.cfg.LanguageServerArtifact, false)
	if err != nil {
		return "", fmt.Errorf("install managed csharp-ls NuGet package: %w", err)
	}
	if err := ensureManagedDotnetLauncher(languageServer.LauncherPath, sdk.LauncherPath, sdk.BinaryPath, languageServer.BinaryPath); err != nil {
		return "", fmt.Errorf("write managed csharp-ls launcher: %w", err)
	}
	return languageServer.LauncherPath, nil
}

func (i *DotnetCSharpInstaller) installOrReuseArtifact(ctx context.Context, spec NativeArtifactSpec, requireGenericLauncher bool) (NativeInstallResult, bool, error) {
	normalized, err := normalizeNativeArtifactSpec(spec)
	if err != nil {
		return NativeInstallResult{}, false, err
	}
	finalDir := filepath.Join(i.root, normalized.name, normalized.version)
	_, statErr := os.Lstat(finalDir)
	if errors.Is(statErr, os.ErrNotExist) {
		result, installErr := i.native.InstallArtifact(ctx, spec)
		return result, true, installErr
	}
	if statErr != nil {
		return NativeInstallResult{}, false, fmt.Errorf("inspect existing managed artifact %s: %w", finalDir, statErr)
	}
	result := NativeInstallResult{
		Name:         normalized.name,
		Version:      normalized.version,
		InstallDir:   finalDir,
		BinaryPath:   filepath.Join(finalDir, "payload", filepath.FromSlash(normalized.binaryPath)),
		LauncherPath: filepath.Join(finalDir, "launcher", normalized.launcherName),
		SHA256:       normalized.sha256,
	}
	if err := validateExistingManagedArtifact(result, requireGenericLauncher); err != nil {
		return NativeInstallResult{}, false, fmt.Errorf("existing managed artifact %s failed validation: %w", finalDir, err)
	}
	return result, false, nil
}

// validateExistingManagedArtifact 复核已发布 artifact 的目录、可执行文件和 launcher 绑定。
func validateExistingManagedArtifact(result NativeInstallResult, requireGenericLauncher bool) error {
	if err := rejectExistingSymlinkComponents(result.InstallDir); err != nil {
		return err
	}
	info, err := os.Lstat(result.InstallDir)
	if err != nil {
		return fmt.Errorf("stat install directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("install directory is not a real directory")
	}
	for label, path := range map[string]string{"binary": result.BinaryPath, "launcher": result.LauncherPath} {
		if err := validateManagedExecutable(label, path); err != nil {
			return err
		}
	}
	if requireGenericLauncher {
		content, err := os.ReadFile(result.LauncherPath)
		if err != nil {
			return fmt.Errorf("read generic launcher: %w", err)
		}
		expected := "exec " + shellQuote(result.BinaryPath) + " \"$@\""
		if !strings.Contains(string(content), expected) {
			return fmt.Errorf("generic launcher does not target managed binary %q", result.BinaryPath)
		}
	}
	return nil
}

// validateManagedExecutable 拒绝符号链接并要求目标是可执行普通文件。
func validateManagedExecutable(label, target string) error {
	if err := rejectExistingSymlinkComponents(target); err != nil {
		return fmt.Errorf("%s path: %w", label, err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("stat %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", label)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", label)
	}
	return nil
}

// ensureManagedDotnetLauncher 保留已正确绑定的 launcher，并原子修正旧通用 launcher。
func ensureManagedDotnetLauncher(launcherPath, dotnetLauncher, dotnetBinary, assemblyPath string) error {
	content, err := os.ReadFile(launcherPath)
	if err != nil {
		return err
	}
	dotnetRoot := filepath.Dir(dotnetBinary)
	managedMarkers := []string{
		"export DOTNET_ROOT=" + shellQuote(dotnetRoot),
		"export PATH=" + shellQuote(dotnetRoot),
		"export DOTNET_MULTILEVEL_LOOKUP=0",
		"exec " + shellQuote(dotnetLauncher) + " " + shellQuote(assemblyPath) + " \"$@\"",
	}
	managed := true
	for _, marker := range managedMarkers {
		if !strings.Contains(string(content), marker) {
			managed = false
			break
		}
	}
	if managed {
		return nil
	}
	expectedGeneric := "exec " + shellQuote(assemblyPath) + " \"$@\""
	if !strings.Contains(string(content), expectedGeneric) {
		return errors.New("existing csharp-ls launcher is neither managed nor the installer-generated launcher")
	}
	return writeManagedDotnetLauncher(launcherPath, dotnetLauncher, dotnetBinary, assemblyPath)
}

// writeManagedDotnetLauncher 写入完全受管的 dotnet 环境和 csharp-ls 启动命令。
func writeManagedDotnetLauncher(launcherPath, dotnetLauncher, dotnetBinary, assemblyPath string) error {
	paths := map[string]string{
		"managed csharp-ls launcher": launcherPath,
		"managed dotnet launcher":    dotnetLauncher,
		"managed dotnet binary":      dotnetBinary,
		"managed csharp-ls assembly": assemblyPath,
	}
	if err := validateManagedDotnetPaths(paths); err != nil {
		return err
	}
	if err := validateManagedDotnetLauncherFile(launcherPath); err != nil {
		return err
	}
	dotnetRoot := filepath.Dir(dotnetBinary)
	cliHome := filepath.Join(dotnetRoot, ".cli-home")
	nugetPackages := filepath.Join(dotnetRoot, ".nuget", "packages")
	for _, directory := range []string{cliHome, nugetPackages} {
		if err := ensureInstallDirectory(directory); err != nil {
			return fmt.Errorf("prepare managed dotnet directory %s: %w", directory, err)
		}
	}
	content := "#!/bin/sh\nset -eu\n" +
		"export DOTNET_ROOT=" + shellQuote(dotnetRoot) + "\n" +
		"export DOTNET_ROOT_X64=" + shellQuote(dotnetRoot) + "\n" +
		"export PATH=" + shellQuote(dotnetRoot) + "\n" +
		"export DOTNET_MULTILEVEL_LOOKUP=0\n" +
		"export DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1\n" +
		"export DOTNET_CLI_HOME=" + shellQuote(cliHome) + "\n" +
		"export NUGET_PACKAGES=" + shellQuote(nugetPackages) + "\n" +
		"exec " + shellQuote(dotnetLauncher) + " " + shellQuote(assemblyPath) + " \"$@\"\n"
	return overwriteManagedDotnetLauncher(launcherPath, content)
}

// validateManagedDotnetLauncherFile 拒绝符号链接并要求既有 launcher 是普通文件。
func validateManagedDotnetLauncherFile(launcherPath string) error {
	if err := rejectExistingSymlinkComponents(launcherPath); err != nil {
		return fmt.Errorf("validate managed csharp-ls launcher path: %w", err)
	}
	info, err := os.Lstat(launcherPath)
	if err != nil {
		return fmt.Errorf("inspect managed csharp-ls launcher: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("managed csharp-ls launcher is not a regular file")
	}
	return nil
}

// overwriteManagedDotnetLauncher 写入、同步并恢复 launcher 的私有可执行权限。
func overwriteManagedDotnetLauncher(launcherPath, content string) error {
	file, err := os.OpenFile(launcherPath, os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return fmt.Errorf("open managed csharp-ls launcher: %w", err)
	}
	_, writeErr := file.WriteString(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write managed csharp-ls launcher: %w", writeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync managed csharp-ls launcher: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close managed csharp-ls launcher: %w", closeErr)
	}
	if err := os.Chmod(launcherPath, 0o700); err != nil {
		return fmt.Errorf("mark managed csharp-ls launcher executable: %w", err)
	}
	return nil
}

// validateManagedDotnetPaths 要求 launcher、runtime 和 assembly 全部使用显式绝对路径。
func validateManagedDotnetPaths(paths map[string]string) error {
	for label, value := range paths {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || !filepath.IsAbs(trimmed) {
			return fmt.Errorf("%s path must be absolute: %q", label, value)
		}
	}
	return nil
}
