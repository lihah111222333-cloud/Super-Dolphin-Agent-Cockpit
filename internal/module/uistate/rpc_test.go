package uistate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
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

	if string(result) != "null" {
		t.Fatalf("ui/preferences/get scoped settings.provider.active = %s, want null for frontend global fallback", result)
	}
}
