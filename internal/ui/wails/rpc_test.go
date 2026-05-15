package wails

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	rpcpkg "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestNewRPCHandlersRegistersNativeDialogRoutes(t *testing.T) {
	t.Parallel()

	handlers := NewRPCHandlers(&App{}, nil, nil).Handlers
	for _, method := range []string{
		"ui/selectProjectDir",
		"ui/selectProjectDirs",
		"ui/selectFiles",
		"ui/buildInfo",
		"ui/saveClipboardImage",
		"ui/log",
		"ui/windowBootstrap/get",
		"ui/openNewWindow",
	} {
		if _, ok := handlers[method]; !ok {
			t.Fatalf("handler %q is not registered", method)
		}
	}
}

func TestUILogRouteAcceptsClientMetaAndCountsEntries(t *testing.T) {
	t.Parallel()

	server := newWailsRPCServer(t, &App{})
	raw, err := server.Dispatch(context.Background(), "ui/log", json.RawMessage(`{
		"entries":[
			{"level":"warn","scope":"thread","event":"opened","seq":1},
			{"level":"error","scope":"ui","event":"hydrate_failed","seq":2}
		],
		"_aoClientKind":"desktop-wails",
		"_aoClientRoute":"/chat"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/log) error = %v", err)
	}

	var result struct {
		OK       bool `json:"ok"`
		Ingested int  `json:"ingested"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/log) error = %v", err)
	}
	if !result.OK || result.Ingested != 2 {
		t.Fatalf("ui/log result = %#v, want ok=true ingested=2", result)
	}
}

func TestSaveClipboardImageRouteReturnsPath(t *testing.T) {
	t.Parallel()

	server := newWailsRPCServer(t, &App{})
	raw, err := server.Dispatch(context.Background(), "ui/saveClipboardImage", json.RawMessage(`{
		"base64Payload":"aGVsbG8=",
		"_aoClientKind":"web-debug-shim",
		"_aoClientRoute":"/chat"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/saveClipboardImage) error = %v", err)
	}

	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/saveClipboardImage) error = %v", err)
	}
	if result.Path == "" {
		t.Fatal("ui/saveClipboardImage path is empty")
	}
	defer os.Remove(result.Path)
	if got, err := os.ReadFile(result.Path); err != nil || string(got) != "hello" {
		t.Fatalf("saved clipboard image = %q, %v; want hello, nil", string(got), err)
	}
}

func TestWindowBootstrapRouteConsumesSnapshotOnce(t *testing.T) {
	t.Parallel()

	server := newWailsRPCServer(t, &App{
		windowBootstrap: map[string]any{"page": "chat"},
	})

	first := dispatchBootstrapGet(t, server)
	if first.Snapshot["page"] != "chat" {
		t.Fatalf("first snapshot = %#v, want page=chat", first.Snapshot)
	}

	second := dispatchBootstrapGet(t, server)
	if second.Snapshot != nil {
		t.Fatalf("second snapshot = %#v, want nil", second.Snapshot)
	}
}

func TestOpenNewWindowRouteDefaultsGroupAndEncodesSnapshot(t *testing.T) {
	t.Parallel()

	var capturedGroup string
	var capturedN int
	var capturedBootstrap string
	var capturedCWD string
	server := newWailsRPCServer(t, &App{
		group: "team-alpha",
		openNewWindowInvoker: func(group string, n int, uiBootstrap, cwd string) (string, error) {
			capturedGroup = group
			capturedN = n
			capturedBootstrap = uiBootstrap
			capturedCWD = cwd
			return "window-7", nil
		},
	})

	raw, err := server.Dispatch(context.Background(), "ui/openNewWindow", json.RawMessage(`{
		"cwd":"/tmp/project",
		"snapshot":{"page":"chat"},
		"_aoClientKind":"desktop-wails",
		"_aoClientRoute":"/chat"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/openNewWindow) error = %v", err)
	}

	var result struct {
		OK       bool   `json:"ok"`
		WindowID string `json:"windowId"`
		CWD      string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/openNewWindow) error = %v", err)
	}
	assertOpenNewWindowResult(t, result.OK, result.WindowID, result.CWD)
	assertOpenNewWindowParams(t, capturedGroup, capturedN, capturedCWD)
	snapshot, err := decodeWindowBootstrapSnapshot(capturedBootstrap)
	if err != nil {
		t.Fatalf("decodeWindowBootstrapSnapshot() error = %v", err)
	}
	if snapshot["page"] != "chat" {
		t.Fatalf("captured snapshot = %#v, want page=chat", snapshot)
	}
}

func assertOpenNewWindowResult(t *testing.T, ok bool, windowID, cwd string) {
	t.Helper()

	if !ok {
		t.Fatal("ui/openNewWindow ok = false, want true")
	}
	if windowID != "window-7" {
		t.Fatalf("ui/openNewWindow windowID = %q, want window-7", windowID)
	}
	if cwd != "/tmp/project" {
		t.Fatalf("ui/openNewWindow cwd = %q, want /tmp/project", cwd)
	}
}

func assertOpenNewWindowParams(t *testing.T, group string, n int, cwd string) {
	t.Helper()

	if group != "team-alpha" {
		t.Fatalf("captured group = %q, want team-alpha", group)
	}
	if n != 0 {
		t.Fatalf("captured n = %d, want 0", n)
	}
	if cwd != "/tmp/project" {
		t.Fatalf("captured cwd = %q, want /tmp/project", cwd)
	}
}

func TestHandleCopyTextHeadlessReturnsSoftFailure(t *testing.T) {
	t.Parallel()

	result, err := handleCopyText(&App{}, "hello")
	if err != nil {
		t.Fatalf("handleCopyText() error = %v", err)
	}
	if ok, _ := result["ok"].(bool); ok {
		t.Fatalf("handleCopyText() ok = true, want false")
	}
	if result["error"] != "clipboard not available in headless mode" {
		t.Fatalf("handleCopyText() error = %#v", result["error"])
	}
}

func newWailsRPCServer(t *testing.T, app *App) *rpcpkg.Server {
	t.Helper()

	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewRPCHandlers(app, nil, nil).Handlers)
	return server
}

func dispatchBootstrapGet(t *testing.T, server *rpcpkg.Server) struct {
	Snapshot map[string]any `json:"snapshot"`
} {
	t.Helper()

	raw, err := server.Dispatch(context.Background(), "ui/windowBootstrap/get", json.RawMessage(`{
		"_aoClientKind":"desktop-wails",
		"_aoClientRoute":"/chat"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/windowBootstrap/get) error = %v", err)
	}

	var result struct {
		Snapshot map[string]any `json:"snapshot"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/windowBootstrap/get) error = %v", err)
	}
	return result
}
