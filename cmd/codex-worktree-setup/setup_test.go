package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolvePathsUsesWorktreeOwnedDefaults(t *testing.T) {
	root := t.TempDir()
	got, err := resolvePaths(context.Background(), setupOptions{Worktree: root})
	if err != nil {
		t.Fatalf("resolvePaths() error = %v", err)
	}
	wantBinary := "mcp-lsp"
	if runtime.GOOS == "windows" {
		wantBinary += ".exe"
	}
	if got.Worktree != canonicalTestPath(t, root) {
		t.Fatalf("Worktree = %q, want %q", got.Worktree, canonicalTestPath(t, root))
	}
	if got.Binary != filepath.Join(got.Worktree, "bin", wantBinary) {
		t.Fatalf("Binary = %q", got.Binary)
	}
	if got.Config != filepath.Join(got.Worktree, ".codex", "config.toml") {
		t.Fatalf("Config = %q", got.Config)
	}
}

func TestResolvePathsRejectsPathsOutsideWorktree(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	tests := []struct {
		name string
		opts setupOptions
		want string
	}{
		{name: "binary", opts: setupOptions{Worktree: root, Binary: filepath.Join(outside, "mcp-lsp")}, want: "binary path must stay inside worktree"},
		{name: "config", opts: setupOptions{Worktree: root, Config: filepath.Join(outside, "config.toml")}, want: "config path must stay inside worktree"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolvePaths(context.Background(), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestResolvePathsRejectsBinarySymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows junction coverage is exercised by containment normalization")
	}
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "mcp-lsp")
	if err := os.WriteFile(out, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "mcp-lsp")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	_, err := resolvePaths(context.Background(), setupOptions{Worktree: root, Binary: link})
	if err == nil || !strings.Contains(err.Error(), "binary path must stay inside worktree") {
		t.Fatalf("error = %v", err)
	}
}

func TestRenderConfigPreservesUserBytesAndIsIdempotent(t *testing.T) {
	original := []byte("# user setting\nservice_tier = \"fast\"\n")
	p := setupPaths{Worktree: "/repo", Binary: "/repo/bin/mcp-lsp", Config: "/repo/.codex/config.toml"}
	first, err := renderConfig(original, p, "/repo/bin:/usr/bin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderConfig(first, p, "/repo/bin:/usr/bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("renderConfig() is not idempotent\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !bytes.HasPrefix(first, original) {
		t.Fatalf("unmanaged bytes changed:\n%s", first)
	}
	for _, want := range []string{
		"required = true",
		"PROJECT_ROOT = \"/repo\"",
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE = \"production\"",
		"GO_AGENT_LSP_ROOTS = \"[\\\"/repo\\\"]\"",
		"[mcp_servers.lsp.tools.patch_edit]",
		"approval_mode = \"approve\"",
	} {
		if !bytes.Contains(first, []byte(want)) {
			t.Fatalf("rendered config missing %q:\n%s", want, first)
		}
	}
}

func TestRenderConfigRejectsMalformedOwnershipAndUnmanagedLSP(t *testing.T) {
	p := setupPaths{Worktree: "/repo", Binary: "/repo/bin/mcp-lsp", Config: "/repo/.codex/config.toml"}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "unmanaged", input: "[mcp_servers.lsp]\ncommand=\"user\"\n", want: "unmanaged mcp_servers.lsp"},
		{name: "begin only", input: managedBegin + "\n", want: "managed LSP markers"},
		{name: "end only", input: managedEnd + "\n", want: "managed LSP markers"},
		{name: "duplicate", input: managedBegin + "\n" + managedBegin + "\n" + managedEnd + "\n", want: "managed LSP markers"},
		{name: "reversed", input: managedEnd + "\n" + managedBegin + "\n", want: "managed LSP markers"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := renderConfig([]byte(tc.input), p, "/usr/bin")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestPreflightLanguageServersRequiresEveryRuntimeCompanion(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, executableName("gopls")))
	writeExecutable(t, filepath.Join(bin, executableName("typescript-language-server")))
	_, err := preflightLanguageServers(bin)
	if err == nil || !strings.Contains(err.Error(), "tsserver") {
		t.Fatalf("error = %v, want missing tsserver", err)
	}
	writeExecutable(t, filepath.Join(bin, executableName("tsserver")))
	got, err := preflightLanguageServers(bin)
	if err != nil {
		t.Fatalf("preflightLanguageServers() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("language binaries = %#v", got)
	}
}

func TestConfiguredPathDropsTransientCodexEntriesAndDuplicates(t *testing.T) {
	transient := filepath.Join(string(filepath.Separator), "Users", "dev", ".codex", "tmp", "arg0", "session")
	stable := filepath.Join(string(filepath.Separator), "usr", "local", "bin")
	got := configuredPath("/repo", strings.Join([]string{transient, stable, stable}, string(os.PathListSeparator)))
	if strings.Contains(filepath.ToSlash(got), "/.codex/tmp/") {
		t.Fatalf("configuredPath() retained transient entry: %q", got)
	}
	if strings.Count(got, stable) != 1 {
		t.Fatalf("configuredPath() did not deduplicate stable entry: %q", got)
	}
}

func TestValidateToolNamesRequiresExactShortSurface(t *testing.T) {
	want := append([]string(nil), requiredLSPTools...)
	if err := validateToolNames(want); err != nil {
		t.Fatalf("validateToolNames() error = %v", err)
	}
	if err := validateToolNames(want[:len(want)-1]); err == nil {
		t.Fatal("validateToolNames() error = nil for missing tool")
	}
	if err := validateToolNames(append(want, "extra")); err == nil {
		t.Fatal("validateToolNames() error = nil for extra tool")
	}
}

func TestParseCLIRejectsMissingAndUnknownCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"verify", "extra"}} {
		if _, err := parseCLI(args); err == nil {
			t.Fatalf("parseCLI(%q) error = nil", args)
		}
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(got)
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
}
