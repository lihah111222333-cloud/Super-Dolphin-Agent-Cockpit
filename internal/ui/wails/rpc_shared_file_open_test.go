package wails

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	rpcpkg "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestNewRPCHandlersRegistersSharedFileOpenRoute(t *testing.T) {
	t.Parallel()

	handlers := NewRPCHandlers(&App{}, nil, nil).Handlers
	if _, ok := handlers["ui/sharedFile/open"]; !ok {
		t.Fatal("handler ui/sharedFile/open is not registered")
	}
}

func TestOpenSharedFileRejectsTraversal(t *testing.T) {
	t.Parallel()

	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewRPCHandlers(&App{}, &config.Config{ProjectRoot: t.TempDir()}, nil).Handlers)

	_, err := server.Dispatch(context.Background(), "ui/sharedFile/open", json.RawMessage(`{"path":"../secret.mp4"}`))
	if err == nil {
		t.Fatal("Dispatch(ui/sharedFile/open) error = nil, want traversal rejection")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("Dispatch(ui/sharedFile/open) error = %v, want traversal rejection", err)
	}
}

func TestResolveSharedFileOpenPathRejectsMissingRootAndPath(t *testing.T) {
	t.Parallel()

	if _, err := resolveSharedFileOpenPath("", "dag/video/final.mp4"); err == nil {
		t.Fatal("resolveSharedFileOpenPath(empty root) error = nil, want error")
	}
	if _, err := resolveSharedFileOpenPath(t.TempDir(), " "); err == nil {
		t.Fatal("resolveSharedFileOpenPath(empty path) error = nil, want error")
	}
}

func TestResolveSharedFileOpenPathRequiresExistingRegularFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sharedDir := filepath.Join(root, ".agnet", "shared", "dag", "video")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sharedDir, "final.mp4")
	if err := os.WriteFile(file, []byte("mp4-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveSharedFileOpenPath(root, "dag/video/final.mp4")
	if err != nil {
		t.Fatalf("resolveSharedFileOpenPath() error = %v", err)
	}
	if got != file {
		t.Fatalf("resolveSharedFileOpenPath() = %q, want %q", got, file)
	}

	for _, tt := range []struct {
		name string
		path string
	}{
		{name: "missing file", path: "dag/video/missing.mp4"},
		{name: "directory", path: "dag/video"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := resolveSharedFileOpenPath(root, tt.path); err == nil {
				t.Fatalf("resolveSharedFileOpenPath(%q) error = nil, want regular-file rejection", tt.path)
			}
		})
	}

	link := filepath.Join(sharedDir, "link.mp4")
	if err := os.Symlink(file, link); err != nil {
		t.Logf("skip symlink assertion: %v", err)
		return
	}
	if _, err := resolveSharedFileOpenPath(root, "dag/video/link.mp4"); err == nil {
		t.Fatal("resolveSharedFileOpenPath(symlink) error = nil, want regular-file rejection")
	}
}

func TestResolveSharedFileOpenPathRejectsParentSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	outsideVideoDir := filepath.Join(outside, "video")
	if err := os.MkdirAll(outsideVideoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideVideoDir, "final.mp4"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	sharedRoot := filepath.Join(root, ".agnet", "shared")
	if err := os.MkdirAll(sharedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sharedRoot, "dag")); err != nil {
		t.Logf("skip parent symlink assertion: %v", err)
		return
	}

	if _, err := resolveSharedFileOpenPath(root, "dag/video/final.mp4"); err == nil {
		t.Fatal("resolveSharedFileOpenPath(parent symlink escape) error = nil, want rejection")
	}
}
