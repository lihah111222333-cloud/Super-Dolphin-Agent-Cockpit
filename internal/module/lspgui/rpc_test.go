package lspgui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	rpcpkg "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestNewLSPGUIHandlersRegistersExpectedRoutes(t *testing.T) {
	t.Parallel()

	handlers := NewLSPGUIHandlers(NewService(&config.Config{ProjectRoot: t.TempDir()})).Handlers
	for _, method := range []string{
		"lsp/gui_file",
		"lsp/gui_grep",
		"lsp/gui_structure",
		"lsp/gui_inspect",
		"lsp/gui_xref",
	} {
		if _, ok := handlers[method]; !ok {
			t.Fatalf("Handlers missing %q", method)
		}
	}
}

func TestLSPGUIFileAndSearchDispatch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filePath := filepath.Join(root, "demo.txt")
	if err := os.WriteFile(filePath, []byte("alpha\nneedle beta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewLSPGUIHandlers(NewService(&config.Config{ProjectRoot: root})).Handlers)

	readRaw, err := server.Dispatch(context.Background(), "lsp/gui_file", json.RawMessage(`{"action":"read_file","file_path":"demo.txt","offset":0,"limit":20}`))
	if err != nil {
		t.Fatalf("Dispatch(read_file) error = %v", err)
	}
	var read fileReadResult
	if err := json.Unmarshal(readRaw, &read); err != nil {
		t.Fatalf("Unmarshal(read_file) error = %v", err)
	}
	wantPath, err := canonicalPath(filePath)
	if err != nil {
		t.Fatalf("canonicalPath() error = %v", err)
	}
	if read.FilePath != filepath.ToSlash(wantPath) && read.FilePath != wantPath {
		t.Fatalf("read.FilePath = %q, want %q", read.FilePath, wantPath)
	}
	if read.TotalLines < 2 || read.Content == "" {
		t.Fatalf("read result = %#v", read)
	}

	searchRaw, err := server.Dispatch(context.Background(), "lsp/gui_grep", json.RawMessage(`{"action":"text_search","query":"needle","path":"","max_results":10}`))
	if err != nil {
		t.Fatalf("Dispatch(text_search) error = %v", err)
	}
	var search searchResult
	if err := json.Unmarshal(searchRaw, &search); err != nil {
		t.Fatalf("Unmarshal(text_search) error = %v", err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("len(search.Results) = %d, want 1 (%#v)", len(search.Results), search.Results)
	}
	if search.Results[0].Line != 2 || search.Results[0].Col != 1 {
		t.Fatalf("search result = %#v", search.Results[0])
	}
}
