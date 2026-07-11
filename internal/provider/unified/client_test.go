package unified_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestClient_StartSession_SelectsCorrectDriver(t *testing.T) {
	claudeSession, codexSession := &mockSession{threadID: "claude-thread"}, &mockSession{threadID: "codex-thread"}
	claude, codex := &mockDriver{name: "claude", session: claudeSession}, &mockDriver{name: "codex", session: codexSession}
	registry := unified.NewRegistry(unified.RegistryParams{Drivers: []contract.DriverFactory{
		{Name: "claude", Create: func() contract.Driver { return claude }},
		{Name: "codex", Create: func() contract.Driver { return codex }},
	}})
	sessions := unified.NewSessionManager(nil)
	client := unified.NewClient(registry, sessions, nil)
	got, err := client.StartSession(context.Background(), dto.StartSessionRequest{Provider: "claude", AgentID: "agent-1"})
	registered, regErr := sessions.Get("agent-1")
	if err != nil || regErr != nil || got != claudeSession || registered != claudeSession || claude.started != 1 || codex.started != 0 {
		t.Fatalf("start session mismatch: session=%v err=%v registered=%v regErr=%v starts=%d/%d", got, err, registered, regErr, claude.started, codex.started)
	}
}

func TestClient_StartSession_UnknownProvider(t *testing.T) {
	client := unified.NewClient(unified.NewRegistry(unified.RegistryParams{}), nil, nil)
	if _, err := client.StartSession(context.Background(), dto.StartSessionRequest{Provider: "unknown"}); err == nil {
		t.Fatal("expected unknown provider error")
	}
}

func TestClient_ResumeSession_SelectsCorrectDriver(t *testing.T) {
	claudeSession, codexSession := &mockSession{threadID: "claude-thread"}, &mockSession{threadID: "codex-thread"}
	claude, codex := &mockDriver{name: "claude", session: claudeSession}, &mockDriver{name: "codex", session: codexSession}
	registry := unified.NewRegistry(unified.RegistryParams{Drivers: []contract.DriverFactory{
		{Name: "claude", Create: func() contract.Driver { return claude }},
		{Name: "codex", Create: func() contract.Driver { return codex }},
	}})
	sessions := unified.NewSessionManager(nil)
	client := unified.NewClient(registry, sessions, nil)
	got, err := client.ResumeSession(context.Background(), dto.ResumeSessionRequest{Provider: "codex", AgentID: "agent-2"})
	sessions.Activate("agent-2")
	registered, regErr := sessions.Get("agent-2")
	if err != nil || regErr != nil || got != codexSession || registered != codexSession || codex.resumed != 1 || claude.resumed != 0 {
		t.Fatalf("resume session mismatch: session=%v err=%v registered=%v regErr=%v resumes=%d/%d", got, err, registered, regErr, codex.resumed, claude.resumed)
	}
}

func TestClient_ResumeSessionRegistersPendingUntilActivated(t *testing.T) {
	claudeSession, codexSession := &mockSession{threadID: "claude-thread"}, &mockSession{threadID: "codex-thread"}
	claude, codex := &mockDriver{name: "claude", session: claudeSession}, &mockDriver{name: "codex", session: codexSession}
	registry := unified.NewRegistry(unified.RegistryParams{Drivers: []contract.DriverFactory{
		{Name: "claude", Create: func() contract.Driver { return claude }},
		{Name: "codex", Create: func() contract.Driver { return codex }},
	}})
	sessions := unified.NewSessionManager(nil)
	client := unified.NewClient(registry, sessions, nil)

	got, err := client.ResumeSession(context.Background(), dto.ResumeSessionRequest{Provider: "codex", AgentID: "agent-pending"})
	if err != nil || got != codexSession {
		t.Fatalf("ResumeSession() = (%v, %v), want returned codex session", got, err)
	}
	if registered, regErr := sessions.Get("agent-pending"); !errors.Is(regErr, contract.ErrSessionNotFound) || registered != nil {
		t.Fatalf("pending resume visible before activation: session=%v err=%v", registered, regErr)
	}
	sessions.Activate("agent-pending")
	registered, regErr := sessions.Get("agent-pending")
	if regErr != nil || registered != codexSession {
		t.Fatalf("activated resume = (%v, %v), want codex session", registered, regErr)
	}
}

func TestClient_ResumeSession_UnknownProvider(t *testing.T) {
	client := unified.NewClient(unified.NewRegistry(unified.RegistryParams{}), nil, nil)
	if _, err := client.ResumeSession(context.Background(), dto.ResumeSessionRequest{Provider: "unknown"}); err == nil {
		t.Fatal("expected unknown provider error")
	}
}
