package wails

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	rpcpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
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

func TestPreviewSharedFileReturnsTokenizedMediaURL(t *testing.T) {
	root := t.TempDir()
	finalPath := "dag/video/final.mp4"
	writeSharedFile(t, root, finalPath, validMP4Bytes())

	registry := newSharedFilePreviewRegistry(time.Now)
	previewAddr := &sharedFilePreviewHTTPAddr{}
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewRPCHandlers(&App{sharedFilePreviewRegistry: registry, sharedFilePreviewHTTPAddr: previewAddr}, &config.Config{ProjectRoot: root}, nil).Handlers)

	raw, err := server.Dispatch(context.Background(), "ui/sharedFile/open", json.RawMessage(`{"path":"dag/video/final.mp4","preview":true}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/sharedFile/open preview) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("preview result = %s, want object: %v", raw, err)
	}
	previewURL, _ := payload["url"].(string)
	parsedPreviewURL, err := url.Parse(previewURL)
	if err != nil {
		t.Fatalf("preview url = %q, parse error: %v", previewURL, err)
	}
	if parsedPreviewURL.Path != sharedFilePreviewPathPrefix || parsedPreviewURL.Query().Get("id") == "" {
		t.Fatalf("preview url = %q, want tokenized shared-file preview URL", previewURL)
	}
	if strings.Contains(previewURL, finalPath) || strings.Contains(previewURL, root) {
		t.Fatalf("preview url = %q must not leak raw path", previewURL)
	}
	if got, _ := payload["path"].(string); got != finalPath {
		t.Fatalf("path = %q, want %q", got, finalPath)
	}
	if got, _ := payload["contentType"].(string); got != "video/mp4" {
		t.Fatalf("contentType = %q, want video/mp4", got)
	}
}

func TestSharedFilePreviewHTTPServesRegisteredToken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	finalPath := "dag/video/final.mp4"
	want := validMP4Bytes()
	writeSharedFile(t, root, finalPath, want)
	registry := newSharedFilePreviewRegistry(time.Now)
	previewURL, _, err := registry.register(root, finalPath, "127.0.0.1:4511")
	if err != nil {
		t.Fatalf("register shared preview: %v", err)
	}

	srv := httptest.NewServer(withSharedFilePreviewAssetsRegistry(http.NotFoundHandler(), registry))
	defer srv.Close()

	parsedPreviewURL, err := url.Parse(previewURL)
	if err != nil {
		t.Fatalf("parse preview URL: %v", err)
	}
	res, err := http.Get(srv.URL + parsedPreviewURL.RequestURI())
	if err != nil {
		t.Fatalf("GET preview token: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type = %q, want video/mp4", got)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestSharedFilePreviewRejectsRawPathAndUnsafeInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSharedFile(t, root, "dag/video/final.mp4", validMP4Bytes())
	registry := newSharedFilePreviewRegistry(time.Now)

	badPaths := []string{
		"/etc/passwd",
		"../secret.mp4",
		`C:\Users\me\secret.mp4`,
	}
	for _, path := range badPaths {
		if _, _, err := registry.register(root, path, "127.0.0.1:4511"); err == nil {
			t.Fatalf("register(%q) error = nil, want rejection", path)
		}
	}

	assertSharedPreviewRejectsSymlink(t, registry, root)
	assertSharedPreviewRejectsOversized(t, registry, root)
	writeSharedFile(t, root, "dag/video/fake.mp4", []byte("not a video"))
	if _, _, err := registry.register(root, "dag/video/fake.mp4", "127.0.0.1:4511"); err == nil {
		t.Fatal("register MIME mismatch error = nil, want rejection")
	}

	srv := httptest.NewServer(withSharedFilePreviewAssetsRegistry(http.NotFoundHandler(), registry))
	defer srv.Close()
	res, err := http.Get(srv.URL + sharedFilePreviewPathPrefix + "?path=" + url.QueryEscape("dag/video/final.mp4"))
	if err != nil {
		t.Fatalf("GET raw path preview: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("raw path status = %d, want 404", res.StatusCode)
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

func assertSharedPreviewRejectsSymlink(t *testing.T, registry *sharedFilePreviewRegistry, root string) {
	t.Helper()
	sharedDir := filepath.Join(root, ".agnet", "shared", "dag", "video")
	link := filepath.Join(sharedDir, "link.mp4")
	if err := os.Symlink(filepath.Join(sharedDir, "final.mp4"), link); err != nil {
		t.Logf("skip symlink assertion: %v", err)
		return
	}
	if _, _, err := registry.register(root, "dag/video/link.mp4", "127.0.0.1:4511"); err == nil {
		t.Fatal("register symlink error = nil, want rejection")
	}
}

func assertSharedPreviewRejectsOversized(t *testing.T, registry *sharedFilePreviewRegistry, root string) {
	t.Helper()
	oversized := filepath.Join(root, ".agnet", "shared", "dag", "video", "large.mp4")
	if err := os.WriteFile(oversized, validMP4Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(oversized, sharedFilePreviewMaxBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.register(root, "dag/video/large.mp4", "127.0.0.1:4511"); err == nil {
		t.Fatal("register oversized file error = nil, want rejection")
	}
}

func writeSharedFile(t *testing.T, root, sharedPath string, content []byte) {
	t.Helper()
	target := filepath.Join(root, ".agnet", "shared", filepath.FromSlash(sharedPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func validMP4Bytes() []byte {
	return []byte{
		0x00, 0x00, 0x00, 0x18,
		'f', 't', 'y', 'p',
		'm', 'p', '4', '2',
		0x00, 0x00, 0x00, 0x00,
		'm', 'p', '4', '2',
		'i', 's', 'o', 'm',
	}
}
