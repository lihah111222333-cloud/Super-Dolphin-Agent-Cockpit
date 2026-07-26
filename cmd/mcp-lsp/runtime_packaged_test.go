package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

func TestNewManagerPackagedRegistersOnlyBundledLanguageServers(t *testing.T) {
	declareTestDependencyBootstrap(t)
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
	declareTestDependencyBootstrap(t)
	root := t.TempDir()
	bundle := t.TempDir()
	writeMcpLSPBundleManifest(t, bundle, `{
  "servers": {
    "gopls": {"path": "bin/gopls", "languages": ["go", "gomod", "gosum", "gowork"]},
    "typescript-language-server": {"path": "bin/typescript-language-server", "languages": ["javascript", "javascriptreact", "typescript", "typescriptreact"]},
    "vscode-langservers-extracted": {"path": "bin/vscode-css-language-server", "languages": ["css"]},
    "pyright": {"path": "bin/pyright-langserver", "languages": ["python"]},
    "rust-analyzer": {"path": "bin/rust-analyzer", "languages": ["rust"]},
    "bash-language-server": {"path": "bin/bash-language-server", "languages": ["shellscript"]},
    "sqruff": {"path": "bin/sqruff", "languages": ["sql"]}
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
		"sqruff",
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
		"sql",
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
