package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestFileOpenProductionHandlerUsesResolvedScopeManager(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	manager := &structureTestManager{}
	resolved := lspmanager.ResolvedToolScope{
		ToolScope: lspmanager.ToolScope{
			AgentID:    "agent-trusted",
			ThreadID:   "thread-trusted",
			CWD:        root,
			Family:     "lsp",
			LanguageID: "go",
			TargetPath: target,
		},
		ScopeKey:     "lsp\x00agent-trusted\x00thread-trusted",
		WorkspaceKey: "workspace-trusted",
		ManagerKey:   "manager-trusted",
	}
	registry := &structureTestRegistry{
		fileManager: lspmanager.ManagerWithResolvedScope(manager, resolved),
	}
	handler := NewFileHandler(Config{WorkspaceRoot: "/untrusted/root", Registry: registry})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  "agent-trusted",
		ThreadID: "thread-trusted",
		CWD:      root,
		Family:   "lsp",
	})
	input, err := json.Marshal(map[string]any{
		"action":    "open_file",
		"file_path": "main.go",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(ctx, input); err != nil {
		t.Fatalf("open_file: %v", err)
	}
	got, ok := lspmanager.ResolvedToolScopeFromContext(manager.didOpenContext)
	if !ok {
		t.Fatalf("DidOpen context missing resolved tool scope")
	}
	if got.ManagerKey != "manager-trusted" || got.AgentID != "agent-trusted" || got.CWD != root {
		t.Fatalf("resolved scope = %#v, want trusted manager scope", got)
	}
}

func TestReadBatchReturnsPartialFailureWhenAnyItemFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})

	resp, err := handlerBase{}.readBatch(ctx, readFileRequest{rawPaths: []string{"ok.txt", "missing.txt"}})
	if err == nil {
		t.Fatalf("readBatch() err = nil, response = %#v; want partial failure error", resp)
	}
	if !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("readBatch() error = %v, want missing path", err)
	}
	if !strings.Contains(fmt.Sprint(resp), "ok") {
		t.Fatalf("readBatch() response = %#v, want successful item preserved", resp)
	}
}
