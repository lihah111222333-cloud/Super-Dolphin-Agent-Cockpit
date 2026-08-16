//go:build linux && amd64

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

const (
	linuxDotnetRuntimeVersion = "9.0.4"
	linuxDotnetSDKVersion     = "9.0.203"
	linuxCSharpLSVersion      = "0.20.0"
)

// linuxCSharpManagedManifest 固定 Linux amd64 官方 .NET 与 csharp-ls artifacts。
type linuxCSharpManagedManifest struct {
	runtime installer.NativeArtifactSpec
	sdk     installer.NativeArtifactSpec
	server  installer.NativeArtifactSpec
}

func linuxCSharpManagedManifests() linuxCSharpManagedManifest {
	return linuxCSharpManagedManifest{
		runtime: installer.NativeArtifactSpec{
			Name:         "dotnet-runtime-linux-x64",
			Version:      linuxDotnetRuntimeVersion,
			URL:          "https://builds.dotnet.microsoft.com/dotnet/Runtime/9.0.4/dotnet-runtime-9.0.4-linux-x64.tar.gz",
			SHA256:       "9ad9909313b5214bbd0776a313b82fa482447721c3db56b7086c487fc238f462",
			Format:       installer.NativeArtifactFormatTarGz,
			BinaryPath:   "dotnet",
			LauncherName: "dotnet",
		},
		sdk: installer.NativeArtifactSpec{
			Name:         "dotnet-sdk-linux-x64",
			Version:      linuxDotnetSDKVersion,
			URL:          "https://builds.dotnet.microsoft.com/dotnet/Sdk/9.0.203/dotnet-sdk-9.0.203-linux-x64.tar.gz",
			SHA256:       "c7e99b0060a274f31a29ec5e159c7133478bb30dca0366e1c5617976e6de23a3",
			Format:       installer.NativeArtifactFormatTarGz,
			BinaryPath:   "dotnet",
			LauncherName: "dotnet",
		},
		server: installer.NativeArtifactSpec{
			Name:         "csharp-ls",
			Version:      linuxCSharpLSVersion,
			URL:          "https://api.nuget.org/v3-flatcontainer/csharp-ls/0.20.0/csharp-ls.0.20.0.nupkg",
			SHA256:       "3aa6a91990a7e2af12659879363f062dac5c66835d30e78d54fc38ddbc4c120d",
			Format:       installer.NativeArtifactFormatZip,
			BinaryPath:   "tools/net9.0/any/CSharpLanguageServer.dll",
			LauncherName: "csharp-ls",
		},
	}
}

type linuxCSharpInstallRootResolver func() (string, error)

// resolveLinuxCSharpInstallRoot 返回私有、绝对且不依赖系统 dotnet 的缓存根。
func resolveLinuxCSharpInstallRoot() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve Linux C# managed cache directory: %w", err)
	}
	cacheDir = strings.TrimSpace(cacheDir)
	if cacheDir == "" || !filepath.IsAbs(cacheDir) {
		return "", fmt.Errorf("resolve Linux C# managed cache directory is not absolute: %q", cacheDir)
	}
	return filepath.Join(cacheDir, "super-agent-v3", "mcp-lsp", "csharp"), nil
}

// registerLinuxCSharpInstaller 注册唯一的 Linux C# managed installer。
func registerLinuxCSharpInstaller(inst *installer.Provider) error {
	return registerLinuxCSharpInstallerWithResolver(inst, resolveLinuxCSharpInstallRoot, nil)
}

func registerLinuxCSharpInstallerWithResolver(
	inst *installer.Provider,
	resolveRoot linuxCSharpInstallRootResolver,
	httpClient *http.Client,
	manifest ...linuxCSharpManagedManifest,
) error {
	if inst == nil {
		return errors.New("Linux C# managed installer provider is nil")
	}
	if resolveRoot == nil {
		return errors.New("Linux C# managed install root resolver is nil")
	}
	root, err := resolveRoot()
	if err != nil {
		return fmt.Errorf("resolve Linux C# managed install root: %w", err)
	}
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("Linux C# managed install root must be absolute: %q", root)
	}
	selected := linuxCSharpManagedManifests()
	if len(manifest) > 1 {
		return errors.New("Linux C# managed installer accepts at most one manifest")
	}
	if len(manifest) == 1 {
		selected = manifest[0]
	}
	managed, err := installer.NewDotnetCSharpInstaller(installer.DotnetCSharpInstallerConfig{
		InstallRoot:            root,
		HTTPClient:             httpClient,
		RuntimeArtifact:        selected.runtime,
		SDKArtifact:            selected.sdk,
		LanguageServerArtifact: selected.server,
		MaxArtifactBytes:       2 << 30,
	})
	if err != nil {
		return fmt.Errorf("create Linux C# managed installer: %w", err)
	}
	launcherPath := filepath.Join(root, selected.server.Name, selected.server.Version, "launcher", selected.server.LauncherName)
	if !filepath.IsAbs(launcherPath) {
		return fmt.Errorf("Linux C# managed launcher path is not absolute: %q", launcherPath)
	}
	inst.Register("csharp", installer.InstallerConfig{
		BinaryName:          "csharp-ls",
		AllowInstallCommand: true,
		ManagedBinaryPath:   launcherPath,
		ManagedInstall: func(ctx context.Context) (string, error) {
			return managed.Install(ctx)
		},
	})
	return nil
}
