//go:build linux && amd64

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	linuxSwiftToolchainVersion       = "6.3.3"
	linuxRubyRuntimeVersion          = "4.0.6"
	linuxSolargraphVersion           = "0.60.2"
	linuxSwiftMaxArtifactBytes int64 = 8 << 30
	linuxGemMaxBytes           int64 = 64 << 20
)

// linuxRubyGemManifest 固定一个 RubyGems 包；它不会被当作可执行 native artifact。
type linuxRubyGemManifest struct {
	name     string
	version  string
	url      string
	sha256   string
	launcher string
}

// linuxSwiftRubyManifest 固定 Swift toolchain、受管 Ruby runtime 和 Solargraph gem。
type linuxSwiftRubyManifest struct {
	swift             lspinstaller.NativeArtifactSpec
	swiftDependencies []lspinstaller.NativeArtifactSpec
	ruby              lspinstaller.NativeArtifactSpec
	rubyDependencies  []lspinstaller.NativeArtifactSpec
	solargraph        linuxRubyGemManifest
}

// linuxSwiftRubyManifests 返回固定摘要的 Swift、Ruby、依赖库和 Solargraph 清单。
func linuxSwiftRubyManifests() linuxSwiftRubyManifest {
	return linuxSwiftRubyManifest{
		swift: lspinstaller.NativeArtifactSpec{
			Name:          "swift",
			Version:       linuxSwiftToolchainVersion + "-ubuntu24.04-deps1",
			URL:           "https://download.swift.org/swift-6.3.3-release/ubuntu2404/swift-6.3.3-RELEASE/swift-6.3.3-RELEASE-ubuntu24.04.tar.gz",
			SHA256:        "da8272a5fddccd65b1529ed0e52e04526e2eadd4237d58d6220efeb973c6cd19",
			Format:        lspinstaller.NativeArtifactFormatTarGz,
			BinaryPath:    "swift-6.3.3-RELEASE-ubuntu24.04/usr/bin/sourcekit-lsp",
			LauncherName:  "sourcekit-lsp",
			AllowSymlinks: true,
		},
		swiftDependencies: []lspinstaller.NativeArtifactSpec{
			{
				Name:          "libxml2",
				Version:       "2.9.14+dfsg-1.3ubuntu3.8",
				URL:           "https://security.ubuntu.com/ubuntu/pool/main/libx/libxml2/libxml2_2.9.14+dfsg-1.3ubuntu3.8_amd64.deb",
				SHA256:        "bfd07c01d6e5ab3e327f3ca5819409b1914bbfb3f1a016d53e4dabd5f96143bb",
				Format:        lspinstaller.NativeArtifactFormatDeb,
				BinaryPath:    "usr/lib/x86_64-linux-gnu/libxml2.so.2.9.14",
				LauncherName:  "libxml2.so.2",
				AllowSymlinks: true,
			},
			{
				Name:          "libicu74",
				Version:       "74.2-1ubuntu3",
				URL:           "https://security.ubuntu.com/ubuntu/pool/main/i/icu/libicu74_74.2-1ubuntu3_amd64.deb",
				SHA256:        "d29c97a21a3e3254731cfac186e4d4e611e5e67d2c9a0430f6acfbd9acaefa2e",
				Format:        lspinstaller.NativeArtifactFormatDeb,
				BinaryPath:    "usr/lib/x86_64-linux-gnu/libicuuc.so.74.2",
				LauncherName:  "libicuuc.so.74",
				AllowSymlinks: true,
			},
		},
		ruby: lspinstaller.NativeArtifactSpec{
			Name:         "portable-ruby",
			Version:      linuxRubyRuntimeVersion,
			URL:          "https://ghcr.io/v2/homebrew/core/portable-ruby/blobs/sha256:0980099dc2668dc47bd4c85b704beb76b9406b4a85f77fdda9820d8341b40f87",
			SHA256:       "0980099dc2668dc47bd4c85b704beb76b9406b4a85f77fdda9820d8341b40f87",
			Format:       lspinstaller.NativeArtifactFormatTarGz,
			BinaryPath:   "portable-ruby/4.0.6/bin/ruby",
			LauncherName: "ruby",
		},
		rubyDependencies: []lspinstaller.NativeArtifactSpec{
			{
				Name: "libyaml-runtime", Version: "0.2.5-2build3",
				URL:    "https://archive.ubuntu.com/ubuntu/pool/main/liby/libyaml/libyaml-0-2_0.2.5-2build3_amd64.deb",
				SHA256: "bdeecb9b4309731eef49eea87cf126e343d12d3823e32f0286e3e5f18f437994",
				Format: lspinstaller.NativeArtifactFormatDeb, BinaryPath: "usr/lib/x86_64-linux-gnu/libyaml-0.so.2.0.9",
				LauncherName: "libyaml-0.so.2", AllowSymlinks: true,
			},
			{
				Name: "libyaml-dev", Version: "0.2.5-2build3",
				URL:    "https://archive.ubuntu.com/ubuntu/pool/main/liby/libyaml/libyaml-dev_0.2.5-2build3_amd64.deb",
				SHA256: "1931aae9d213c8c5324cfd14dbb123ad9f9db651ffeccdebe69a111b1a964cf6",
				Format: lspinstaller.NativeArtifactFormatDeb, BinaryPath: "usr/include/yaml.h",
				LauncherName: "yaml.h", AllowSymlinks: true,
			},
		},
		solargraph: linuxRubyGemManifest{
			name:     "solargraph",
			version:  linuxSolargraphVersion,
			url:      "https://rubygems.org/downloads/solargraph-0.60.2.gem",
			sha256:   "35c8fb31fcdbe8ccd0e0e84862a65b8deb319f86210c5966e41e2fc011e52538",
			launcher: "solargraph",
		},
	}
}

// registerLinuxSwiftRubyInstallers 注册 Linux amd64 的 Swift 和 Ruby 受管安装器。
func registerLinuxSwiftRubyInstallers(inst *lspinstaller.Provider, root string, httpClient *http.Client) error {
	return registerLinuxSwiftRubyInstallersWithManifest(inst, root, httpClient, linuxSwiftRubyManifests())
}

// registerLinuxSwiftRubyInstallersWithManifest keeps the production manifest
// fixed while allowing TLS tests to substitute byte-identical local fixtures.
// registerLinuxSwiftRubyInstallersWithManifest 使用同一受管根注册 Swift 与 Ruby installer。
func registerLinuxSwiftRubyInstallersWithManifest(inst *lspinstaller.Provider, root string, httpClient *http.Client, manifest linuxSwiftRubyManifest) error {
	if inst == nil {
		return errors.New("Linux Swift/Ruby installer provider is nil")
	}
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("Linux Swift/Ruby install root must be absolute: %q", root)
	}
	if httpClient == nil {
		httpClient = newLinuxNativeArtifactHTTPClient()
	}
	swiftInstaller, err := lspinstaller.NewNativeArtifactInstaller(lspinstaller.NativeArtifactInstallerConfig{
		InstallRoot:      root,
		HTTPClient:       httpClient,
		MaxArtifactBytes: linuxSwiftMaxArtifactBytes,
	})
	if err != nil {
		return fmt.Errorf("create managed Swift installer: %w", err)
	}
	rubyInstaller, err := lspinstaller.NewNativeArtifactInstaller(lspinstaller.NativeArtifactInstallerConfig{
		InstallRoot: root,
		HTTPClient:  httpClient,
	})
	if err != nil {
		return fmt.Errorf("create managed Ruby installer: %w", err)
	}
	registerLinuxSwiftInstaller(inst, root, swiftInstaller, manifest.swift, manifest.swiftDependencies)
	registerLinuxRubyInstaller(inst, root, rubyInstaller, manifest.ruby, manifest.rubyDependencies, manifest.solargraph, httpClient)
	return nil
}

func registerLinuxSwiftInstaller(inst *lspinstaller.Provider, root string, native *lspinstaller.NativeArtifactInstaller, spec lspinstaller.NativeArtifactSpec, dependencies []lspinstaller.NativeArtifactSpec) {
	managedPath := linuxManagedArtifactPath(root, spec)
	inst.Register(contract.LSPServiceSwift, lspinstaller.InstallerConfig{
		BinaryName:          "sourcekit-lsp",
		Language:            contract.LSPServiceSwift,
		AllowInstallCommand: true,
		ManagedOnly:         true,
		ManagedBinaryPath:   managedPath,
		ManagedInstall: func(ctx context.Context) (string, error) {
			return installLinuxSwiftSourcekit(ctx, native, root, spec, dependencies)
		},
	})
}

func registerLinuxRubyInstaller(
	inst *lspinstaller.Provider,
	root string,
	native *lspinstaller.NativeArtifactInstaller,
	ruby lspinstaller.NativeArtifactSpec,
	dependencies []lspinstaller.NativeArtifactSpec,
	solargraph linuxRubyGemManifest,
	httpClient *http.Client,
) {
	managedPath := filepath.Join(root, solargraph.name, solargraph.version, "launcher", solargraph.launcher)
	inst.Register(contract.LSPServiceRuby, lspinstaller.InstallerConfig{
		BinaryName:          "solargraph",
		Language:            contract.LSPServiceRuby,
		AllowInstallCommand: true,
		ManagedOnly:         true,
		ManagedBinaryPath:   managedPath,
		ManagedInstall: func(ctx context.Context) (string, error) {
			return installLinuxRubySolargraph(ctx, native, root, ruby, dependencies, solargraph, httpClient)
		},
	})
}

func installLinuxSwiftSourcekit(ctx context.Context, native *lspinstaller.NativeArtifactInstaller, root string, spec lspinstaller.NativeArtifactSpec, dependencies []lspinstaller.NativeArtifactSpec) (string, error) {
	result, err := ensureLinuxNativeArtifact(ctx, native, root, spec)
	if err != nil {
		return "", fmt.Errorf("install Swift toolchain: %w", err)
	}
	toolchainBin := filepath.Dir(result.BinaryPath)
	toolchainUsr := filepath.Dir(toolchainBin)
	libraryDirs := []string{filepath.Join(toolchainUsr, "lib", "swift", "linux")}
	for _, dependency := range dependencies {
		library, err := ensureLinuxNativeLibraryArtifact(ctx, native, root, dependency)
		if err != nil {
			return "", fmt.Errorf("install Swift runtime dependency %s: %w", dependency.Name, err)
		}
		libraryDirs = append(libraryDirs, filepath.Dir(library.BinaryPath))
	}
	if err := writeLinuxSwiftLauncher(result.LauncherPath, result.BinaryPath, toolchainUsr, libraryDirs...); err != nil {
		return "", fmt.Errorf("write sourcekit-lsp launcher: %w", err)
	}
	return result.LauncherPath, nil
}

func ensureLinuxNativeLibraryArtifact(ctx context.Context, native *lspinstaller.NativeArtifactInstaller, root string, spec lspinstaller.NativeArtifactSpec) (lspinstaller.NativeInstallResult, error) {
	if native == nil {
		return lspinstaller.NativeInstallResult{}, errors.New("native artifact installer is nil")
	}
	finalDir := filepath.Join(root, spec.Name, spec.Version)
	binaryPath := filepath.Join(finalDir, "payload", filepath.FromSlash(spec.BinaryPath))
	launcherPath := linuxManagedArtifactPath(root, spec)
	if isLinuxRegularFile(binaryPath) {
		return lspinstaller.NativeInstallResult{Name: spec.Name, Version: spec.Version, InstallDir: finalDir, BinaryPath: binaryPath, LauncherPath: launcherPath, SHA256: spec.SHA256}, nil
	}
	if _, err := os.Stat(finalDir); err == nil {
		return lspinstaller.NativeInstallResult{}, fmt.Errorf("native runtime dependency exists but file is incomplete: %s", finalDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return lspinstaller.NativeInstallResult{}, fmt.Errorf("inspect native runtime dependency directory: %w", err)
	}
	return native.InstallArtifact(ctx, spec)
}

// ensureLinuxNativeArtifact 复用完整可执行 artifact，或拒绝半安装目录后重新安装。
func ensureLinuxNativeArtifact(ctx context.Context, native *lspinstaller.NativeArtifactInstaller, root string, spec lspinstaller.NativeArtifactSpec) (lspinstaller.NativeInstallResult, error) {
	if native == nil {
		return lspinstaller.NativeInstallResult{}, errors.New("native artifact installer is nil")
	}
	finalDir := filepath.Join(root, spec.Name, spec.Version)
	binaryPath := filepath.Join(finalDir, "payload", filepath.FromSlash(spec.BinaryPath))
	launcherPath := linuxManagedArtifactPath(root, spec)
	if isLinuxExecutableFile(binaryPath) {
		if _, err := os.Stat(finalDir); err != nil {
			return lspinstaller.NativeInstallResult{}, fmt.Errorf("inspect existing native artifact: %w", err)
		}
		return lspinstaller.NativeInstallResult{Name: spec.Name, Version: spec.Version, InstallDir: finalDir, BinaryPath: binaryPath, LauncherPath: launcherPath, SHA256: spec.SHA256}, nil
	}
	if _, err := os.Stat(finalDir); err == nil {
		return lspinstaller.NativeInstallResult{}, fmt.Errorf("native artifact exists but binary is incomplete: %s", finalDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return lspinstaller.NativeInstallResult{}, fmt.Errorf("inspect native artifact directory: %w", err)
	}
	return native.InstallArtifact(ctx, spec)
}

// writeLinuxSwiftLauncher 注入受管 Swift 工具链和动态库搜索路径。
func writeLinuxSwiftLauncher(launcher, target, toolchainUsr string, libraryDirs ...string) error {
	if !filepath.IsAbs(launcher) || !filepath.IsAbs(target) || !filepath.IsAbs(toolchainUsr) {
		return errors.New("Swift launcher paths must be absolute")
	}
	if !isLinuxExecutableFile(target) {
		return fmt.Errorf("sourcekit-lsp target is not executable: %s", target)
	}
	for _, libraryDir := range libraryDirs {
		if !filepath.IsAbs(libraryDir) {
			return fmt.Errorf("Swift runtime library path must be absolute: %q", libraryDir)
		}
	}
	if len(libraryDirs) == 0 {
		return errors.New("Swift runtime library paths are required")
	}
	content := "#!/bin/sh\nset -eu\nexport SWIFT_EXEC=" + linuxSwiftShellQuote(filepath.Join(toolchainUsr, "bin", "swift")) + "\nexport PATH=" + linuxSwiftShellQuote(filepath.Join(toolchainUsr, "bin")) + ":${PATH:-}\nexport LD_LIBRARY_PATH=" + linuxSwiftShellQuote(strings.Join(libraryDirs, string(os.PathListSeparator))) + "\nexec " + linuxSwiftShellQuote(target) + " \"$@\"\n"
	return writeLinuxManagedLauncher(launcher, content)
}

// writeLinuxManagedLauncher 以临时文件和 rename 原子发布私有可执行 launcher。
func writeLinuxManagedLauncher(launcher, content string) error {
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		return fmt.Errorf("create launcher directory: %w", err)
	}
	if info, err := os.Lstat(launcher); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("managed launcher target is not a regular file")
	}
	temporary, err := os.CreateTemp(filepath.Dir(launcher), ".managed-launcher-")
	if err != nil {
		return fmt.Errorf("create launcher temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := writeLinuxManagedLauncherFile(temporary, content); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, launcher); err != nil {
		return fmt.Errorf("publish launcher: %w", err)
	}
	committed = true
	return nil
}

// writeLinuxManagedLauncherFile 写入、同步并关闭 launcher 临时文件。
func writeLinuxManagedLauncherFile(file *os.File, content string) error {
	if err := file.Chmod(0o700); err != nil {
		_ = file.Close()
		return fmt.Errorf("chmod launcher: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write launcher: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync launcher: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close launcher: %w", err)
	}
	return nil
}

func linuxSwiftShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func isLinuxRegularFile(filename string) bool {
	info, err := os.Stat(filename)
	return err == nil && info.Mode().IsRegular()
}

// installLinuxRubySolargraph 安装 Ruby runtime、native 依赖和固定 Solargraph gem。
func installLinuxRubySolargraph(
	ctx context.Context,
	native *lspinstaller.NativeArtifactInstaller,
	root string,
	ruby lspinstaller.NativeArtifactSpec,
	dependencies []lspinstaller.NativeArtifactSpec,
	solargraph linuxRubyGemManifest,
	httpClient *http.Client,
) (string, error) {
	rubyResult, err := ensureLinuxNativeArtifact(ctx, native, root, ruby)
	if err != nil {
		return "", fmt.Errorf("install managed Ruby runtime: %w", err)
	}
	rubyHome := filepath.Dir(filepath.Dir(rubyResult.BinaryPath))
	includeDirs, libraryDirs, err := ensureLinuxRubyDependencies(ctx, native, root, dependencies)
	if err != nil {
		return "", err
	}
	gemHome := filepath.Join(root, solargraph.name, solargraph.version, "gems")
	gemBinary := filepath.Join(gemHome, "bin", solargraph.launcher)
	if !isLinuxExecutableFile(gemBinary) {
		if err := installFixedRubyGemAtomically(ctx, httpClient, rubyResult.BinaryPath, rubyHome, gemHome, includeDirs, libraryDirs, solargraph); err != nil {
			return "", err
		}
	}
	if !isLinuxExecutableFile(gemBinary) {
		return "", fmt.Errorf("fixed Solargraph gem did not publish executable: %s", gemBinary)
	}
	launcher := filepath.Join(root, solargraph.name, solargraph.version, "launcher", solargraph.launcher)
	defaultGemHome := filepath.Join(rubyHome, "lib", "ruby", "gems", "4.0.0")
	content := "#!/bin/sh\nset -eu\nexport GEM_HOME=" + linuxSwiftShellQuote(gemHome) + "\nexport GEM_PATH=" + linuxSwiftShellQuote(gemHome+string(os.PathListSeparator)+defaultGemHome) + "\nexport LD_LIBRARY_PATH=" + linuxSwiftShellQuote(strings.Join(libraryDirs, string(os.PathListSeparator))) + "\nexport PATH=" + linuxSwiftShellQuote(filepath.Join(rubyHome, "bin")) + ":${PATH:-}\nexec " + linuxSwiftShellQuote(rubyResult.BinaryPath) + " " + linuxSwiftShellQuote(gemBinary) + " \"$@\"\n"
	if err := writeLinuxManagedLauncher(launcher, content); err != nil {
		return "", fmt.Errorf("write Solargraph launcher: %w", err)
	}
	return launcher, nil
}

// ensureLinuxRubyDependencies 安装 Ruby native 依赖并返回编译和运行时搜索目录。
func ensureLinuxRubyDependencies(ctx context.Context, native *lspinstaller.NativeArtifactInstaller, root string, dependencies []lspinstaller.NativeArtifactSpec) ([]string, []string, error) {
	var includeDirs, libraryDirs []string
	for _, dependency := range dependencies {
		result, err := ensureLinuxNativeArtifact(ctx, native, root, dependency)
		if err != nil {
			return nil, nil, fmt.Errorf("install Ruby native dependency %s: %w", dependency.Name, err)
		}
		if strings.HasSuffix(result.BinaryPath, ".h") {
			includeDirs = append(includeDirs, filepath.Dir(result.BinaryPath))
			developmentLibraryDir := filepath.Join(result.InstallDir, "payload", "usr", "lib", "x86_64-linux-gnu")
			if info, statErr := os.Stat(developmentLibraryDir); statErr != nil || !info.IsDir() {
				return nil, nil, fmt.Errorf("Ruby native dependency %s library directory is missing: %s", dependency.Name, developmentLibraryDir)
			}
			libraryDirs = append(libraryDirs, developmentLibraryDir)
		} else {
			libraryDirs = append(libraryDirs, filepath.Dir(result.BinaryPath))
		}
	}
	return includeDirs, libraryDirs, nil
}

// installFixedRubyGemAtomically 在发布目录外安装完整 gem 图，失败不会污染下次安装。
func installFixedRubyGemAtomically(ctx context.Context, httpClient *http.Client, rubyBinary, rubyHome, gemHome string, includeDirs, libraryDirs []string, spec linuxRubyGemManifest) error {
	parent := filepath.Dir(gemHome)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create Solargraph version directory: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".gems-stage-")
	if err != nil {
		return fmt.Errorf("create Solargraph staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := installFixedRubyGem(ctx, httpClient, rubyBinary, rubyHome, stage, includeDirs, libraryDirs, spec); err != nil {
		return err
	}
	stagedBinary := filepath.Join(stage, "bin", spec.launcher)
	if !isLinuxExecutableFile(stagedBinary) {
		return fmt.Errorf("staged Solargraph gem did not publish executable: %s", stagedBinary)
	}
	if err := os.RemoveAll(gemHome); err != nil {
		return fmt.Errorf("remove incomplete Solargraph GEM_HOME: %w", err)
	}
	if err := os.Rename(stage, gemHome); err != nil {
		return fmt.Errorf("publish Solargraph GEM_HOME: %w", err)
	}
	committed = true
	return nil
}

// installFixedRubyGem 使用固定 gem 文件和受管编译搜索路径安装 Solargraph 依赖图。
func installFixedRubyGem(ctx context.Context, httpClient *http.Client, rubyBinary, rubyHome, gemHome string, includeDirs, libraryDirs []string, spec linuxRubyGemManifest) error {
	if httpClient == nil {
		return errors.New("Ruby gem HTTP client is nil")
	}
	gemPath := filepath.Join(gemHome, "cache", spec.name+"-"+spec.version+".gem")
	if err := downloadLinuxVerifiedGem(ctx, httpClient, spec.url, spec.sha256, gemPath); err != nil {
		return fmt.Errorf("download fixed Solargraph gem: %w", err)
	}
	if err := os.MkdirAll(gemHome, 0o700); err != nil {
		return fmt.Errorf("create Ruby GEM_HOME: %w", err)
	}
	gemBinary := filepath.Join(rubyHome, "bin", "gem")
	if !isLinuxExecutableFile(gemBinary) {
		return fmt.Errorf("managed Ruby gem command is missing: %s", gemBinary)
	}
	args := []string{"install", gemPath, "--install-dir", gemHome, "--no-document", "--conservative", "--minimal-deps", "--clear-sources", "--source", "https://rubygems.org"}
	if len(includeDirs) > 0 && len(libraryDirs) > 0 {
		// RubyGems 不会可靠地把 CPPFLAGS/LDFLAGS 传播到每个传递 native
		// extension；显式 extconf 参数保证 Psych 使用受管 libyaml。
		args = append(args, "--",
			"--with-libyaml-include="+includeDirs[0],
			"--with-libyaml-lib="+libraryDirs[len(libraryDirs)-1],
		)
	}
	command := exec.CommandContext(ctx, gemBinary, args...)
	command.Env = managedRubyEnvironment(rubyHome, gemHome, includeDirs, libraryDirs)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install fixed Solargraph gem %s: %w\nOutput: %s", spec.version, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func managedRubyEnvironment(rubyHome, gemHome string, includeDirs, libraryDirs []string) []string {
	env := os.Environ()
	pathValue := filepath.Join(rubyHome, "bin")
	if current := strings.TrimSpace(os.Getenv("PATH")); current != "" {
		pathValue += string(os.PathListSeparator) + current
	}
	defaultGemHome := filepath.Join(rubyHome, "lib", "ruby", "gems", "4.0.0")
	env = append(env,
		"GEM_HOME="+gemHome,
		"GEM_PATH="+gemHome+string(os.PathListSeparator)+defaultGemHome,
		"PATH="+pathValue,
		"CPPFLAGS="+joinRubyCompilerSearchFlags("-I", includeDirs),
		"LDFLAGS="+joinRubyCompilerSearchFlags("-L", libraryDirs),
		"LD_LIBRARY_PATH="+strings.Join(libraryDirs, string(os.PathListSeparator)),
	)
	return env
}

func joinRubyCompilerSearchFlags(prefix string, directories []string) string {
	flags := make([]string, 0, len(directories))
	for _, directory := range directories {
		flags = append(flags, prefix+directory)
	}
	return strings.Join(flags, " ")
}

// downloadLinuxVerifiedGem 复用已校验缓存或通过 HTTPS 下载固定 SHA-256 的 gem。
func downloadLinuxVerifiedGem(ctx context.Context, client *http.Client, rawURL, expected, destination string) error {
	ready, err := prepareLinuxGemDownload(client, rawURL, expected, destination)
	if err != nil || ready {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("create gem request: %w", err)
	}
	response, err := followHTTPSOnly(client, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("gem download returned HTTP status %s", response.Status)
	}
	return publishLinuxVerifiedGem(response.Body, expected, destination)
}

// prepareLinuxGemDownload 校验输入、创建 cache 目录并复核既有 gem。
func prepareLinuxGemDownload(client *http.Client, rawURL, expected, destination string) (bool, error) {
	if client == nil {
		return false, errors.New("gem HTTP client is nil")
	}
	if err := validateLinuxGemIdentity(rawURL, expected); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return false, fmt.Errorf("create gem cache directory: %w", err)
	}
	info, err := os.Stat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect cached gem: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("cached gem is not a regular file")
	}
	return true, verifyLinuxSHA256(destination, expected)
}

// validateLinuxGemIdentity 要求固定 HTTPS URL 和小写 SHA-256。
func validateLinuxGemIdentity(rawURL, expected string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return fmt.Errorf("gem URL must be HTTPS: %q", rawURL)
	}
	if len(expected) != sha256.Size*2 || strings.ToLower(expected) != expected {
		return fmt.Errorf("gem SHA256 is not a lowercase digest: %q", expected)
	}
	return nil
}

// publishLinuxVerifiedGem 有界写入临时文件，核验摘要后原子发布。
func publishLinuxVerifiedGem(input io.Reader, expected, destination string) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".gem-download-")
	if err != nil {
		return fmt.Errorf("create gem temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	digest := sha256.New()
	limited := io.LimitReader(input, linuxGemMaxBytes+1)
	written, copyErr := io.Copy(io.MultiWriter(temporary, digest), limited)
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("read gem bytes: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close gem temporary file: %w", closeErr)
	}
	if written > linuxGemMaxBytes {
		return fmt.Errorf("gem exceeds maximum size of %d bytes", linuxGemMaxBytes)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if actual != expected {
		return fmt.Errorf("gem SHA256 does not match: got %s, want %s", actual, expected)
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("publish gem: %w", err)
	}
	committed = true
	return nil
}

// verifyLinuxSHA256 对有界普通文件计算并核对 SHA-256。
func verifyLinuxSHA256(filename, expected string) error {
	info, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("stat cached gem: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("cached gem is not a regular file")
	}
	if info.Size() > linuxGemMaxBytes {
		return fmt.Errorf("cached gem exceeds maximum size of %d bytes", linuxGemMaxBytes)
	}
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open cached gem: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(file, linuxGemMaxBytes+1))
	if err != nil {
		return fmt.Errorf("hash cached gem: %w", err)
	}
	if written > linuxGemMaxBytes {
		return fmt.Errorf("cached gem exceeds maximum size of %d bytes", linuxGemMaxBytes)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if actual != expected {
		return fmt.Errorf("cached gem SHA256 does not match: got %s, want %s", actual, expected)
	}
	return nil
}

func followHTTPSOnly(client *http.Client, request *http.Request) (*http.Response, error) {
	copyClient := *client
	priorRedirect := copyClient.CheckRedirect
	copyClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL == nil || !strings.EqualFold(next.URL.Scheme, "https") {
			return errors.New("gem redirect must remain HTTPS")
		}
		if priorRedirect != nil {
			return priorRedirect(next, via)
		}
		return nil
	}
	response, err := copyClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download gem: %w", err)
	}
	return response, nil
}

type linuxGHCRAuthTransport struct {
	base  http.RoundTripper
	once  sync.Once
	token string
	err   error
}

func newLinuxNativeArtifactHTTPClient() *http.Client {
	return &http.Client{Transport: &linuxGHCRAuthTransport{base: http.DefaultTransport}}
}

// RoundTrip 仅为 ghcr.io 请求附加一次获取的匿名 pull token。
func (t *linuxGHCRAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil {
		return nil, errors.New("GHCR HTTP transport is incomplete")
	}
	if request.URL == nil || !strings.EqualFold(request.URL.Host, "ghcr.io") {
		return t.base.RoundTrip(request)
	}
	t.once.Do(func() { t.token, t.err = fetchLinuxGHCRToken(request.Context(), t.base) })
	if t.err != nil {
		return nil, t.err
	}
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

// fetchLinuxGHCRToken 获取 Homebrew portable-ruby 镜像的只读 bearer token。
func fetchLinuxGHCRToken(ctx context.Context, base http.RoundTripper) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ghcr.io/token?scope=repository%3Ahomebrew%2Fcore%2Fportable-ruby%3Apull&service=ghcr.io", nil)
	if err != nil {
		return "", fmt.Errorf("create GHCR token request: %w", err)
	}
	response, err := base.RoundTrip(request)
	if err != nil {
		return "", fmt.Errorf("request GHCR token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("GHCR token returned HTTP status %s", response.Status)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode GHCR token: %w", err)
	}
	if strings.TrimSpace(payload.Token) == "" {
		return "", errors.New("GHCR token response is empty")
	}
	return payload.Token, nil
}
