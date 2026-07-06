package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	lspinstaller "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/installer"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

func TestNewManagerRegistersDocumentFallbackAdapters(t *testing.T) {
	root := runtimeCanonicalTempDir(t)
	t.Setenv("GO_AGENT_LSP_ROOT", root)

	cfg, err := platformconfig.New()
	if err != nil {
		t.Fatalf("platform config: %v", err)
	}
	mgr, err := newManager(cfg)
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	resolver, ok := mgr.registry.(interface {
		ResolveManagerForFile(context.Context, string) (lspmanager.ScopedManager, error)
	})
	if !ok {
		t.Fatalf("runtime registry does not expose scoped file resolver")
	}

	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  "agent-fallback",
		ThreadID: "thread-fallback",
		CWD:      root,
		Family:   "lsp",
	})
	cases := []struct {
		name         string
		body         string
		wantLanguage string
		wantSymbol   string
	}{
		{name: "README.md", body: "# Title\n", wantLanguage: "markdown", wantSymbol: "Title"},
		{name: "config.json", body: "{\n  \"name\": \"demo\"\n}\n", wantLanguage: "json", wantSymbol: "name"},
		{name: "config.yaml", body: "name: demo\n", wantLanguage: "yaml", wantSymbol: "name"},
	}

	for _, tc := range cases {
		assertDocumentFallbackCase(t, root, ctx, resolver, tc)
	}
}

func TestNewManagerUsesPlatformLSPConfig(t *testing.T) {
	root := runtimeCanonicalTempDir(t)
	t.Setenv("PROJECT_ROOT", root)
	t.Setenv("GO_AGENT_LSP_ROOT", root)
	t.Setenv("LSP_JSTS_ROOT_MARKERS", "custom.workspace")
	writeRuntimeTestFile(t, filepath.Join(root, "custom.workspace"), "marker\n")
	target := filepath.Join(root, "src", "app.ts")
	writeRuntimeTestFile(t, target, "export const value = 1\n")

	cfg, err := platformconfig.New()
	if err != nil {
		t.Fatalf("platform config: %v", err)
	}
	mgr, err := newManager(cfg)
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	resolver, ok := mgr.registry.(interface {
		ResolveManagerForFile(context.Context, string) (lspmanager.ScopedManager, error)
	})
	if !ok {
		t.Fatalf("runtime registry does not expose scoped file resolver")
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{AgentID: "agent-config", ThreadID: "thread-config", CWD: root, Family: "lsp"})
	scoped, err := resolver.ResolveManagerForFile(ctx, target)
	if err != nil {
		t.Fatalf("ResolveManagerForFile: %v", err)
	}
	if got := scoped.ResolvedScope.WorkspaceRoot; got != root {
		t.Fatalf("resolved workspace root = %q, want %q", got, root)
	}
}

func assertDocumentFallbackCase(
	t *testing.T,
	root string,
	ctx context.Context,
	resolver interface {
		ResolveManagerForFile(context.Context, string) (lspmanager.ScopedManager, error)
	},
	tc struct {
		name         string
		body         string
		wantLanguage string
		wantSymbol   string
	},
) {
	t.Helper()
	target := filepath.Join(root, tc.name)
	if err := os.WriteFile(target, []byte(tc.body), 0o644); err != nil {
		t.Fatalf("write %s: %v", tc.name, err)
	}
	scoped, err := resolver.ResolveManagerForFile(ctx, target)
	if err != nil {
		t.Fatalf("ResolveManagerForFile(%s): %v", tc.name, err)
	}
	if got := scoped.ResolvedScope.LanguageID; got != tc.wantLanguage {
		t.Fatalf("ResolveManagerForFile(%s) language = %q, want %q", tc.name, got, tc.wantLanguage)
	}
	if scoped.ResolvedScope.LanguageID == "go" {
		t.Fatalf("ResolveManagerForFile(%s) defaulted fallback document to Go", tc.name)
	}
	if got := scoped.ResolvedScope.RootKind; got != "document_fallback" {
		t.Fatalf("ResolveManagerForFile(%s) root kind = %q, want document_fallback", tc.name, got)
	}
	assertDocumentFallbackSymbol(t, ctx, scoped, target, tc.name, tc.wantSymbol)
}

func TestRuntimeRootUsesWorkspaceRootsEnvWhenPrimaryRootMissing(t *testing.T) {
	primary := t.TempDir()
	extra := t.TempDir()
	t.Setenv("GO_AGENT_LSP_ROOT", "")
	setRuntimeWorkspaceRootsEnv(t, primary, extra)

	got, err := runtimeRoot()
	if err != nil {
		t.Fatalf("runtimeRoot() error = %v", err)
	}
	if got != primary {
		t.Fatalf("runtimeRoot() = %q, want first GO_AGENT_LSP_ROOTS entry %q", got, primary)
	}
}

func TestRuntimeRootRejectsExplicitEmptyWorkspaceRootsEnv(t *testing.T) {
	t.Setenv("GO_AGENT_LSP_ROOT", "")
	t.Setenv("GO_AGENT_LSP_ROOTS", `[]`)

	_, err := runtimeRoot()
	if err == nil {
		t.Fatal("runtimeRoot() error = nil, want explicit empty workspace roots failure")
	}
}

func TestRuntimeRootRejectsEmptyWorkspaceRootsEnvEvenWithPrimaryRoot(t *testing.T) {
	t.Setenv("GO_AGENT_LSP_ROOT", t.TempDir())
	t.Setenv("GO_AGENT_LSP_ROOTS", `[]`)

	_, err := runtimeRoot()
	if err == nil {
		t.Fatal("runtimeRoot() error = nil, want GO_AGENT_LSP_ROOTS to fail closed when explicitly empty")
	}
}

func TestRuntimeRootRejectsMissingWorkspaceRootEnv(t *testing.T) {
	unsetEnvForTest(t, "GO_AGENT_LSP_ROOT")
	unsetEnvForTest(t, "GO_AGENT_LSP_ROOTS")

	_, err := runtimeRoot()
	if err == nil {
		t.Fatal("runtimeRoot() error = nil, want missing workspace root env to fail closed")
	}
}

func TestRuntimeWorkspaceRootsResolveRelativeRootsAgainstPrimaryRoot(t *testing.T) {
	unsetEnvForTest(t, "GO_AGENT_LSP_ROOT")
	primary := t.TempDir()
	setRuntimeWorkspaceRootsEnv(t, primary, "packages/api")

	got, err := runtimeWorkspaceRoots()
	if err != nil {
		t.Fatalf("runtimeWorkspaceRoots() error = %v", err)
	}
	want := []string{primary, filepath.Join(primary, "packages/api")}
	if len(got) != len(want) {
		t.Fatalf("runtimeWorkspaceRoots() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runtimeWorkspaceRoots()[%d] = %q, want %q; all roots %#v", i, got[i], want[i], got)
		}
	}
}

func TestRuntimeWorkspaceRootsRejectRelativePrimaryRoot(t *testing.T) {
	unsetEnvForTest(t, "GO_AGENT_LSP_ROOT")
	t.Setenv("GO_AGENT_LSP_ROOTS", `["packages/api"]`)

	_, err := runtimeWorkspaceRoots()
	if err == nil {
		t.Fatal("runtimeWorkspaceRoots() error = nil, want relative primary root failure")
	}
}

func TestRuntimeWorkspaceRootsRejectEmptyPrimaryWithAbsoluteAdditionalRoot(t *testing.T) {
	unsetEnvForTest(t, "GO_AGENT_LSP_ROOT")
	extra := t.TempDir()
	setRuntimeWorkspaceRootsEnv(t, "", extra)

	_, err := runtimeWorkspaceRoots()
	if err == nil {
		t.Fatal("runtimeWorkspaceRoots() error = nil, want missing primary root failure")
	}
}

func setRuntimeWorkspaceRootsEnv(t *testing.T, roots ...string) {
	t.Helper()
	raw, err := json.Marshal(roots)
	if err != nil {
		t.Fatalf("marshal GO_AGENT_LSP_ROOTS: %v", err)
	}
	t.Setenv("GO_AGENT_LSP_ROOTS", string(raw))
}

func writeRuntimeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runtimeCanonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(temp dir): %v", err)
	}
	return root
}

func TestRuntimeServerBinaryPrefersInstalledBinaryOverride(t *testing.T) {
	installed := filepath.Join(t.TempDir(), "gopls")
	if got := runtimeServerBinary("gopls", installed); got != installed {
		t.Fatalf("runtimeServerBinary() = %q, want installed override %q", got, installed)
	}
}

func TestRuntimeAdapterDiagnosticsMaxWaitCoversAllLSPClientAdapters(t *testing.T) {
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	for _, languageID := range []string{"go", "typescript", "python", "rust", "java", "css", "shellscript"} {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("missing adapter for %s", languageID)
		}
		if got := runtimeAdapterDiagnosticsMaxWait(adapter); got != lspDiagnosticsColdStartMaxWait {
			t.Fatalf("runtimeAdapterDiagnosticsMaxWait(%s) = %s, want %s", languageID, got, lspDiagnosticsColdStartMaxWait)
		}
	}
}

func TestRuntimeAdapterDiagnosticsMaxWaitKeepsFallbackAdaptersDefault(t *testing.T) {
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	for _, languageID := range []string{"markdown", "json", "yaml"} {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("missing adapter for %s", languageID)
		}
		if got := runtimeAdapterDiagnosticsMaxWait(adapter); got != 0 {
			t.Fatalf("runtimeAdapterDiagnosticsMaxWait(%s) = %s, want default manager wait", languageID, got)
		}
	}
}

func TestRuntimeAdapterInitOptionsPackagedPythonDisablesSystemInterpreterProbe(t *testing.T) {
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage("python")
	if !ok {
		t.Fatal("missing python adapter")
	}

	initOptions := runtimeAdapterInitOptions(adapter, true)
	settings, ok := initOptions["settings"].(map[string]any)
	if !ok {
		t.Fatalf("runtimeAdapterInitOptions(python, packaged) = %#v, want settings", initOptions)
	}
	python, ok := settings["python"].(map[string]any)
	if !ok {
		t.Fatalf("python settings = %#v, want map", settings["python"])
	}
	if got := python["pythonPath"]; got != "/__super_dolphin_no_system_python__/python" {
		t.Fatalf("python.pythonPath = %#v, want packaged no-system interpreter sentinel", got)
	}
}

func TestRuntimePrimaryLanguageIDsIncludeShellscript(t *testing.T) {
	if !slices.Contains(runtimePrimaryLanguageIDs(), "shellscript") {
		t.Fatalf("runtimePrimaryLanguageIDs() = %#v, missing shellscript", runtimePrimaryLanguageIDs())
	}
}

func TestSetupInstallerRegistersShellLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "bash-language-server")
	fakeServer := filepath.Join(binDir, mcpLSPExecutableFileName("bash-language-server"))
	writeMcpLSPExecutable(t, binDir, "shellcheck")
	t.Setenv("PATH", binDir)

	result, err := setupInstaller().EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "shellscript")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(shellscript) error = %v", err)
	}
	if result.Binary != "bash-language-server" {
		t.Fatalf("shell installer binary = %q, want bash-language-server", result.Binary)
	}
	if result.Path != fakeServer {
		t.Fatalf("shell installer path = %q, want %q", result.Path, fakeServer)
	}
}

func TestSetupInstallerInstallsShellcheckWhenShellServerAlreadyExists(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "bash-language-server")
	fakeServer := filepath.Join(binDir, mcpLSPExecutableFileName("bash-language-server"))
	fakeNPM := filepath.Join(binDir, mcpLSPExecutableFileName("npm"))
	marker := filepath.Join(binDir, "npm-called")
	script := `#!/bin/sh
set -eu
case " $* " in
  *" shellcheck "*) ;;
  *) echo "missing shellcheck install arg: $*" >&2; exit 1 ;;
esac
printf '%s\n' "$*" > "$FAKE_NPM_MARKER"
printf '#!/bin/sh\nexit 0\n' > "$FAKE_INSTALL_BIN/shellcheck"
/bin/chmod +x "$FAKE_INSTALL_BIN/shellcheck"
`
	if runtime.GOOS == "windows" {
		script = "@echo off\r\n" +
			"set \"ARGS=%*\"\r\n" +
			"if \"%ARGS:shellcheck=%\"==\"%ARGS%\" (\r\n" +
			"  echo missing shellcheck install arg: %* 1>&2\r\n" +
			"  exit /b 1\r\n" +
			")\r\n" +
			"echo %*>\"%FAKE_NPM_MARKER%\"\r\n" +
			"(\r\n" +
			"  echo @echo off\r\n" +
			"  echo exit /b 0\r\n" +
			") > \"%FAKE_INSTALL_BIN%\\shellcheck.cmd\"\r\n" +
			"exit /b 0\r\n"
	}
	if err := os.WriteFile(fakeNPM, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("FAKE_INSTALL_BIN", binDir)
	t.Setenv("FAKE_NPM_MARKER", marker)

	result, err := setupInstaller().EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "shellscript")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(shellscript) error = %v", err)
	}
	if result.Path != fakeServer {
		t.Fatalf("shell installer path = %q, want %q", result.Path, fakeServer)
	}
	if _, err := os.Stat(filepath.Join(binDir, mcpLSPExecutableFileName("shellcheck"))); err != nil {
		t.Fatalf("shellcheck dependency was not installed: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("shell installer did not invoke npm when shellcheck was missing: %v", err)
	}
}

func TestNewManagerPackagedRegistersOnlyBundledLanguageServers(t *testing.T) {
	root := t.TempDir()
	bundle := t.TempDir()
	writeMcpLSPBundleManifest(t, bundle, `{
  "servers": {
    "go": {"path": "bin/gopls", "languages": ["go", "gomod", "gosum", "gowork"]},
    "typescript": {"path": "node_modules/.bin/typescript-language-server", "languages": ["javascript", "javascriptreact", "typescript", "typescriptreact"]}
  }
}
`)
	writeMcpLSPExecutable(t, filepath.Join(bundle, "bin"), "gopls")
	writeMcpLSPExecutable(t, filepath.Join(bundle, "node_modules", ".bin"), "typescript-language-server")
	t.Setenv("GO_AGENT_LSP_ROOT", root)
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", bundle)
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", filepath.Join(bundle, "manifest.json"))
	t.Setenv("PATH", t.TempDir())

	cfg, err := platformconfig.New()
	if err != nil {
		t.Fatalf("platform config: %v", err)
	}
	mgr, err := newManager(cfg)
	if err != nil {
		t.Fatalf("newManager() error = %v", err)
	}
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, Family: "lsp"})
	if _, err := mgr.registry.GetManagerForLanguage(ctx, "go"); err != nil {
		t.Fatalf("bundled go manager error = %v", err)
	}
	if _, err := mgr.registry.GetManagerForLanguage(ctx, "javascript"); err != nil {
		t.Fatalf("bundled javascript manager error = %v", err)
	}
	_, err = mgr.registry.GetManagerForLanguage(ctx, "python")
	if !errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		t.Fatalf("python manager error = %v, want unsupported because python is not in bundled LSP manifest", err)
	}
}

func TestNewManagerPackagedStandardBundleRegistersNonJDTLSLanguages(t *testing.T) {
	root := t.TempDir()
	bundle := t.TempDir()
	writeMcpLSPBundleManifest(t, bundle, `{
  "servers": {
    "gopls": {"path": "bin/gopls", "languages": ["go", "gomod", "gosum", "gowork"]},
    "typescript-language-server": {"path": "bin/typescript-language-server", "languages": ["javascript", "javascriptreact", "typescript", "typescriptreact"]},
    "vscode-langservers-extracted": {"path": "bin/vscode-css-language-server", "languages": ["css"]},
    "pyright": {"path": "bin/pyright-langserver", "languages": ["python"]},
    "rust-analyzer": {"path": "bin/rust-analyzer", "languages": ["rust"]},
    "bash-language-server": {"path": "bin/bash-language-server", "languages": ["shellscript"]}
  }
}
`)
	for _, name := range []string{
		"gopls",
		"typescript-language-server",
		"vscode-css-language-server",
		"pyright-langserver",
		"rust-analyzer",
		"bash-language-server",
	} {
		writeMcpLSPExecutable(t, filepath.Join(bundle, "bin"), name)
	}
	t.Setenv("GO_AGENT_LSP_ROOT", root)
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", bundle)
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", filepath.Join(bundle, "manifest.json"))
	t.Setenv("PATH", t.TempDir())

	cfg, err := platformconfig.New()
	if err != nil {
		t.Fatalf("platform config: %v", err)
	}
	mgr, err := newManager(cfg)
	if err != nil {
		t.Fatalf("newManager() error = %v", err)
	}
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, Family: "lsp"})
	for _, languageID := range []string{
		"go",
		"gomod",
		"gosum",
		"gowork",
		"javascript",
		"javascriptreact",
		"typescript",
		"typescriptreact",
		"css",
		"python",
		"rust",
		"shellscript",
	} {
		if _, err := mgr.registry.GetManagerForLanguage(ctx, languageID); err != nil {
			t.Fatalf("bundled %s manager error = %v", languageID, err)
		}
	}
	_, err = mgr.registry.GetManagerForLanguage(ctx, "java")
	if !errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		t.Fatalf("java manager error = %v, want unsupported because jdtls is not in standard bundle", err)
	}
}

func assertDocumentFallbackSymbol(
	t *testing.T,
	ctx context.Context,
	scoped lspmanager.ScopedManager,
	target string,
	name string,
	want string,
) {
	t.Helper()
	symbols, err := scoped.Manager.DocumentSymbol(ctx, target)
	if err != nil {
		t.Fatalf("DocumentSymbol(%s): %v", name, err)
	}
	if len(symbols) == 0 || symbols[0].Name != want {
		t.Fatalf("DocumentSymbol(%s) = %#v, want first symbol %q", name, symbols, want)
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%q): %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func writeMcpLSPBundleManifest(t *testing.T, bundle, body string) {
	t.Helper()
	path := filepath.Join(bundle, "manifest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(normalizeMcpLSPBundleManifestForTest(body)), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func writeMcpLSPExecutable(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	path := filepath.Join(dir, mcpLSPExecutableFileName(name))
	body := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		body = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func mcpLSPExecutableFileName(name string) string {
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		return name + ".cmd"
	}
	return name
}

func normalizeMcpLSPBundleManifestForTest(body string) string {
	if runtime.GOOS != "windows" {
		return body
	}
	for _, path := range []string{
		"bin/gopls",
		"bin/typescript-language-server",
		"node_modules/.bin/typescript-language-server",
		"bin/vscode-css-language-server",
		"bin/pyright-langserver",
		"bin/rust-analyzer",
		"bin/bash-language-server",
	} {
		body = strings.ReplaceAll(body, `"`+path+`"`, `"`+path+`.cmd"`)
	}
	return body
}
