package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestNewManagerRegistersDocumentFallbackAdapters(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GO_AGENT_LSP_ROOT", root)

	mgr, err := newManager()
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
