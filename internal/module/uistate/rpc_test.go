package uistate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestUIStateGetAcceptsKnownDiffRevision(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	server := rpc.NewServer(rpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)
	_, err = server.Dispatch(context.Background(), "ui/state/get", json.RawMessage(`{"threadId":"thread-1","includeDiff":true,"knownDiffRevision":7}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/state/get) error = %v", err)
	}
}

func TestUIPreferencesGetDefaultsGlobalActiveProviderToCodex(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	server := rpc.NewServer(rpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)
	result, err := server.Dispatch(context.Background(), "ui/preferences/get", json.RawMessage(`{"key":"settings.provider.active"}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/preferences/get) error = %v", err)
	}

	var got string
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", result, err)
	}
	if got != "codex" {
		t.Fatalf("ui/preferences/get settings.provider.active = %q, want %q", got, "codex")
	}
}

func TestUIPreferencesGetDoesNotSynthesizeScopedActiveProvider(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	server := rpc.NewServer(rpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)
	result, err := server.Dispatch(context.Background(), "ui/preferences/get", json.RawMessage(`{"key":"settings.provider.active","cwd":"/tmp/new-install"}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/preferences/get) error = %v", err)
	}

	if string(result) != `"codex"` {
		t.Fatalf("ui/preferences/get scoped settings.provider.active = %s, want \"codex\" default for first-time install", result)
	}
}

func TestUIVideoSetAPIKeyPersistsWithoutExplicitSuperDolphinHome(t *testing.T) {
	home := t.TempDir()
	appData := filepath.Join(home, "AppData", "Roaming")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", appData)
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	t.Setenv("SILICONFLOW_API_KEY", "")

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	server := rpc.NewServer(rpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)
	if _, err := server.Dispatch(context.Background(), "ui/video/setApiKey", json.RawMessage(`{"apiKey":"sk-test-persist"}`)); err != nil {
		t.Fatalf("Dispatch(ui/video/setApiKey) error = %v", err)
	}

	path := defaultVideoEnvPathForTest(home, appData)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(data) != "SILICONFLOW_API_KEY=sk-test-persist\n" {
		t.Fatalf("video.env = %q, want persisted SiliconFlow key", data)
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("video.env mode = %o, want 600", mode)
	}
}

// TestUIVideoSetAPIKeyDoesNotMutateProcessEnvWhenPersistenceFails 确认持久化失败不会半应用新 key。
func TestUIVideoSetAPIKeyDoesNotMutateProcessEnvWhenPersistenceFails(t *testing.T) {
	home := t.TempDir()
	blockedHome := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(blockedHome, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", blockedHome, err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", blockedHome)
	t.Setenv("SILICONFLOW_API_KEY", "sk-existing")

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	server := rpc.NewServer(rpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)
	_, err = server.Dispatch(context.Background(), "ui/video/setApiKey", json.RawMessage(`{"apiKey":"sk-new-key"}`))
	if err == nil || !strings.Contains(err.Error(), "create video env directory") {
		t.Fatalf("Dispatch(ui/video/setApiKey) error = %v, want persistence failure", err)
	}
	if got := os.Getenv("SILICONFLOW_API_KEY"); got != "sk-existing" {
		t.Fatalf("SILICONFLOW_API_KEY = %q, want previous key after failed persistence", got)
	}
}

func defaultVideoEnvPathForTest(home, appData string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(appData, "Super Dolphin", "video.env")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Super Dolphin", "video.env")
	default:
		return filepath.Join(home, ".config", "Super Dolphin", "video.env")
	}
}
