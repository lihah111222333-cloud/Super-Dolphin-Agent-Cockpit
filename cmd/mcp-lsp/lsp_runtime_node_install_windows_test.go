//go:build windows

package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

func TestWindowsNodeInstallerCheckOnlyDoesNotMaterializeOrUsePATH(t *testing.T) {
	var requests atomic.Int64
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected Node asset request during check-only preflight")
	})}
	cacheRoot := filepath.Join(t.TempDir(), "lsp-assets")
	cache, err := installer.NewWindowsAssetCache(cacheRoot, client)
	if err != nil {
		t.Fatalf("NewWindowsAssetCache(): %v", err)
	}
	nodeRuntime, err := installer.NewWindowsNodeRuntimeWithAssetCache(cache)
	if err != nil {
		t.Fatalf("NewWindowsNodeRuntimeWithAssetCache(): %v", err)
	}
	before, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatalf("ReadDir(empty AssetCache root): %v", err)
	}
	pathBefore := os.Getenv("PATH")
	provider := installer.NewProvider()
	provider.Register("typescript", installer.InstallerConfig{
		BinaryName:          "typescript-language-server.cmd",
		InstallCmd:          "npm.cmd",
		InstallArgs:         []string{"install", "-g", "typescript-language-server@" + typeScriptLanguageServerInstallVersion},
		AllowInstallCommand: true,
		InstallCommandResolver: func(ctx context.Context) (string, error) {
			return nodeRuntime.NPMCommand(ctx)
		},
		InstalledBinaryPathResolver: func(ctx context.Context) (string, error) {
			return nodeRuntime.BinaryPath(ctx, "typescript-language-server.cmd")
		},
		InstallArgsResolver: func(ctx context.Context) ([]string, error) {
			paths, err := nodeRuntime.Ensure(ctx)
			if err != nil {
				return nil, err
			}
			return []string{"install", "--prefix", paths.Prefix, "--save-exact", "typescript-language-server@" + typeScriptLanguageServerInstallVersion}, nil
		},
	})
	_, err = provider.EnsureInstalledDetailed(installer.WithToolCallInstallCheckOnly(context.Background()), "typescript")
	var missing *installer.MissingBinaryError
	if !errors.As(err, &missing) {
		t.Fatalf("check-only EnsureInstalledDetailed() error = %v, want MissingBinaryError", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("check-only Node asset HTTP requests = %d, want 0", got)
	}
	if got := os.Getenv("PATH"); got != pathBefore {
		t.Fatalf("check-only Node preflight changed PATH from %q to %q", pathBefore, got)
	}
	after, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatalf("ReadDir(AssetCache root after check-only): %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("check-only AssetCache entries changed from %d to %d", len(before), len(after))
	}
}

func TestWindowsNodeCohortReadinessDoesNotSkipOverlappingMarkdownPackage(t *testing.T) {
	cache, err := installer.NewWindowsAssetCache(filepath.Join(t.TempDir(), "lsp-assets"), nil)
	if err != nil {
		t.Fatalf("NewWindowsAssetCache(): %v", err)
	}
	nodeRuntime, err := installer.NewWindowsNodeRuntimeWithAssetCache(cache)
	if err != nil {
		t.Fatalf("NewWindowsNodeRuntimeWithAssetCache(): %v", err)
	}
	paths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("ExpectedPaths(): %v", err)
	}
	if err := os.MkdirAll(paths.BinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", paths.BinDir, err)
	}
	for _, name := range []string{"vscode-css-language-server.cmd", "vscode-markdown-language-server.cmd"} {
		if err := os.WriteFile(filepath.Join(paths.BinDir, name), []byte("@echo off\r\n"), 0o644); err != nil {
			t.Fatalf("write overlapping binary %q: %v", name, err)
		}
	}
	writePackageVersion := func(name, version string) {
		t.Helper()
		packageDir := filepath.Join(paths.Prefix, "node_modules", filepath.FromSlash(name))
		if err := os.MkdirAll(packageDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", packageDir, err)
		}
		contents := []byte(`{"name":"` + name + `","version":"` + version + `"}`)
		if err := os.WriteFile(filepath.Join(packageDir, "package.json"), contents, 0o644); err != nil {
			t.Fatalf("write package metadata %q: %v", name, err)
		}
	}
	writePackageVersion("vscode-langservers-extracted", vscodeLangserversExtractedInstallVersion)
	writePackageVersion("markdown-it", runtimeMarkdownItInstallVersion)
	writePackageVersion("vscode-markdown-languageservice", "0.5.0")

	var cssSpec, markdownSpec runtimeInstallerSpec
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		for _, language := range spec.languages {
			switch language {
			case "css":
				cssSpec = spec
			case "markdown":
				markdownSpec = spec
			}
		}
	}
	provider := installer.NewProvider()
	attachReadOnlyNodeRuntime := func(spec runtimeInstallerSpec) runtimeInstallerSpec {
		packages, packagesErr := runtimeNPMExactPackages(spec.args)
		spec.installedBinaryPathResolver = func(ctx context.Context) (string, error) {
			return nodeRuntime.BinaryPath(ctx, spec.binaryName)
		}
		spec.installedReadinessValidator = func(ctx context.Context) error {
			if packagesErr != nil {
				return packagesErr
			}
			return nodeRuntime.ValidateExactPackages(ctx, packages)
		}
		return spec
	}
	registerInstallerSpecs(provider, []runtimeInstallerSpec{
		attachReadOnlyNodeRuntime(cssSpec),
		attachReadOnlyNodeRuntime(markdownSpec),
	})
	cssResult, err := provider.EnsureInstalledDetailed(installer.WithToolCallInstallCheckOnly(context.Background()), "css")
	if err != nil {
		t.Fatalf("CSS check-only EnsureInstalledDetailed() = %v", err)
	}
	if cssResult.Status != installer.InstallStatusPathFound {
		t.Fatalf("CSS check-only status = %q, want path_found", cssResult.Status)
	}
	_, err = provider.EnsureInstalledDetailed(installer.WithToolCallInstallCheckOnly(context.Background()), "markdown")
	var missing *installer.MissingBinaryError
	if !errors.As(err, &missing) {
		t.Fatalf("Markdown stale-package check-only error = %v, want MissingBinaryError", err)
	}

	writePackageVersion("vscode-markdown-languageservice", vscodeMarkdownLanguageServiceInstallVersion)
	markdownResult, err := provider.EnsureInstalledDetailed(installer.WithToolCallInstallCheckOnly(context.Background()), "markdown")
	if err != nil {
		t.Fatalf("Markdown exact-package check-only EnsureInstalledDetailed() = %v", err)
	}
	if markdownResult.Status != installer.InstallStatusPathFound {
		t.Fatalf("Markdown exact-package check-only status = %q, want path_found", markdownResult.Status)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRuntimeNPMInstallerSpecsPinWindowsAndPreserveNonWindows(t *testing.T) {
	t.Parallel()

	want := []struct {
		language string
		binary   string
		packages []string
	}{
		{language: "javascript", binary: "typescript-language-server", packages: []string{
			"typescript-language-server@" + typeScriptLanguageServerInstallVersion,
			"typescript@" + typeScriptInstallVersion,
		}},
		{language: "javascriptreact", binary: "typescript-language-server", packages: []string{
			"typescript-language-server@" + typeScriptLanguageServerInstallVersion,
			"typescript@" + typeScriptInstallVersion,
		}},
		{language: "typescript", binary: "typescript-language-server", packages: []string{
			"typescript-language-server@" + typeScriptLanguageServerInstallVersion,
			"typescript@" + typeScriptInstallVersion,
		}},
		{language: "typescriptreact", binary: "typescript-language-server", packages: []string{
			"typescript-language-server@" + typeScriptLanguageServerInstallVersion,
			"typescript@" + typeScriptInstallVersion,
		}},
		{language: "python", binary: "pyright-langserver", packages: []string{
			"pyright@" + pyrightInstallVersion,
		}},
		{language: "css", binary: "vscode-css-language-server", packages: []string{
			"vscode-langservers-extracted@" + vscodeLangserversExtractedInstallVersion,
		}},
		{language: "html", binary: "vscode-html-language-server", packages: []string{
			"vscode-langservers-extracted@" + vscodeLangserversExtractedInstallVersion,
		}},
		{language: "json", binary: "vscode-json-language-server", packages: []string{
			"vscode-langservers-extracted@" + vscodeLangserversExtractedInstallVersion,
		}},
		{language: "markdown", binary: "vscode-markdown-language-server", packages: []string{
			"vscode-langservers-extracted@" + vscodeLangserversExtractedInstallVersion,
			"vscode-markdown-languageservice@" + vscodeMarkdownLanguageServiceInstallVersion,
			"markdown-it@" + runtimeMarkdownItInstallVersion,
		}},
		{language: "yaml", binary: "yaml-language-server", packages: []string{
			"yaml-language-server@" + yamlLanguageServerInstallVersion,
		}},
		{language: "vue", binary: "vue-language-server", packages: []string{
			"@vue/language-server@" + vueLanguageServerInstallVersion,
			"typescript-language-server@" + typeScriptLanguageServerInstallVersion,
			"typescript@" + typeScriptInstallVersion,
		}},
		{language: "svelte", binary: "svelteserver", packages: []string{
			"svelte-language-server@" + svelteLanguageServerInstallVersion,
		}},
		{language: "php", binary: "intelephense", packages: []string{
			"intelephense@" + intelephenseInstallVersion,
		}},
		{language: "dockerfile", binary: "docker-langserver", packages: []string{
			"dockerfile-language-server-nodejs@" + dockerfileLanguageServerInstallVersion,
		}},
		{language: "graphql", binary: "graphql-lsp", packages: []string{
			"graphql-language-service-cli@" + graphqlLanguageServiceCLIInstallVersion,
		}},
		{language: "prisma", binary: "prisma-language-server", packages: []string{
			"@prisma/language-server@" + prismaLanguageServerInstallVersion,
		}},
	}

	for _, platform := range []string{"linux", "darwin", "windows"} {
		platform := platform
		t.Run(platform, func(t *testing.T) {
			t.Parallel()

			specs := runtimeNPMInstallerSpecsForPlatform(platform)
			byLanguage := make(map[string]runtimeInstallerSpec, len(want))
			for _, spec := range specs {
				for _, language := range spec.languages {
					if _, exists := byLanguage[language]; exists {
						t.Fatalf("language %q is registered more than once", language)
					}
					byLanguage[language] = spec
				}
			}
			if len(byLanguage) != len(want) {
				t.Fatalf("NPM installer language coverage = %d, want %d", len(byLanguage), len(want))
			}

			wantCommand := runtimeNPMCommandForPlatform(platform)
			for _, tc := range want {
				spec, ok := byLanguage[tc.language]
				if !ok {
					t.Fatalf("missing NPM installer for %s", tc.language)
				}
				if spec.installCmd != wantCommand {
					t.Errorf("%s install command = %q, want %q", tc.language, spec.installCmd, wantCommand)
				}
				wantBinary := runtimeNPMExecutableNameForPlatform(platform, tc.binary)
				if spec.binaryName != wantBinary {
					t.Errorf("%s binary = %q, want %q", tc.language, spec.binaryName, wantBinary)
				}
				wantPackages := append([]string(nil), tc.packages...)
				if platform != "windows" {
					switch tc.language {
					case "javascript", "javascriptreact", "typescript", "typescriptreact":
						// TypeScript 的两个精确版本在本任务前已经是非 Windows 契约。
					case "vue":
						wantPackages = []string{"@vue/language-server"}
					case "css", "html", "json", "markdown":
						wantPackages = []string{
							"vscode-langservers-extracted",
							"vscode-markdown-languageservice@" + vscodeMarkdownLanguageServiceInstallVersion,
						}
					default:
						for index, packageSpec := range wantPackages {
							separator := strings.LastIndex(packageSpec, "@")
							if separator > 0 {
								wantPackages[index] = packageSpec[:separator]
							}
						}
					}
				}
				wantArgs := append([]string{"install", "-g"}, wantPackages...)
				if !slices.Equal(spec.args, wantArgs) {
					t.Errorf("%s install args = %#v, want %#v", tc.language, spec.args, wantArgs)
				}
				if platform == "windows" {
					for _, packageSpec := range spec.args[2:] {
						assertPinnedNPMPackage(t, tc.language, packageSpec)
					}
				}
			}
		})
	}
}

func TestNonWindowsProductionInstallerConfigurationRetainsPATHRuntime(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		goos := goos
		t.Run(goos, func(t *testing.T) {
			cfg, err := runtimeShellNPMInstallerConfigForProduction(goos)
			if err != nil {
				t.Fatalf("runtimeShellNPMInstallerConfigForProduction(%q): %v", goos, err)
			}
			if cfg.InstallCommandResolver != nil || cfg.InstallArgsResolver != nil || cfg.InstalledBinaryPathResolver != nil || cfg.InstalledReadinessValidator != nil {
				t.Fatalf("non-Windows %s config unexpectedly attached locked Node resolvers: %#v", goos, cfg)
			}
			if cfg.InstallLockKey != "" {
				t.Fatalf("non-Windows %s install lock key = %q, want empty legacy lock", goos, cfg.InstallLockKey)
			}
			if cfg.InstallTimeout != 0 {
				t.Fatalf("non-Windows %s install timeout = %s, want legacy zero/default behavior", goos, cfg.InstallTimeout)
			}
			if cfg.InstallCmd != runtimeNPMCommandForPlatform(goos) {
				t.Fatalf("non-Windows %s install command = %q, want %q", goos, cfg.InstallCmd, runtimeNPMCommandForPlatform(goos))
			}
			wantArgs := []string{"install", "-g", "bash-language-server", "shellcheck"}
			if !slices.Equal(cfg.InstallArgs, wantArgs) {
				t.Fatalf("non-Windows %s install args = %#v, want unchanged legacy args %#v", goos, cfg.InstallArgs, wantArgs)
			}
			if len(cfg.RequiredBinaries) != 1 || cfg.RequiredBinaries[0].Name != "shellcheck" || !slices.Equal(cfg.RequiredBinaries[0].CheckArgs, []string{"--version"}) {
				t.Fatalf("non-Windows %s required binaries changed from legacy shellcheck contract: %#v", goos, cfg.RequiredBinaries)
			}
		})
	}
}

func TestRuntimeShellNPMInstallerPreservesNonWindowsAndPinsWindows(t *testing.T) {
	t.Parallel()

	for _, target := range []struct {
		goos                  string
		goarch                string
		wantShellcheckPackage bool
	}{
		{goos: "linux", goarch: "arm64", wantShellcheckPackage: true},
		{goos: "darwin", goarch: "arm64", wantShellcheckPackage: true},
		{goos: "windows", goarch: "amd64", wantShellcheckPackage: true},
		{goos: "windows", goarch: "arm64", wantShellcheckPackage: false},
		{goos: "windows", goarch: "386", wantShellcheckPackage: false},
	} {
		target := target
		t.Run(target.goos+"-"+target.goarch, func(t *testing.T) {
			t.Parallel()

			cfg := runtimeShellNPMInstallerConfigForTarget(target.goos, target.goarch)
			if got, want := cfg.InstallCmd, runtimeNPMCommandForPlatform(target.goos); got != want {
				t.Fatalf("shell install command = %q, want %q", got, want)
			}
			wantArgs := []string{"install", "-g", "bash-language-server", "shellcheck"}
			if target.goos == "windows" {
				wantArgs = []string{
					"install", "-g",
					"bash-language-server@" + bashLanguageServerInstallVersion,
				}
				if target.wantShellcheckPackage {
					wantArgs = append(wantArgs, "shellcheck@"+shellcheckInstallVersion)
				}
			}
			if !slices.Equal(cfg.InstallArgs, wantArgs) {
				t.Fatalf("shell install args = %#v, want %#v", cfg.InstallArgs, wantArgs)
			}
			if got, want := cfg.BinaryName, runtimeNPMExecutableNameForPlatform(target.goos, "bash-language-server"); got != want {
				t.Fatalf("shell binary = %q, want %q", got, want)
			}
			wantRequired := 0
			if target.wantShellcheckPackage {
				wantRequired = 1
			}
			if len(cfg.RequiredBinaries) != wantRequired {
				t.Fatalf("shell required binaries = %#v, want %d", cfg.RequiredBinaries, wantRequired)
			}
			if wantRequired == 1 {
				if got, want := cfg.RequiredBinaries[0].Name, runtimeNPMExecutableNameForPlatform(target.goos, "shellcheck"); got != want {
					t.Fatalf("shellcheck binary = %q, want %q", got, want)
				}
				if !slices.Equal(cfg.RequiredBinaries[0].CheckArgs, []string{"--version"}) {
					t.Fatalf("shellcheck check args = %#v, want --version", cfg.RequiredBinaries[0].CheckArgs)
				}
			}
			if target.goos == "windows" {
				for _, packageSpec := range cfg.InstallArgs[2:] {
					assertPinnedNPMPackage(t, "shellscript", packageSpec)
				}
			}
		})
	}
}

func TestRuntimeWindowsArchitectureNormalizesAndRejectsUnsupportedShellcheckTargets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		goarch     string
		wantArch   string
		wantNPMBin bool
	}{
		{name: "arm64", goarch: "arm64", wantArch: "arm64", wantNPMBin: false},
		{name: "aarch64 alias", goarch: "aarch64", wantArch: "arm64", wantNPMBin: false},
		{name: "amd64", goarch: "amd64", wantArch: "x64", wantNPMBin: true},
		{name: "x64 alias", goarch: "x64", wantArch: "x64", wantNPMBin: true},
		{name: "386", goarch: "386", wantArch: "x86", wantNPMBin: false},
		{name: "x86 alias", goarch: "x86", wantArch: "x86", wantNPMBin: false},
		{name: "unsupported", goarch: "riscv64", wantArch: "", wantNPMBin: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeWindowsArchitecture(tc.goarch); got != tc.wantArch {
				t.Fatalf("runtimeWindowsArchitecture(%q) = %q, want %q", tc.goarch, got, tc.wantArch)
			}
			if got := runtimeShellcheckNPMAvailableForTarget("windows", tc.goarch); got != tc.wantNPMBin {
				t.Fatalf("runtimeShellcheckNPMAvailableForTarget(windows, %q) = %t, want %t", tc.goarch, got, tc.wantNPMBin)
			}
		})
	}
}

func assertPinnedNPMPackage(t *testing.T, language, packageSpec string) {
	t.Helper()
	separator := strings.LastIndex(packageSpec, "@")
	if separator <= 0 || separator == len(packageSpec)-1 {
		t.Fatalf("%s package %q is not pinned to an exact version", language, packageSpec)
	}
	version := packageSpec[separator+1:]
	if strings.EqualFold(version, "latest") || strings.Contains(version, "^") || strings.Contains(version, "~") {
		t.Fatalf("%s package %q is not pinned to an exact version", language, packageSpec)
	}
}
