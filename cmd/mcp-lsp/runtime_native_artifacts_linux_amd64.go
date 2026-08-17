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
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	linuxBufVersion          = "1.72.0"
	linuxLuaLSVersion        = "3.19.1"
	linuxTerraformLSVersion  = "0.39.0"
	linuxSqruffVersion       = "0.40.0"
	linuxDartVersion         = "3.13.0"
	linuxRustAnalyzerVersion = "2026-08-10.1"
	linuxClangdVersion       = "22.1.6"
	linuxJavaRuntimeVersion  = "21.0.11+10"
	linuxJDTLSVersion        = "1.60.0-202606262232"
	linuxKotlinLSVersion     = "1.3.13"
)

// linuxNativeArtifactManifest 固定一个 Linux amd64 语言服务器官方归档及其启动路径。
type linuxNativeArtifactManifest struct {
	language        string
	binaryName      string
	binaryCheckArgs []string
	artifact        installer.NativeArtifactSpec
}

// linuxJavaKotlinArtifacts 固定 Java 运行时及 Java/Kotlin LSP 的官方归档。
// JDTLS 和 kotlin-language-server 都是 JVM 应用，必须通过 javaRuntime 启动，
// 因而它们不能再登记为 brew/apt 的独立安装命令。
type linuxJavaKotlinArtifacts struct {
	javaRuntime installer.NativeArtifactSpec
	jdtls       installer.NativeArtifactSpec
	kotlin      installer.NativeArtifactSpec
}

// linuxJavaKotlinArtifactSpecs 返回固定摘要的 Java runtime、JDTLS 与 Kotlin server 清单。
func linuxJavaKotlinArtifactSpecs() linuxJavaKotlinArtifacts {
	return linuxJavaKotlinArtifacts{
		javaRuntime: installer.NativeArtifactSpec{
			Name:         "temurin-jre",
			Version:      linuxJavaRuntimeVersion,
			URL:          "https://github.com/adoptium/temurin21-binaries/releases/download/jdk-21.0.11%2B10/OpenJDK21U-jre_x64_linux_hotspot_21.0.11_10.tar.gz",
			SHA256:       "e5038aae3ca9ff670bc696496b0728dbd23d280026bad30291cb919221ecfdcb",
			Format:       installer.NativeArtifactFormatTarGz,
			BinaryPath:   "jdk-21.0.11+10-jre/bin/java",
			LauncherName: "java",
		},
		jdtls: installer.NativeArtifactSpec{
			Name:         "jdtls",
			Version:      linuxJDTLSVersion,
			URL:          "https://download.eclipse.org/jdtls/milestones/1.60.0/jdt-language-server-1.60.0-202606262232.tar.gz",
			SHA256:       "e94c303d8198f977930803582738771fd18c52c5492878410bf222b1aa81ef1d",
			Format:       installer.NativeArtifactFormatTarGz,
			BinaryPath:   "bin/jdtls",
			LauncherName: "jdtls",
		},
		kotlin: installer.NativeArtifactSpec{
			Name:         "kotlin-language-server",
			Version:      linuxKotlinLSVersion,
			URL:          "https://github.com/fwcd/kotlin-language-server/releases/download/1.3.13/server.zip",
			SHA256:       "4fe7d71d087b307c7869036171bd9d8c6a4284cd7c25b89098b0a24eb2d9b6d2",
			Format:       installer.NativeArtifactFormatZip,
			BinaryPath:   "server/bin/kotlin-language-server",
			LauncherName: "kotlin-language-server",
		},
	}
}

// linuxNativeArtifactManifests 返回 Linux amd64 的基础语言服务器清单。
func linuxNativeArtifactManifests() []linuxNativeArtifactManifest {
	manifests := []linuxNativeArtifactManifest{
		{
			language:   "proto",
			binaryName: "buf",
			artifact: installer.NativeArtifactSpec{
				Name:         "buf",
				Version:      linuxBufVersion,
				URL:          "https://github.com/bufbuild/buf/releases/download/v1.72.0/buf-Linux-x86_64.tar.gz",
				SHA256:       "a9c6186cf6fcf062b247345e1b7b12c26f580c1b2a4bbf4d3fe080abf85ceee8",
				Format:       installer.NativeArtifactFormatTarGz,
				BinaryPath:   "buf/bin/buf",
				LauncherName: "buf",
			},
		},
		{
			language:   "lua",
			binaryName: "lua-language-server",
			artifact: installer.NativeArtifactSpec{
				Name:         "lua-language-server",
				Version:      linuxLuaLSVersion,
				URL:          "https://github.com/LuaLS/lua-language-server/releases/download/3.19.1/lua-language-server-3.19.1-linux-x64.tar.gz",
				SHA256:       "e9235d2d72ef55bc41cf8c99cda2ed64777682024b4bb81f5dea425060c5cbb8",
				Format:       installer.NativeArtifactFormatTarGz,
				BinaryPath:   "bin/lua-language-server",
				LauncherName: "lua-language-server",
			},
		},
		{
			language:   "terraform",
			binaryName: "terraform-ls",
			artifact: installer.NativeArtifactSpec{
				Name:         "terraform-ls",
				Version:      linuxTerraformLSVersion,
				URL:          "https://releases.hashicorp.com/terraform-ls/0.39.0/terraform-ls_0.39.0_linux_amd64.zip",
				SHA256:       "7750edc736845fd8c04ff0fc6332423c12d8275b358668c8c17e8aedc43ef971",
				Format:       installer.NativeArtifactFormatZip,
				BinaryPath:   "terraform-ls",
				LauncherName: "terraform-ls",
			},
		},
		{
			language:        "sql",
			binaryName:      "sqruff",
			binaryCheckArgs: []string{"--version"},
			artifact: installer.NativeArtifactSpec{
				Name:         "sqruff",
				Version:      linuxSqruffVersion,
				URL:          "https://github.com/quarylabs/sqruff/releases/download/v0.40.0/sqruff-linux-x86_64-musl.tar.gz",
				SHA256:       "8a377bdfdfaf46483c33cce46d3b4eb46bcec4b9557f6d0106adc85cc926660e",
				Format:       installer.NativeArtifactFormatTarGz,
				BinaryPath:   "sqruff",
				LauncherName: "sqruff",
			},
		},
	}
	return appendLinuxAdditionalArtifactManifests(manifests)
}

// appendLinuxAdditionalArtifactManifests 追加共享 clangd 以及 Dart、Rust 清单。
func appendLinuxAdditionalArtifactManifests(manifests []linuxNativeArtifactManifest) []linuxNativeArtifactManifest {
	clangd := installer.NativeArtifactSpec{
		Name:         "clangd",
		Version:      linuxClangdVersion,
		URL:          "https://github.com/clangd/clangd/releases/download/22.1.6/clangd-linux-22.1.6.zip",
		SHA256:       "a9c77443af2e447ed467e84771848d3a6ac1c56f84bcfcde717e66318de77cfa",
		Format:       installer.NativeArtifactFormatZip,
		BinaryPath:   "clangd_22.1.6/bin/clangd",
		LauncherName: "clangd",
	}
	for _, language := range contract.ClangdLanguageIDs() {
		manifests = append(manifests, linuxNativeArtifactManifest{
			language:   language,
			binaryName: "clangd",
			artifact:   clangd,
		})
	}
	manifests = append(manifests,
		linuxNativeArtifactManifest{
			language:   contract.LSPServiceDart,
			binaryName: "dart",
			artifact: installer.NativeArtifactSpec{
				Name:         "dart",
				Version:      linuxDartVersion,
				URL:          "https://storage.googleapis.com/dart-archive/channels/stable/release/3.13.0/sdk/dartsdk-linux-x64-release.zip",
				SHA256:       "87902573facd8acacac7ee1fe73fa8d0668e06065016068e2ed6c5c99c6b1ee0",
				Format:       installer.NativeArtifactFormatZip,
				BinaryPath:   "dart-sdk/bin/dart",
				LauncherName: "dart",
			},
		},
		linuxNativeArtifactManifest{
			language:   contract.LSPServiceRust,
			binaryName: "rust-analyzer",
			artifact: installer.NativeArtifactSpec{
				Name:         "rust-analyzer",
				Version:      linuxRustAnalyzerVersion,
				URL:          "https://github.com/rust-lang/rust-analyzer/releases/download/2026-08-10.1/rust-analyzer-x86_64-unknown-linux-gnu.gz",
				SHA256:       "d42908a7dc7b89250ae881a0919e477296843665c98574ecc8fe16ba60cecefb",
				Format:       installer.NativeArtifactFormatGzip,
				BinaryPath:   "rust-analyzer",
				LauncherName: "rust-analyzer",
			},
		},
	)
	return manifests
}

type linuxNativeArtifactInstallRootResolver func() (string, error)

// resolveLinuxNativeArtifactInstallRoot 为受管归档返回私有、绝对的用户缓存目录。
// 不回退到临时目录或当前目录，无法取得用户缓存目录时直接失败。
func resolveLinuxNativeArtifactInstallRoot() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve Linux native artifact cache directory: %w", err)
	}
	cacheDir = strings.TrimSpace(cacheDir)
	if cacheDir == "" {
		return "", errors.New("resolve Linux native artifact cache directory: user cache directory is empty")
	}
	if !filepath.IsAbs(cacheDir) {
		return "", fmt.Errorf("resolve Linux native artifact cache directory: path is not absolute: %q", cacheDir)
	}
	root := filepath.Clean(filepath.Join(cacheDir, "super-agent-v3", "mcp-lsp", "native-artifacts"))
	if !filepath.IsAbs(root) || root == string(filepath.Separator) || root == "." {
		return "", fmt.Errorf("resolve Linux native artifact install root: invalid path %q", root)
	}
	return root, nil
}

// registerPlatformNativeArtifactInstallers 注册 Linux 的自包含官方归档。
func registerPlatformNativeArtifactInstallers(inst *installer.Provider) error {
	return registerLinuxNativeArtifactInstallersWithResolver(inst, resolveLinuxNativeArtifactInstallRoot, nil)
}

// registerLinuxNativeArtifactInstallersWithResolver 使用显式根目录解析器注册受管 Linux 工具链。
func registerLinuxNativeArtifactInstallersWithResolver(
	inst *installer.Provider,
	resolveRoot linuxNativeArtifactInstallRootResolver,
	httpClient *http.Client,
	manifests ...linuxNativeArtifactManifest,
) error {
	root, nativeInstaller, err := newLinuxNativeArtifactInstaller(inst, resolveRoot, httpClient)
	if err != nil {
		return err
	}
	includeJavaKotlin := len(manifests) == 0
	if includeJavaKotlin {
		manifests = linuxNativeArtifactManifests()
	}
	for _, manifest := range manifests {
		if err := registerLinuxNativeArtifactManifest(inst, nativeInstaller, root, manifest); err != nil {
			return err
		}
	}
	if includeJavaKotlin {
		if err := registerLinuxJavaKotlinInstallers(inst, nativeInstaller, root, linuxJavaKotlinArtifactSpecs()); err != nil {
			return err
		}
		if err := registerLinuxSwiftRubyInstallers(inst, root, httpClient); err != nil {
			return err
		}
	}
	return nil
}

// newLinuxNativeArtifactInstaller 校验 provider 与根目录，并创建受管归档 installer。
func newLinuxNativeArtifactInstaller(inst *installer.Provider, resolveRoot linuxNativeArtifactInstallRootResolver, httpClient *http.Client) (string, *installer.NativeArtifactInstaller, error) {
	if inst == nil {
		return "", nil, errors.New("Linux native artifact installer provider is nil")
	}
	if resolveRoot == nil {
		return "", nil, errors.New("Linux native artifact install root resolver is nil")
	}
	root, err := resolveRoot()
	if err != nil {
		return "", nil, fmt.Errorf("resolve Linux native artifact install root: %w", err)
	}
	root = filepath.Clean(strings.TrimSpace(root))
	if !filepath.IsAbs(root) {
		return "", nil, fmt.Errorf("Linux native artifact install root must be absolute: %q", root)
	}
	nativeInstaller, err := installer.NewNativeArtifactInstaller(installer.NativeArtifactInstallerConfig{InstallRoot: root, HTTPClient: httpClient})
	if err != nil {
		return "", nil, fmt.Errorf("create Linux native artifact installer: %w", err)
	}
	return root, nativeInstaller, nil
}

// registerLinuxNativeArtifactManifest 把一个固定 artifact 清单注册为受管安装器。
func registerLinuxNativeArtifactManifest(
	inst *installer.Provider,
	nativeInstaller *installer.NativeArtifactInstaller,
	root string,
	manifest linuxNativeArtifactManifest,
) error {
	if strings.TrimSpace(manifest.language) == "" || strings.TrimSpace(manifest.binaryName) == "" {
		return errors.New("Linux native artifact manifest language and binary are required")
	}
	spec := manifest.artifact
	launcherName := strings.TrimSpace(spec.LauncherName)
	if launcherName == "" {
		return fmt.Errorf("Linux native artifact manifest %s launcher name is required", manifest.language)
	}
	managedPath := filepath.Join(root, spec.Name, spec.Version, "launcher", launcherName)
	if !filepath.IsAbs(managedPath) {
		return fmt.Errorf("Linux native artifact manifest %s managed path is not absolute", manifest.language)
	}
	cfg := installer.InstallerConfig{
		BinaryName:          manifest.binaryName,
		BinaryCheckArgs:     append([]string(nil), manifest.binaryCheckArgs...),
		AllowInstallCommand: true,
		ManagedBinaryPath:   managedPath,
		ManagedInstall: func(ctx context.Context) (string, error) {
			result, err := nativeInstaller.InstallArtifact(ctx, spec)
			if err != nil {
				return "", fmt.Errorf("install %s native artifact: %w", manifest.language, err)
			}
			return result.LauncherPath, nil
		},
	}
	inst.Register(manifest.language, cfg)
	return nil
}

// registerLinuxJavaKotlinInstallers 注册共享同一受管 Java runtime 的两个语言服务器。
func registerLinuxJavaKotlinInstallers(
	inst *installer.Provider,
	nativeInstaller *installer.NativeArtifactInstaller,
	root string,
	artifacts linuxJavaKotlinArtifacts,
) error {
	if inst == nil || nativeInstaller == nil {
		return errors.New("Linux Java/Kotlin native artifact installer is incomplete")
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("Linux Java/Kotlin native artifact root must be absolute: %q", root)
	}
	javaPath := linuxManagedArtifactPath(root, artifacts.javaRuntime)
	jdtlsPath := linuxManagedArtifactPath(root, artifacts.jdtls)
	kotlinPath := linuxManagedArtifactPath(root, artifacts.kotlin)
	if javaPath == "" || jdtlsPath == "" || kotlinPath == "" {
		return errors.New("Linux Java/Kotlin native artifact launcher paths are incomplete")
	}
	inst.Register("java", installer.InstallerConfig{
		BinaryName:          "jdtls",
		AllowInstallCommand: true,
		ManagedBinaryPath:   jdtlsPath,
		ManagedInstall: func(ctx context.Context) (string, error) {
			return installLinuxJavaLanguageServer(ctx, nativeInstaller, root, artifacts.javaRuntime, artifacts.jdtls)
		},
	})
	inst.Register("kotlin", installer.InstallerConfig{
		BinaryName:          "kotlin-language-server",
		AllowInstallCommand: true,
		ManagedBinaryPath:   kotlinPath,
		ManagedInstall: func(ctx context.Context) (string, error) {
			return installLinuxJavaLanguageServer(ctx, nativeInstaller, root, artifacts.javaRuntime, artifacts.kotlin)
		},
	})
	return nil
}

func linuxManagedArtifactPath(root string, spec installer.NativeArtifactSpec) string {
	name := strings.TrimSpace(spec.Name)
	version := strings.TrimSpace(spec.Version)
	launcher := strings.TrimSpace(spec.LauncherName)
	if name == "" || version == "" || launcher == "" {
		return ""
	}
	path := filepath.Join(root, name, version, "launcher", launcher)
	if !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func installLinuxJavaLanguageServer(
	ctx context.Context,
	nativeInstaller *installer.NativeArtifactInstaller,
	root string,
	javaRuntime installer.NativeArtifactSpec,
	languageServer installer.NativeArtifactSpec,
) (string, error) {
	javaHome, err := ensureLinuxManagedJavaRuntime(ctx, nativeInstaller, root, javaRuntime)
	if err != nil {
		return "", err
	}
	result, err := nativeInstaller.InstallArtifact(ctx, languageServer)
	if err != nil {
		return "", fmt.Errorf("install %s artifact: %w", languageServer.Name, err)
	}
	if err := writeLinuxJavaLauncher(result.LauncherPath, result.BinaryPath, javaHome); err != nil {
		return "", fmt.Errorf("write %s managed launcher: %w", languageServer.Name, err)
	}
	return result.LauncherPath, nil
}

// ensureLinuxManagedJavaRuntime 复用完整 runtime，或安装并验证 java 与 launcher。
func ensureLinuxManagedJavaRuntime(
	ctx context.Context,
	nativeInstaller *installer.NativeArtifactInstaller,
	root string,
	spec installer.NativeArtifactSpec,
) (string, error) {
	javaHomeRelative := filepath.Dir(filepath.Dir(filepath.FromSlash(spec.BinaryPath)))
	if javaHomeRelative == "." || filepath.IsAbs(javaHomeRelative) {
		return "", fmt.Errorf("managed Java runtime binary path is invalid: %q", spec.BinaryPath)
	}
	javaHome := filepath.Join(root, spec.Name, spec.Version, "payload", javaHomeRelative)
	javaBinary := filepath.Join(javaHome, "bin", "java")
	launcher := linuxManagedArtifactPath(root, spec)
	if launcher == "" || !filepath.IsAbs(javaHome) {
		return "", errors.New("managed Java runtime paths are not absolute")
	}
	if isLinuxExecutableFile(javaBinary) && isLinuxExecutableFile(launcher) {
		return javaHome, nil
	}
	result, err := nativeInstaller.InstallArtifact(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("install managed Java runtime: %w", err)
	}
	if !isLinuxExecutableFile(result.BinaryPath) || !isLinuxExecutableFile(result.LauncherPath) {
		return "", errors.New("managed Java runtime installed without executable java launcher")
	}
	return javaHome, nil
}

func isLinuxExecutableFile(filename string) bool {
	info, err := os.Stat(filename)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

// writeLinuxJavaLauncher 用受管 JAVA_HOME 覆盖 native installer 生成的通用 launcher。
func writeLinuxJavaLauncher(launcher, target, javaHome string) error {
	if !filepath.IsAbs(launcher) || !filepath.IsAbs(target) || !filepath.IsAbs(javaHome) {
		return errors.New("managed Java launcher paths must be absolute")
	}
	content := "#!/bin/sh\nset -eu\nexport JAVA_HOME=" + linuxShellQuote(javaHome) + "\nexport PATH=\"$JAVA_HOME/bin${PATH:+:$PATH}\"\nexec " + linuxShellQuote(target) + " \"$@\"\n"
	return writeLinuxManagedLauncher(launcher, content)
}

func linuxShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
