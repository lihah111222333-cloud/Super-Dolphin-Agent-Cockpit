//go:build !windows

package main

import (
	"runtime"
	"slices"
	"testing"
)

// TestNonWindowsRuntimeInstallerKeepsLegacyNPMArguments 锁定 Linux/macOS 在本次 Windows 安装桥变更前的 PATH npm 参数。
func TestNonWindowsRuntimeInstallerKeepsLegacyNPMArguments(t *testing.T) {
	t.Parallel()

	type expected struct {
		binary   string
		packages []string
	}
	typeScriptPackages := []string{
		"typescript-language-server@" + typeScriptLanguageServerInstallVersion,
		"typescript@" + typeScriptInstallVersion,
	}
	extractedPackages := []string{
		"vscode-langservers-extracted",
		"vscode-markdown-languageservice@" + vscodeMarkdownLanguageServiceInstallVersion,
	}
	want := map[string]expected{
		"javascript":      {binary: "typescript-language-server", packages: typeScriptPackages},
		"javascriptreact": {binary: "typescript-language-server", packages: typeScriptPackages},
		"typescript":      {binary: "typescript-language-server", packages: typeScriptPackages},
		"typescriptreact": {binary: "typescript-language-server", packages: typeScriptPackages},
		"python":          {binary: "pyright-langserver", packages: []string{"pyright"}},
		"css":             {binary: "vscode-css-language-server", packages: extractedPackages},
		"html":            {binary: "vscode-html-language-server", packages: extractedPackages},
		"json":            {binary: "vscode-json-language-server", packages: extractedPackages},
		"markdown":        {binary: "vscode-markdown-language-server", packages: extractedPackages},
		"yaml":            {binary: "yaml-language-server", packages: []string{"yaml-language-server"}},
		"vue":             {binary: "vue-language-server", packages: []string{"@vue/language-server"}},
		"svelte":          {binary: "svelteserver", packages: []string{"svelte-language-server"}},
		"php":             {binary: "intelephense", packages: []string{"intelephense"}},
		"dockerfile":      {binary: "docker-langserver", packages: []string{"dockerfile-language-server-nodejs"}},
		"graphql":         {binary: "graphql-lsp", packages: []string{"graphql-language-service-cli"}},
		"prisma":          {binary: "prisma-language-server", packages: []string{"@prisma/language-server"}},
	}

	got := make(map[string]runtimeInstallerSpec, len(want))
	for _, spec := range runtimeNPMInstallerSpecsForPlatform(runtime.GOOS) {
		for _, language := range spec.languages {
			if _, exists := got[language]; exists {
				t.Fatalf("non-Windows language %q is registered twice", language)
			}
			got[language] = spec
		}
	}
	if len(got) != len(want) {
		t.Fatalf("non-Windows npm language closure = %d, want %d", len(got), len(want))
	}
	for language, expected := range want {
		spec, ok := got[language]
		if !ok {
			t.Fatalf("non-Windows npm installer is missing %q", language)
		}
		if spec.binaryName != expected.binary || spec.installCmd != "npm" {
			t.Errorf("non-Windows %s binary/command = (%q, %q), want (%q, npm)", language, spec.binaryName, spec.installCmd, expected.binary)
		}
		wantArgs := append([]string{"install", "-g"}, expected.packages...)
		if !slices.Equal(spec.args, wantArgs) {
			t.Errorf("non-Windows %s args = %#v, want legacy %#v", language, spec.args, wantArgs)
		}
	}
}

// TestNonWindowsRuntimeInstallerKeepsLegacyShellArguments 锁定 Linux/macOS 未加版本后缀的 shell 安装参数与 PATH companion。
func TestNonWindowsRuntimeInstallerKeepsLegacyShellArguments(t *testing.T) {
	t.Parallel()

	cfg := runtimeShellNPMInstallerConfigForPlatform(runtime.GOOS)
	wantArgs := []string{"install", "-g", "bash-language-server", "shellcheck"}
	if cfg.BinaryName != "bash-language-server" || cfg.InstallCmd != "npm" || !slices.Equal(cfg.InstallArgs, wantArgs) {
		t.Fatalf("non-Windows shell config = %#v, want unchanged PATH npm args %#v", cfg, wantArgs)
	}
	if cfg.InstallTimeout != 0 || cfg.InstallLockKey != "" || cfg.InstallAction != nil || cfg.InstalledBinaryPathResolver != nil {
		t.Fatalf("non-Windows shell config unexpectedly inherited Windows lifecycle hooks: %#v", cfg)
	}
	if len(cfg.RequiredBinaries) != 1 || cfg.RequiredBinaries[0].Name != "shellcheck" || !slices.Equal(cfg.RequiredBinaries[0].CheckArgs, []string{"--version"}) {
		t.Fatalf("non-Windows shellcheck companion changed: %#v", cfg.RequiredBinaries)
	}
}
