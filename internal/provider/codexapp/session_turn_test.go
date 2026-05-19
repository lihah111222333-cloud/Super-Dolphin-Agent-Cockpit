package codexapp

import (
	"context"
	"encoding/json"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestStartTurnAppliesTurnToolScopeRuntimeConfig(t *testing.T) {
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		if method == "turn/start" {
			return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-1"}})
		}
		return mustJSON(map[string]any{"ok": true})
	})
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setRuntimeConfig(map[string]any{
		"cwd":                          "/old",
		"additionalWorkingDirectories": []string{"/old-extra"},
	})

	handle, err := s.StartTurn(context.Background(), dto.TurnRequest{
		ThreadID:                     "provider-thread-1",
		CWD:                          " /new ",
		AdditionalWorkingDirectories: []string{" /new-extra "},
		Inputs:                       []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if handle.ProviderID() != "turn-1" {
		t.Fatalf("ProviderID() = %q, want turn-1", handle.ProviderID())
	}

	got := s.RuntimeConfigSnapshot()
	if got["cwd"] != "/new" {
		t.Fatalf("runtime cwd = %#v, want /new", got["cwd"])
	}
	if roots := providershared.ConfigStringSlice(got, "additionalWorkingDirectories"); len(roots) != 1 || roots[0] != "/new-extra" {
		t.Fatalf("runtime additionalWorkingDirectories = %#v, want [/new-extra]", got["additionalWorkingDirectories"])
	}
}

func TestApplyTurnToolScopeRuntimeConfigUpdatesCWDAndRoots(t *testing.T) {
	s := &session{runtimeConfig: map[string]any{
		"cwd":                          "/old",
		"additionalWorkingDirectories": []string{"/old-extra"},
	}}

	err := s.applyTurnToolScopeRuntimeConfig(dto.TurnRequest{
		CWD:                          " /new ",
		AdditionalWorkingDirectories: []string{"/new-extra"},
	})
	if err != nil {
		t.Fatalf("applyTurnToolScopeRuntimeConfig() error = %v", err)
	}

	got := s.RuntimeConfigSnapshot()
	if got["cwd"] != "/new" {
		t.Fatalf("runtime cwd = %#v, want /new", got["cwd"])
	}
	if roots := providershared.ConfigStringSlice(got, "additionalWorkingDirectories"); len(roots) != 1 || roots[0] != "/new-extra" {
		t.Fatalf("runtime additionalWorkingDirectories = %#v, want [/new-extra]", got["additionalWorkingDirectories"])
	}
}

func TestApplyTurnToolScopeRuntimeConfigClearsStaleAdditionalRoots(t *testing.T) {
	s := &session{runtimeConfig: map[string]any{
		"cwd":                          "/old",
		"additionalWorkingDirectories": []string{"/old-extra"},
	}}

	err := s.applyTurnToolScopeRuntimeConfig(dto.TurnRequest{CWD: "/new"})
	if err != nil {
		t.Fatalf("applyTurnToolScopeRuntimeConfig() error = %v", err)
	}

	got := s.RuntimeConfigSnapshot()
	if got["cwd"] != "/new" {
		t.Fatalf("runtime cwd = %#v, want /new", got["cwd"])
	}
	if roots := got["additionalWorkingDirectories"]; roots != nil {
		t.Fatalf("runtime additionalWorkingDirectories = %#v, want cleared", roots)
	}
}

func TestApplyTurnToolScopeRuntimeConfigRejectsAdditionalRootsWithoutCWD(t *testing.T) {
	s := &session{runtimeConfig: map[string]any{"cwd": "/old"}}

	err := s.applyTurnToolScopeRuntimeConfig(dto.TurnRequest{
		AdditionalWorkingDirectories: []string{"/new-extra"},
	})
	if err == nil {
		t.Fatal("applyTurnToolScopeRuntimeConfig() error = nil, want error")
	}
	if got := s.RuntimeConfigSnapshot()["cwd"]; got != "/old" {
		t.Fatalf("runtime cwd = %#v, want old cwd preserved", got)
	}
}
