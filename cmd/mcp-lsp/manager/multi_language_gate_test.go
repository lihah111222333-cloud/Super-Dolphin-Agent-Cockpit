package manager

import (
	"context"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"path/filepath"
	"testing"
)

func TestMultiLanguageLSPGateCoversRegisteredLanguages(t *testing.T) {
	registry := NewRegistry(nil)
	registered := []string{"go", "gomod", "gosum", "gowork", "javascript", "typescript", "python", "css", "rust", "java"}
	managers := make(map[string]*registryDiagnosticsManager, len(registered))
	for _, lang := range registered {
		mgr := &registryDiagnosticsManager{}
		registry.Register(lang, mgr)
		managers[lang] = mgr
	}

	for _, lang := range registered {
		got, err := registry.GetManagerForLanguage(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), lang)
		if err != nil {
			t.Fatalf("GetManagerForLanguage(%q): %v", lang, err)
		}
		if got != managers[lang] {
			t.Fatalf("GetManagerForLanguage(%q) = %p, want registered fake manager %p", lang, got, managers[lang])
		}
	}

	fileCases := map[string]string{
		"main.go":      "go",
		"go.mod":       "gomod",
		"go.sum":       "gosum",
		"go.work":      "gowork",
		"app.js":       "javascript",
		"app.ts":       "typescript",
		"service.py":   "python",
		"styles.css":   "css",
		"lib.rs":       "rust",
		"Example.java": "java",
	}
	for name, wantLang := range fileCases {
		got, err := registry.GetManagerForFile(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), filepath.Join(t.TempDir(), name))
		if err != nil {
			t.Fatalf("GetManagerForFile(%q): %v", name, err)
		}
		if got != managers[wantLang] {
			t.Fatalf("GetManagerForFile(%q) = %p, want %s fake manager %p", name, got, wantLang, managers[wantLang])
		}
	}
}
