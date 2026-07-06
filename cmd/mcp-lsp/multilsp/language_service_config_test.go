package multilsp

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
	if command.Executable != "sql-language-server" || !reflect.DeepEqual(command.Args, []string{"up", "--method", "stdio"}) {
		t.Fatalf("sql ServerCommand() = %#v, want sql-language-server up --method stdio", command)
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
	if resolved.WorkspaceRoot != root || resolved.RootKind != "sql_project" {
		t.Fatalf("sql resolved scope = %#v, want sql project at %q", resolved, root)
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
