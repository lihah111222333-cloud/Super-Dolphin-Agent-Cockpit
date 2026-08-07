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

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

type testStdioServer struct{ err error }

func (s testStdioServer) Run(context.Context) error { return s.err }

type testStdioCloser struct{ err error }

func (c testStdioCloser) Close() error { return c.err }

func TestStdioRunnerJoinsRunAndCloseErrors(t *testing.T) {
	runErr := errors.New("run failed")
	closeErr := errors.New("close failed")
	for _, tc := range []struct {
		name     string
		runErr   error
		closeErr error
	}{
		{name: "both nil"},
		{name: "run only", runErr: runErr},
		{name: "close only", closeErr: closeErr},
		{name: "both", runErr: runErr, closeErr: closeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (stdioRunner{server: testStdioServer{err: tc.runErr}, manager: testStdioCloser{err: tc.closeErr}}).Run(context.Background())
			if errors.Is(err, runErr) != (tc.runErr != nil) || errors.Is(err, closeErr) != (tc.closeErr != nil) {
				t.Fatalf("Run() error = %v, run=%v close=%v", err, tc.runErr, tc.closeErr)
			}
		})
	}
}

func TestNewManagerRegistersDocumentLanguageAdapters(t *testing.T) {
	declareTestDependencyBootstrap(t)
	root := runtimeCanonicalTempDir(t)
	t.Setenv("GO_AGENT_LSP_ROOT", root)
	binDir := t.TempDir()
	for _, name := range []string{
		"vscode-json-language-server",
		"vscode-markdown-language-server",
		"yaml-language-server",
	} {
		writeMcpLSPExecutable(t, binDir, name)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

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
		wantRootKind string
	}{
		{name: "README.md", body: "# Title\n", wantLanguage: "markdown", wantRootKind: "markdown_project"},
		{name: "config.json", body: "{\n  \"name\": \"demo\"\n}\n", wantLanguage: "json", wantRootKind: "dir_fallback"},
		{name: "config.yaml", body: "name: demo\n", wantLanguage: "yaml", wantRootKind: "dir_fallback"},
	}

	for _, tc := range cases {
		assertDocumentLanguageCase(t, root, ctx, resolver, tc)
	}
}

func TestSetupInstallerPrefersNPMGlobalBinaryOverPNPMCommandShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX npm and pnpm command shims")
	}
	prefix := t.TempDir()
	shadowBin := filepath.Join(prefix, "shadow-bin")
	npmPrefix := filepath.Join(prefix, "npm-prefix")
	shadowBinary := filepath.Join(shadowBin, "vscode-markdown-language-server")
	globalBinary := filepath.Join(npmPrefix, "bin", "vscode-markdown-language-server")
	writeRuntimeExecutable(t, shadowBinary, "#!/bin/sh\nexit 9\n# cmd-shim-target=/invalid/pnpm/markdown-server\n")
	writeRuntimeExecutable(t, globalBinary, "#!/bin/sh\nexit 0\n")
	writeRuntimeExecutable(t, filepath.Join(shadowBin, "npm"), "#!/bin/sh\nprintf '%s\\n' '"+npmPrefix+"'\n")
	t.Setenv("PATH", shadowBin)

	result, err := setupInstaller().EnsureInstalledDetailed(
		lspinstaller.WithToolCallInstallCheckOnly(context.Background()),
		"markdown",
	)
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(markdown) error = %v", err)
	}
	if result.Path != globalBinary {
		t.Fatalf("EnsureInstalledDetailed(markdown).Path = %q, want npm global binary %q", result.Path, globalBinary)
	}
}

func TestNewManagerUsesPlatformLSPConfig(t *testing.T) {
	declareTestDependencyBootstrap(t)
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

func assertDocumentLanguageCase(
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
		wantRootKind string
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
		t.Fatalf("ResolveManagerForFile(%s) defaulted document language to Go", tc.name)
	}
	if got := scoped.ResolvedScope.RootKind; got != tc.wantRootKind {
		t.Fatalf("ResolveManagerForFile(%s) root kind = %q, want %q", tc.name, got, tc.wantRootKind)
	}
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

func writeRuntimeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
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

func TestRuntimeServerArgsSharesOnlyCompatibleGoplsEnvironments(t *testing.T) {
	binary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	first := mustRuntimeServerArgs(t, command, binary, []string{"GOOS=darwin", "GOARCH=arm64"})
	reordered := mustRuntimeServerArgs(t, command, binary, []string{"GOARCH=arm64", "GOOS=darwin"})
	incompatible := mustRuntimeServerArgs(t, command, binary, []string{"GOOS=darwin", "GOARCH=arm64", "GOWORK=off"})
	if !slices.Equal(first, reordered) {
		t.Fatalf("equivalent gopls environments produced different cohorts: first=%v reordered=%v", first, reordered)
	}
	if slices.Equal(first, incompatible) {
		t.Fatalf("incompatible gopls environments reused one cohort: first=%v incompatible=%v", first, incompatible)
	}
	if first[1] != command.Args[1] {
		t.Fatalf("runtimeServerArgs changed daemon timeout arg: got=%v command=%v", first, command.Args)
	}
}

func TestRuntimeServerArgsUsesCanonicalGitCommonDirForGoplsRootCohort(t *testing.T) {
	firstRoot, secondRoot := writeRuntimeLinkedWorktreeFixture(t)
	binary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	first := mustRuntimeServerArgsForRoot(t, command, binary, firstRoot)
	second := mustRuntimeServerArgsForRoot(t, command, binary, secondRoot)
	if runtimeServerGoplsRemoteID(first) == "" || runtimeServerGoplsRemoteID(first) != runtimeServerGoplsRemoteID(second) {
		t.Fatalf("linked worktrees did not share canonical gopls root cohort: first=%v second=%v", first, second)
	}
	unrelatedRoot := t.TempDir()
	unrelated := mustRuntimeServerArgsForRoot(t, command, binary, unrelatedRoot)
	if runtimeServerGoplsRemoteID(first) == runtimeServerGoplsRemoteID(unrelated) {
		t.Fatalf("unrelated root reused gopls cohort: first=%v unrelated=%v", first, unrelated)
	}
}

func TestRuntimeServerGoplsRootCohortConfigHasTypedProof(t *testing.T) {
	root := t.TempDir()
	binary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	config, err := runtimeServerGoplsRootCohortConfig(
		multilsp.ServerCommand{Executable: "gopls", Args: []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"}},
		binary,
		root,
		[]string{"GOOS=darwin"},
	)
	if err != nil {
		t.Fatalf("runtimeServerGoplsRootCohortConfig() error = %v", err)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("GoplsRootCohortConfig.Validate() error = %v", err)
	}
	proof := config.RepositoryInstanceProof
	if proof.CanonicalRootDigest == "" || proof.FilesystemIdentity == "" || proof.GitMarkerDigest == "" || proof.InstanceNonce == "" {
		t.Fatalf("typed repository proof is incomplete: %#v", proof)
	}
}

func TestRuntimeServerGoplsRootProofIsStableAcrossLinkedWorktrees(t *testing.T) {
	firstRoot, secondRoot := writeRuntimeLinkedWorktreeFixture(t)
	binary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{Executable: "gopls", Args: []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"}}
	first, err := runtimeServerGoplsRootCohortConfig(command, binary, firstRoot, []string{"GOOS=darwin"})
	if err != nil {
		t.Fatalf("runtimeServerGoplsRootCohortConfig(first) error = %v", err)
	}
	second, err := runtimeServerGoplsRootCohortConfig(command, binary, secondRoot, []string{"GOOS=darwin"})
	if err != nil {
		t.Fatalf("runtimeServerGoplsRootCohortConfig(second) error = %v", err)
	}
	if first.RepositoryInstanceProof != second.RepositoryInstanceProof || first.CohortID != second.CohortID {
		t.Fatalf("linked worktree root proof split: first=%#v second=%#v", first, second)
	}
}

func mustRuntimeServerArgsForRoot(t *testing.T, command multilsp.ServerCommand, binary, root string) []string {
	t.Helper()
	args, err := runtimeServerArgsForOS(command, binary, []string{"GOOS=darwin"}, "darwin", root)
	if err != nil {
		t.Fatalf("runtimeServerArgsForOS(root=%q) error = %v", root, err)
	}
	return args
}

func TestRuntimeServerArgsSeparatesAmbientGoBuildEnvironments(t *testing.T) {
	binary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	t.Setenv("GOPROXY", "https://proxy-one.invalid")
	first := mustRuntimeServerArgs(t, command, binary, []string{"GOOS=darwin", "GOARCH=arm64"})
	t.Setenv("GOPROXY", "https://proxy-two.invalid")
	second := mustRuntimeServerArgs(t, command, binary, []string{"GOOS=darwin", "GOARCH=arm64"})
	if slices.Equal(first, second) {
		t.Fatalf("different ambient Go build environments reused one cohort: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerArgsSeparatesDaemonIdleTimeouts(t *testing.T) {
	binary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	firstCommand := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	secondCommand := firstCommand
	secondCommand.Args = []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=2s"}
	first := mustRuntimeServerArgs(t, firstCommand, binary, []string{"GOOS=darwin", "GOARCH=arm64"})
	second := mustRuntimeServerArgs(t, secondCommand, binary, []string{"GOOS=darwin", "GOARCH=arm64"})
	if slices.Equal(first[:1], second[:1]) {
		t.Fatalf("different daemon idle timeouts reused one cohort: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerArgsLeavesNonSharedServerUnchanged(t *testing.T) {
	command := multilsp.ServerCommand{Executable: "pyright-langserver", Args: []string{"--stdio"}}
	got := mustRuntimeServerArgs(t, command, "pyright-langserver", []string{"GOOS=darwin"})
	if !slices.Equal(got, command.Args) {
		t.Fatalf("runtimeServerArgs(non-shared) = %v, want %v", got, command.Args)
	}
}

func TestRuntimeServerArgsDisablesUnsupportedWindowsGoplsAutoDaemon(t *testing.T) {
	command := multilsp.ServerCommand{
		Executable: "gopls.exe",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	got, err := runtimeServerArgsForOS(command, "gopls.exe", nil, "windows")
	if err != nil {
		t.Fatalf("runtimeServerArgsForOS(windows) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("runtimeServerArgsForOS(windows) = %v, want local gopls without unsupported auto daemon flags", got)
	}
}

func TestRuntimeServerArgsSeparatesDifferentGoplsBinaryContents(t *testing.T) {
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	firstBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	secondBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 1\n")
	first := mustRuntimeServerArgs(t, command, firstBinary, []string{"GOOS=darwin"})
	second := mustRuntimeServerArgs(t, command, secondBinary, []string{"GOOS=darwin"})
	if runtimeServerGoplsRemoteID(first) == runtimeServerGoplsRemoteID(second) {
		t.Fatalf("different gopls contents reused remote ID: first=%v second=%v", first, second)
	}
}

func mustRuntimeServerArgs(t *testing.T, command multilsp.ServerCommand, binary string, env []string) []string {
	t.Helper()
	args, err := runtimeServerArgs(command, binary, env)
	if err != nil {
		t.Fatalf("runtimeServerArgs() error = %v", err)
	}
	return args
}

func TestRuntimeAdapterDiagnosticsMaxWaitCoversAllLSPClientAdapters(t *testing.T) {
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	for _, languageID := range runtimePrimaryLanguageIDs() {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("missing adapter for %s", languageID)
		}
		if !adapter.CapabilityPolicy().RequiresLSPClient {
			continue
		}
		if got := runtimeAdapterDiagnosticsMaxWait(adapter); got != lspDiagnosticsColdStartMaxWait {
			t.Fatalf("runtimeAdapterDiagnosticsMaxWait(%s) = %s, want %s", languageID, got, lspDiagnosticsColdStartMaxWait)
		}
	}
}

func TestRuntimeLanguageUsesNodePreservesMembership(t *testing.T) {
	tests := []struct {
		languageID string
		want       bool
	}{
		{languageID: "css", want: true},
		{languageID: "dockerfile", want: true},
		{languageID: "graphql", want: true},
		{languageID: "html", want: true},
		{languageID: "javascript", want: true},
		{languageID: "javascriptreact", want: true},
		{languageID: "json", want: true},
		{languageID: "markdown", want: true},
		{languageID: "php", want: true},
		{languageID: "prisma", want: true},
		{languageID: "python", want: true},
		{languageID: "shellscript", want: true},
		{languageID: "svelte", want: true},
		{languageID: "typescript", want: true},
		{languageID: "typescriptreact", want: true},
		{languageID: "vue", want: true},
		{languageID: "yaml", want: true},
		{languageID: "  TypeScript  ", want: true},
		{languageID: "go", want: false},
		{languageID: "plaintext", want: false},
		{languageID: "", want: false},
	}
	for _, test := range tests {
		if got := runtimeLanguageUsesNode(test.languageID); got != test.want {
			t.Errorf("runtimeLanguageUsesNode(%q) = %t, want %t", test.languageID, got, test.want)
		}
	}
}

func TestRuntimeDocumentFallbackLanguagesDefaultEmpty(t *testing.T) {
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	for _, languageID := range []string{"markdown", "json", "yaml"} {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("missing adapter for %s", languageID)
		}
		if !adapter.CapabilityPolicy().RequiresLSPClient {
			t.Fatalf("%s adapter should use a real LSP client", languageID)
		}
	}
	if fallback, ok := registry.AdapterForLanguage("plaintext"); ok {
		t.Fatalf("unexpected default document fallback adapter: %#v", fallback)
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

func TestRuntimeJSTSInitOptionsResolveInstalledTSServerPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX npm binary symlink")
	}
	binDir, typeScriptRoot := writeTypeScriptNPMFixture(t)
	t.Setenv("PATH", binDir)
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage("typescript")
	if !ok {
		t.Fatal("missing typescript adapter")
	}

	serverBinary := filepath.Join(binDir, "typescript-language-server")
	initOptions := runtimeAdapterInitOptionsWithBinary(adapter, false, serverBinary)
	tsserver, ok := initOptions["tsserver"].(map[string]any)
	if !ok {
		t.Fatalf("typescript init options = %#v, want tsserver map", initOptions)
	}
	if got := runtimeStringOption(tsserver["fallbackPath"]); got != typeScriptRoot {
		t.Fatalf("tsserver fallbackPath = %q, want %q", got, typeScriptRoot)
	}
}

func TestRuntimeJSTSInitOptionsUseSingleBoundedTSServer(t *testing.T) {
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage("typescript")
	if !ok {
		t.Fatal("missing typescript adapter")
	}

	initOptions := runtimeAdapterInitOptionsWithBinary(adapter, false, "")
	if got := initOptions["maxTsServerMemory"]; got != runtimeJSTSMaxMemoryMB {
		t.Fatalf("maxTsServerMemory = %#v, want %d", got, runtimeJSTSMaxMemoryMB)
	}
	tsserver, ok := initOptions["tsserver"].(map[string]any)
	if !ok {
		t.Fatalf("typescript init options = %#v, want tsserver map", initOptions)
	}
	if got := tsserver["useSyntaxServer"]; got != runtimeJSTSUseSyntaxServer {
		t.Fatalf("tsserver.useSyntaxServer = %#v, want %q", got, runtimeJSTSUseSyntaxServer)
	}
}

func TestRuntimeJSTSInitOptionsResolvePackagedWrapperTSServerPath(t *testing.T) {
	binDir, typeScriptRoot := writeTypeScriptNPMFixture(t)
	serverBinary := filepath.Join(binDir, "typescript-language-server")
	if err := os.Remove(serverBinary); err != nil {
		t.Fatalf("remove npm symlink: %v", err)
	}
	if err := os.WriteFile(serverBinary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write packaged wrapper: %v", err)
	}
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage("typescript")
	if !ok {
		t.Fatal("missing typescript adapter")
	}

	initOptions := runtimeAdapterInitOptionsWithBinary(adapter, true, serverBinary)
	tsserver := initOptions["tsserver"].(map[string]any)
	if got := runtimeStringOption(tsserver["fallbackPath"]); got != typeScriptRoot {
		t.Fatalf("packaged tsserver fallbackPath = %q, want %q", got, typeScriptRoot)
	}
}

func TestRuntimeTypeScriptModuleRootResolvesPNPMCommandShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX pnpm command shim")
	}
	prefix := t.TempDir()
	binDir := filepath.Join(prefix, "bin")
	typeScriptRoot := filepath.Join(prefix, "store", "node_modules", "typescript")
	target := filepath.Join(typeScriptRoot, "bin", "tsserver")
	writeRuntimeExecutable(t, target, "#!/bin/sh\nexit 0\n")
	writeRuntimeTestFile(t, filepath.Join(typeScriptRoot, "lib", "tsserver.js"), "fixture\n")
	writeRuntimeExecutable(t, filepath.Join(binDir, "tsserver"), "#!/bin/sh\nexit 0\n# cmd-shim-target="+target+"\n")
	t.Setenv("PATH", binDir)

	if got := runtimeTypeScriptModuleRoot(""); got != typeScriptRoot {
		t.Fatalf("runtimeTypeScriptModuleRoot() = %q, want pnpm TypeScript root %q", got, typeScriptRoot)
	}
}

func writeTypeScriptNPMFixture(t *testing.T) (string, string) {
	t.Helper()
	prefix := t.TempDir()
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir npm bin: %v", err)
	}
	nodeModules := filepath.Join(prefix, "lib", "node_modules")
	languageServerCLI := filepath.Join(nodeModules, "typescript-language-server", "lib", "cli.mjs")
	typeScriptRoot := filepath.Join(nodeModules, "typescript")
	for _, path := range []string{languageServerCLI, filepath.Join(typeScriptRoot, "lib", "tsserver.js")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir npm fixture: %v", err)
		}
		mode := os.FileMode(0o600)
		if path == languageServerCLI {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte("fixture\n"), mode); err != nil {
			t.Fatalf("write npm fixture: %v", err)
		}
	}
	if err := os.Symlink(languageServerCLI, filepath.Join(binDir, "typescript-language-server")); err != nil {
		t.Fatalf("symlink typescript-language-server: %v", err)
	}
	canonicalTypeScriptRoot, err := filepath.EvalSymlinks(typeScriptRoot)
	if err != nil {
		t.Fatalf("canonicalize TypeScript fixture root: %v", err)
	}
	return binDir, canonicalTypeScriptRoot
}

func TestRuntimePrimaryLanguageIDsIncludeShellscriptAndSQL(t *testing.T) {
	for _, languageID := range []string{"shellscript", "proto", "sql"} {
		if !slices.Contains(runtimePrimaryLanguageIDs(), languageID) {
			t.Fatalf("runtimePrimaryLanguageIDs() = %#v, missing %s", runtimePrimaryLanguageIDs(), languageID)
		}
	}
}

func TestSetupInstallerRegistersBufProtoLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "buf")
	bufBinary := filepath.Join(binDir, mcpLSPExecutableFileName("buf"))
	t.Setenv("PATH", binDir)

	result, err := setupInstaller().EnsureInstalledDetailed(lspinstaller.WithToolCallInstallCheckOnly(context.Background()), "proto")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(proto) error = %v", err)
	}
	if result.Binary != "buf" || result.Path != bufBinary {
		t.Fatalf("proto installer result = %#v, want buf at %q", result, bufBinary)
	}
}

func TestSetupInstallerReportsMissingBufBinaryForProto(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := setupInstaller().EnsureInstalledDetailed(lspinstaller.WithToolCallInstallCheckOnly(context.Background()), "proto")
	if err == nil {
		t.Fatal("EnsureInstalledDetailed(proto) error = nil, want missing binary")
	}
	var missing *lspinstaller.MissingBinaryError
	if !errors.As(err, &missing) {
		t.Fatalf("EnsureInstalledDetailed(proto) error = %T %v, want MissingBinaryError", err, err)
	}
	if languageID, binaryName := missing.MissingLSPBinary(); languageID != "proto" || binaryName != "buf" {
		t.Fatalf("missing proto binary = (%q, %q), want (proto, buf)", languageID, binaryName)
	}
	if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		t.Fatalf("missing proto binary error was classified as unsupported language: %v", err)
	}
}

func TestSetupInstallerRegistersSQLiteSQLLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "sqruff")
	fakeServer := filepath.Join(binDir, mcpLSPExecutableFileName("sqruff"))
	t.Setenv("PATH", binDir)

	result, err := setupInstaller().EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "sql")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(sql) error = %v", err)
	}
	if result.Binary != "sqruff" || result.Path != fakeServer {
		t.Fatalf("sql installer result = %#v, want sqruff at %q", result, fakeServer)
	}
}

func TestSetupInstallerInstallsPinnedSQLiteSQLLanguageServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX fake cargo script")
	}
	binDir := t.TempDir()
	cargoHome := filepath.Join(t.TempDir(), "cargo-home")
	fakeCargo := filepath.Join(binDir, "cargo")
	marker := filepath.Join(t.TempDir(), "cargo-args")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" > "$CARGO_ARGS_MARKER"
/bin/mkdir -p "$CARGO_HOME/bin"
/bin/cat > "$CARGO_HOME/bin/sqruff" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  echo "sqruff 0.38.0"
fi
exit 0
EOF
/bin/chmod +x "$CARGO_HOME/bin/sqruff"
`
	if err := os.WriteFile(fakeCargo, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake cargo: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CARGO_HOME", cargoHome)
	t.Setenv("CARGO_ARGS_MARKER", marker)

	if _, err := setupInstaller().EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "sql"); err != nil {
		t.Fatalf("EnsureInstalledDetailed(sql) error = %v", err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read cargo args: %v", err)
	}
	want := "install sqruff --version " + sqruffInstallVersion + " --locked"
	if strings.TrimSpace(string(raw)) != want {
		t.Fatalf("cargo args = %q, want %q", strings.TrimSpace(string(raw)), want)
	}
}

func TestSetupInstallerRegistersSQLLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "sqruff")
	fakeServer := filepath.Join(binDir, mcpLSPExecutableFileName("sqruff"))
	t.Setenv("PATH", binDir)

	result, err := setupInstaller().EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "sql")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(sql) error = %v", err)
	}
	if result.Binary != "sqruff" {
		t.Fatalf("sql installer binary = %q, want sqruff", result.Binary)
	}
	if result.Path != fakeServer {
		t.Fatalf("sql installer path = %q, want %q", result.Path, fakeServer)
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

func declareTestDependencyBootstrap(t *testing.T) {
	t.Helper()
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "")
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
		"bin/sqruff",
	} {
		body = strings.ReplaceAll(body, `"`+path+`"`, `"`+path+`.cmd"`)
	}
	return body
}
