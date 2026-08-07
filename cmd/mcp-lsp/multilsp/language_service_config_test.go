package multilsp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestLanguageAdapterRegistryUsesConfiguredRootMarkers(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, filepath.Join(root, "custom.workspace"), "marker\n")
	writeGenericTestFile(t, target, "export const value = 1\n")

	cfg := contract.LSPConfig{
		ProjectAdapters: map[string]contract.LSPProjectAdapterConfig{
			contract.LSPServiceJSTS: {RootMarkers: []string{"custom.workspace"}},
		},
	}
	registry := NewLanguageAdapterRegistryFromConfig(cfg)

	adapter, ok := registry.AdapterForLanguage("typescript")
	if !ok {
		t.Fatal("missing configured typescript adapter")
	}
	resolved, err := adapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "typescript",
		TargetPath: target,
	}, target)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if resolved.WorkspaceRoot != root {
		t.Fatalf("configured typescript workspace root = %q, want %q", resolved.WorkspaceRoot, root)
	}
}

func TestCSharpAdapterDoesNotOverrideExplicitDotnetRoot(t *testing.T) {
	t.Setenv("DOTNET_ROOT", "/explicit/dotnet")
	registry := NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage("csharp")
	if !ok {
		t.Fatal("missing csharp adapter")
	}
	if env := adapter.EnvPolicy(ResolvedLanguageScope{}); len(env) != 0 {
		t.Fatalf("csharp EnvPolicy with explicit DOTNET_ROOT = %#v, want no override", env)
	}
}

func TestDotnetRootUsableRequiresRuntimeAndSDKDirs(t *testing.T) {
	root := t.TempDir()
	if dotnetRootUsable(root) {
		t.Fatal("empty dotnet root reported usable")
	}
	if err := os.MkdirAll(filepath.Join(root, "shared", "Microsoft.NETCore.App"), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if dotnetRootUsable(root) {
		t.Fatal("dotnet root without sdk dir reported usable")
	}
	if err := os.MkdirAll(filepath.Join(root, "sdk"), 0o755); err != nil {
		t.Fatalf("mkdir sdk dir: %v", err)
	}
	if !dotnetRootUsable(root) {
		t.Fatal("dotnet root with runtime and sdk dirs reported unusable")
	}
}

func TestLanguageAdapterRegistryFromConfigRegistersShellAdapter(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "scripts", "run.sh")
	writeGenericTestFile(t, filepath.Join(root, ".shell-root"), "marker\n")
	writeGenericTestFile(t, target, "#!/usr/bin/env bash\n")

	cfg := contract.LSPConfig{
		ProjectAdapters: map[string]contract.LSPProjectAdapterConfig{
			"shell": {
				RootMarkers:           []string{".shell-root"},
				IgnoredDirNames:       []string{"ignored-shell"},
				FirstSourceExtensions: []string{".sh", ".bash", ".zsh", ".ksh", ".bats"},
			},
		},
	}
	registry := NewLanguageAdapterRegistryFromConfig(cfg)

	adapter := mustShellAdapter(t, registry)
	assertShellCommand(t, adapter)

	resolved, err := adapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "shellscript",
		TargetPath: target,
	}, target)
	if err != nil {
		t.Fatalf("shell ResolveRoot: %v", err)
	}
	assertShellResolvedScope(t, resolved, root)
	assertShellBootstrapPolicy(t, adapter.BootstrapPolicy(resolved))
}

func TestLanguageAdapterRegistryFromConfigRegistersSQLAdapter(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "backend", "migrations", "schema", "096_order_client_id_idempotency.sql")
	writeGenericTestFile(t, filepath.Join(root, "sqlc.yaml"), "version: 2\n")
	writeGenericTestFile(t, target, "select 1;\n")

	cfg := contract.LSPConfig{
		ProjectAdapters: map[string]contract.LSPProjectAdapterConfig{
			contract.LSPServiceSQL: {
				RootMarkers:           []string{"sqlc.yaml"},
				IgnoredDirNames:       []string{"ignored-sql"},
				FirstSourceExtensions: []string{".sql"},
			},
		},
	}
	registry := NewLanguageAdapterRegistryFromConfig(cfg)

	adapter := mustSQLAdapter(t, registry)
	assertSQLCommand(t, adapter)

	resolved, err := adapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "sql",
		TargetPath: target,
	}, target)
	if err != nil {
		t.Fatalf("sql ResolveRoot: %v", err)
	}
	assertSQLResolvedScope(t, resolved, root)
	assertSQLBootstrapPolicy(t, adapter.BootstrapPolicy(resolved))

	if resolved.LanguageID != "sql" {
		t.Fatalf("SQLite SQL resolved language = %q, want sql", resolved.LanguageID)
	}
}

func mustShellAdapter(t *testing.T, registry *LanguageAdapterRegistry) LanguageAdapter {
	t.Helper()
	adapter, ok := registry.AdapterForLanguage("shellscript")
	if !ok {
		t.Fatal("missing shellscript adapter")
	}
	return adapter
}

func mustSQLAdapter(t *testing.T, registry *LanguageAdapterRegistry) LanguageAdapter {
	t.Helper()
	adapter, ok := registry.AdapterForLanguage("sql")
	if !ok {
		t.Fatal("missing sql adapter")
	}
	return adapter
}

func assertShellCommand(t *testing.T, adapter LanguageAdapter) {
	t.Helper()
	command, err := adapter.ServerCommand(context.Background(), ResolvedLanguageScope{})
	if err != nil {
		t.Fatalf("shell ServerCommand() error = %v", err)
	}
	if command.Executable != "bash-language-server" || !reflect.DeepEqual(command.Args, []string{"start"}) {
		t.Fatalf("shell ServerCommand() = %#v, want bash-language-server start", command)
	}
}

func assertSQLCommand(t *testing.T, adapter LanguageAdapter) {
	t.Helper()
	command, err := adapter.ServerCommand(context.Background(), ResolvedLanguageScope{})
	if err != nil {
		t.Fatalf("sql ServerCommand() error = %v", err)
	}
	if command.Executable != "sqruff" || !reflect.DeepEqual(command.Args, []string{"lsp"}) {
		t.Fatalf("sql ServerCommand() = %#v, want sqruff lsp", command)
	}
}

func assertShellResolvedScope(t *testing.T, resolved ResolvedLanguageScope, root string) {
	t.Helper()
	if resolved.WorkspaceRoot != root || resolved.RootKind != "shell_project" {
		t.Fatalf("shell resolved scope = %#v, want shell project at %q", resolved, root)
	}
}

func assertSQLResolvedScope(t *testing.T, resolved ResolvedLanguageScope, root string) {
	t.Helper()
	if resolved.WorkspaceRoot != root || resolved.RootKind != "sqlite_sql_project" {
		t.Fatalf("sql resolved scope = %#v, want SQLite SQL project at %q", resolved, root)
	}
}

func assertShellBootstrapPolicy(t *testing.T, policy BootstrapPolicy) {
	t.Helper()
	if !reflect.DeepEqual(policy.FirstSourceExtensions, []string{".sh", ".bash", ".zsh", ".ksh", ".bats"}) {
		t.Fatalf("shell FirstSourceExtensions = %#v", policy.FirstSourceExtensions)
	}
	if _, ok := policy.IgnoredDirNames["ignored-shell"]; !ok {
		t.Fatalf("shell ignored dirs = %#v, missing configured ignored-shell", policy.IgnoredDirNames)
	}
	if !policy.OpenTarget {
		t.Fatalf("shell BootstrapPolicy.OpenTarget = false, want true")
	}
	if !policy.TreatMissingDiagnosticsAsEmpty {
		t.Fatalf("shell BootstrapPolicy.TreatMissingDiagnosticsAsEmpty = false, want true")
	}
}

func assertSQLBootstrapPolicy(t *testing.T, policy BootstrapPolicy) {
	t.Helper()
	if !reflect.DeepEqual(policy.FirstSourceExtensions, []string{".sql"}) {
		t.Fatalf("sql FirstSourceExtensions = %#v", policy.FirstSourceExtensions)
	}
	if _, ok := policy.IgnoredDirNames["ignored-sql"]; !ok {
		t.Fatalf("sql ignored dirs = %#v, missing configured ignored-sql", policy.IgnoredDirNames)
	}
	if !policy.OpenTarget {
		t.Fatalf("sql BootstrapPolicy.OpenTarget = false, want true")
	}
	if !policy.TreatMissingDiagnosticsAsEmpty {
		t.Fatalf("sql BootstrapPolicy.TreatMissingDiagnosticsAsEmpty = false, want true")
	}
}

func TestLanguageAdapterRegistryFromConfigRegistersProtoBufAdapter(t *testing.T) {
	root, target := writeProtoAdapterTestFixture(t)
	adapter := requireProtoAdapter(t)
	assertProtoAdapterServerCommand(t, adapter)
	resolved := resolveProtoAdapterScope(t, adapter, root, target)
	assertProtoAdapterScope(t, adapter, resolved, root)
}

func writeProtoAdapterTestFixture(t *testing.T) (string, string) {
	t.Helper()
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "api", "v1", "message.proto")
	writeGenericTestFile(t, filepath.Join(root, "buf.yaml"), "version: v2\n")
	writeGenericTestFile(t, target, "syntax = \"proto3\";\nmessage Message {}\n")
	return root, target
}

func requireProtoAdapter(t *testing.T) LanguageAdapter {
	t.Helper()
	registry := NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage("proto")
	if !ok {
		t.Fatal("missing proto adapter")
	}
	return adapter
}

func assertProtoAdapterServerCommand(t *testing.T, adapter LanguageAdapter) {
	t.Helper()
	command, err := adapter.ServerCommand(context.Background(), ResolvedLanguageScope{})
	if err != nil {
		t.Fatalf("proto ServerCommand: %v", err)
	}
	if command.Executable != "buf" || !reflect.DeepEqual(command.Args, []string{"lsp", "serve"}) {
		t.Fatalf("proto ServerCommand() = %#v, want buf lsp serve", command)
	}
}

func resolveProtoAdapterScope(t *testing.T, adapter LanguageAdapter, root, target string) ResolvedLanguageScope {
	t.Helper()
	resolved, err := adapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "proto",
		TargetPath: target,
	}, target)
	if err != nil {
		t.Fatalf("proto ResolveRoot: %v", err)
	}
	return resolved
}

func assertProtoAdapterScope(t *testing.T, adapter LanguageAdapter, resolved ResolvedLanguageScope, root string) {
	t.Helper()
	if resolved.WorkspaceRoot != root || resolved.LanguageID != "proto" || resolved.RootKind != "proto_project" {
		t.Fatalf("proto resolved scope = %#v, want proto project at %q", resolved, root)
	}
	policy := adapter.BootstrapPolicy(resolved)
	if !policy.OpenTarget || !policy.TreatMissingDiagnosticsAsEmpty || !reflect.DeepEqual(policy.FirstSourceExtensions, []string{".proto"}) {
		t.Fatalf("proto BootstrapPolicy() = %#v, want open target and .proto source", policy)
	}
}

func TestPythonAdapterRequiresExplicitDiagnosticsPublish(t *testing.T) {
	registry := NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage("python")
	if !ok {
		t.Fatal("missing python adapter")
	}
	policy := adapter.BootstrapPolicy(ResolvedLanguageScope{LanguageID: "python"})
	if policy.TreatMissingDiagnosticsAsEmpty {
		t.Fatal("python TreatMissingDiagnosticsAsEmpty = true, want delayed publish to remain pending")
	}
}
