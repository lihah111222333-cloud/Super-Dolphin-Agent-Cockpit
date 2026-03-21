package lspgui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	rpcpkg "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestReadFileRejectsPathOutsideProjectRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server := newLSPGUITestServer(root)
	_, err := server.Dispatch(
		context.Background(),
		"lsp/gui_file",
		json.RawMessage(`{"action":"read_file","file_path":`+strconv.Quote(outside)+`}`),
	)
	if err == nil {
		t.Fatal("Dispatch(read_file outside) error = nil, want sandbox rejection")
	}
}

func TestReadFileRejectsLargeFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	large := filepath.Join(root, "large.txt")
	if err := os.WriteFile(large, make([]byte, maxReadFileBytes+1), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server := newLSPGUITestServer(root)
	_, err := server.Dispatch(
		context.Background(),
		"lsp/gui_file",
		json.RawMessage(`{"action":"read_file","file_path":"large.txt"}`),
	)
	if err == nil {
		t.Fatal("Dispatch(read_file large) error = nil, want size rejection")
	}
}

func TestDiagnosticsRejectsPathOutsideProjectRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server := newLSPGUITestServer(root)
	_, err := server.Dispatch(
		context.Background(),
		"lsp/gui_file",
		json.RawMessage(`{"action":"diagnostics","file_path":`+strconv.Quote(outside)+`}`),
	)
	if err == nil {
		t.Fatal("Dispatch(diagnostics outside) error = nil, want sandbox rejection")
	}
}

func TestHandleGrepHonorsContextAndSkipsIgnoredInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeLSPGUITestFile(t, filepath.Join(root, "hit.txt"), "needle\n")
	writeLSPGUITestFile(t, filepath.Join(root, "node_modules", "skip.txt"), "needle\n")
	writeLSPGUITestFile(t, filepath.Join(root, "large.txt"), string(append(make([]byte, maxSearchFileBytes+16), []byte("needle")...)))
	svc := NewService(&config.Config{ProjectRoot: root})

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.HandleGrep(cancelled, grepParams{Action: "text_search", Query: "needle"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("HandleGrep(cancelled) error = %v, want context.Canceled", err)
	}

	got, err := svc.HandleGrep(context.Background(), grepParams{Action: "text_search", Query: "needle", MaxResults: 10})
	if err != nil {
		t.Fatalf("HandleGrep() error = %v", err)
	}
	result, ok := got.(searchResult)
	if !ok {
		t.Fatalf("HandleGrep() type = %T, want searchResult", got)
	}
	if len(result.Results) != 1 || filepath.Base(result.Results[0].File) != "hit.txt" {
		t.Fatalf("search results = %#v", result.Results)
	}
}

func TestHandleGrepCapsResultsAtFifty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for i := range 60 {
		writeLSPGUITestFile(t, filepath.Join(root, "caps", strconv.Itoa(i)+".txt"), "cap-hit\n")
	}
	svc := NewService(&config.Config{ProjectRoot: root})
	got, err := svc.HandleGrep(context.Background(), grepParams{
		Action:     "text_search",
		Query:      "cap-hit",
		MaxResults: 1000,
	})
	if err != nil {
		t.Fatalf("HandleGrep() error = %v", err)
	}
	result := got.(searchResult)
	if len(result.Results) != maxSearchResults {
		t.Fatalf("len(search results) = %d, want %d", len(result.Results), maxSearchResults)
	}
}

func TestHandleGrepAstSearchReturnsStubResult(t *testing.T) {
	t.Parallel()

	svc := NewService(&config.Config{ProjectRoot: t.TempDir()})
	got, err := svc.HandleGrep(context.Background(), grepParams{Action: "ast_search", Symbol: "func main"})
	if err != nil {
		t.Fatalf("HandleGrep(ast_search) error = %v", err)
	}
	result, ok := got.(searchResult)
	if !ok {
		t.Fatalf("HandleGrep(ast_search) type = %T, want searchResult", got)
	}
	if !result.Stub || result.Status != stubStatusNotImplemented {
		t.Fatalf("ast_search result = %#v", result)
	}
}

func TestHandleGrepMatchesRelativeGlobPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeLSPGUITestFile(t, filepath.Join(root, "src", "demo.go"), "needle\n")
	writeLSPGUITestFile(t, filepath.Join(root, "pkg", "demo.go"), "needle\n")
	svc := NewService(&config.Config{ProjectRoot: root})
	got, err := svc.HandleGrep(context.Background(), grepParams{
		Action: "text_search",
		Query:  "needle",
		Glob:   "src/*.go",
	})
	if err != nil {
		t.Fatalf("HandleGrep(glob) error = %v", err)
	}
	result := got.(searchResult)
	if len(result.Results) != 1 {
		t.Fatalf("len(glob results) = %d, want 1 (%#v)", len(result.Results), result.Results)
	}
	if filepath.Base(filepath.Dir(result.Results[0].File)) != "src" {
		t.Fatalf("glob result = %#v, want src/*.go match", result.Results[0])
	}
}

func TestStubResponsesExposeNotImplementedStatus(t *testing.T) {
	t.Parallel()

	svc := NewService(&config.Config{ProjectRoot: t.TempDir()})
	got, err := svc.HandleStructure(context.Background(), structureParams{Action: "document_symbol"})
	if err != nil {
		t.Fatalf("HandleStructure() error = %v", err)
	}
	result, ok := got.(symbolsResult)
	if !ok {
		t.Fatalf("HandleStructure() type = %T, want symbolsResult", got)
	}
	if !result.Stub || result.Status != stubStatusNotImplemented {
		t.Fatalf("stub result = %#v", result)
	}
}

func newLSPGUITestServer(root string) *rpcpkg.Server {
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewLSPGUIHandlers(NewService(&config.Config{ProjectRoot: root})).Handlers)
	return server
}

func writeLSPGUITestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
