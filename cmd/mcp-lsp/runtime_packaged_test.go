package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

func TestNewManagerPackagedPrefersBundledServersAndKeepsAutoInstallLanguages(t *testing.T) {
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
	if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		t.Fatalf("python must remain registered for auto-install, got unsupported: %v", err)
	}
	if err == nil {
		t.Fatal("python unexpectedly resolved with an empty PATH; want installer readiness error")
	}
}

// TestNewManagerPackagedRegistersUnbundledLanguagesForAutoInstall 证明 bundle 只提供首选二进制，
// 不能成为通用 LSP 的语言白名单；未捆绑语言必须保留注册并进入按需安装检查。
func TestNewManagerPackagedRegistersUnbundledLanguagesForAutoInstall(t *testing.T) {
	declareTestDependencyBootstrap(t)
	root := t.TempDir()
	bundle := t.TempDir()
	writeMcpLSPBundleManifest(t, bundle, `{
  "servers": {
    "gopls": {"path": "bin/gopls", "languages": ["go", "gomod", "gosum", "gowork"]}
  }
}
`)
	writeMcpLSPExecutable(t, filepath.Join(bundle, "bin"), "gopls")
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

	ctx := installer.WithToolCallInstallCheckOnly(
		common.WithToolScope(context.Background(), common.ToolScope{CWD: root, Family: "lsp"}),
	)
	for _, languageID := range []string{"typescript", "json", "mql"} {
		_, err := mgr.registry.GetManagerForLanguage(ctx, languageID)
		if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
			t.Fatalf("%s must remain registered for auto-install, got unsupported: %v", languageID, err)
		}
		var missing *installer.MissingBinaryError
		if !errors.As(err, &missing) {
			t.Fatalf("%s error = %T %v, want installer.MissingBinaryError", languageID, err, err)
		}
		gotLanguage, gotBinary := missing.MissingLSPBinary()
		if gotLanguage != languageID || gotBinary == "" {
			t.Fatalf("missing binary = (%q, %q), want language %q and non-empty binary", gotLanguage, gotBinary, languageID)
		}
	}
}

func TestNewManagerPackagedRegistersMQLAliasesWithBundledClangd(t *testing.T) {
	declareTestDependencyBootstrap(t)
	root := t.TempDir()
	bundle := t.TempDir()
	writeMcpLSPBundleManifest(t, bundle, `{
  "servers": {
    "clangd": {"path": "bin/clangd", "languages": ["c", "cpp", "objective-c", "objective-cpp", "mql", "mql4", "mql5", "mq4", "mq5", "mqh"]}
  }
}
`)
	writeMcpLSPExecutable(t, filepath.Join(bundle, "bin"), "clangd")
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
	for _, languageID := range []string{"c", "cpp", "objective-c", "objective-cpp", "mql", "mql4", "mql5", "mq4", "mq5", "mqh"} {
		if _, err := mgr.registry.GetManagerForLanguage(ctx, languageID); err != nil {
			t.Fatalf("bundled %s manager error = %v", languageID, err)
		}
	}
}

// TestNewManagerPackagedBundleSelectsAdapterWithoutPruningAliases 证明 manifest 命中 adapter 后只选择首选二进制，
// 不得把 manifest 的 languages 子集误作该 adapter 的语言别名白名单。
func TestNewManagerPackagedBundleSelectsAdapterWithoutPruningAliases(t *testing.T) {
	declareTestDependencyBootstrap(t)
	root := t.TempDir()
	bundle := t.TempDir()
	writeMcpLSPBundleManifest(t, bundle, `{
  "servers": {
    "gopls": {"path": "bin/gopls", "languages": ["go"]}
  }
}
`)
	writeMcpLSPExecutable(t, filepath.Join(bundle, "bin"), "gopls")
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
	for _, languageID := range []string{"go", "gomod", "gosum", "gowork"} {
		if _, err := mgr.registry.GetManagerForLanguage(ctx, languageID); err != nil {
			t.Fatalf("bundled gopls adapter alias %s error = %v", languageID, err)
		}
	}
}

func TestNewManagerPackagedStandardBundleRegistersNonJDTLSLanguages(t *testing.T) {
	declareTestDependencyBootstrap(t)
	root := t.TempDir()
	bundle := t.TempDir()
	writeMcpLSPBundleManifest(t, bundle, `{
  "servers": {
    "gopls": {"path": "bin/gopls", "languages": ["go", "gomod", "gosum", "gowork"]},
    "clangd": {"path": "bin/clangd", "languages": ["c", "cpp", "objective-c", "objective-cpp", "mql", "mql4", "mql5", "mq4", "mq5", "mqh"]},
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
		"clangd",
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
		"go", "gomod", "gosum", "gowork",
		"c", "cpp", "objective-c", "objective-cpp",
		"mql", "mql4", "mql5", "mq4", "mq5", "mqh",
		"javascript", "javascriptreact", "typescript", "typescriptreact",
		"css", "python", "rust", "shellscript", "sql",
	} {
		if _, err := mgr.registry.GetManagerForLanguage(ctx, languageID); err != nil {
			t.Fatalf("bundled %s manager error = %v", languageID, err)
		}
	}
	_, err = mgr.registry.GetManagerForLanguage(ctx, "java")
	if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		t.Fatalf("java must remain registered for auto-install, got unsupported: %v", err)
	}
	if err == nil {
		t.Fatal("java unexpectedly resolved with an empty PATH; want installer readiness error")
	}
}
