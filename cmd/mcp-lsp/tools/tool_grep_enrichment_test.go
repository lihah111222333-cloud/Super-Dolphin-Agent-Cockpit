package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type grepFuncRangeRegistry struct {
	diagnosticsTestRegistry
	getManagerCalls int
}

func (r *grepFuncRangeRegistry) GetManagerForFile(context.Context, string) (lspmanager.Manager, error) {
	r.getManagerCalls++
	return nil, lspmanager.ErrUnsupportedLanguage
}

func TestGrepTextSearchDoesNotResolveLSPFuncRanges(t *testing.T) {
	root := t.TempDir()
	writeGrepFixtureFile(t, filepath.Join(root, "sample.go"), "package main\nfunc main() {\n\tconst needle = true\n}\n")

	registry := &grepFuncRangeRegistry{}
	handler := NewGrepHandler(Config{WorkspaceRoot: root, Registry: registry})
	payload, err := json.Marshal(grepToolInput{
		Action: "text_search",
		Query:  "needle",
		Path:   root,
		Glob:   "*.go",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), payload)
	if err != nil {
		t.Fatalf("grep returned error: %v", err)
	}
	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep result type = %T, want grepResponse", got)
	}
	if resp.Total != 1 {
		t.Fatalf("grep total = %d, want 1", resp.Total)
	}
	if registry.getManagerCalls != 0 {
		t.Fatalf("GetManagerForFile calls = %d, want 0; text_search must not start LSP func-range enrichment", registry.getManagerCalls)
	}
}
