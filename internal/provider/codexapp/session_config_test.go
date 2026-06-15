package codexapp

import (
	"context"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestSessionImplementsThreadConfigReader(t *testing.T) {
	t.Parallel()

	var raw any = &session{}
	reader, ok := raw.(interface {
		ReadConfig(context.Context, string) (dto.ThreadConfig, error)
	})
	if !ok {
		t.Fatal("*session does not implement ReadConfig")
	}
	cfg, err := reader.ReadConfig(context.Background(), "")
	if err == nil {
		t.Fatalf("ReadConfig() error = nil, want missing thread id error; cfg=%#v", cfg)
	}
}

func TestSessionReadConfigUsesRuntimeConfigSnapshot(t *testing.T) {
	t.Parallel()

	s := &session{}
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{
		"model":          " gpt-5.5 ",
		"effort":         " high ",
		"personality":    "pragmatic",
		"approvalPolicy": " on-request ",
	})

	cfg, err := s.ReadConfig(context.Background(), "ui-thread-ignored")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	assertCodexThreadConfig(t, cfg, "provider-thread-1", "gpt-5.5", "high", "on-request")

	if got := s.RuntimeConfigSnapshot()["personality"]; got != "pragmatic" {
		t.Fatalf("runtime personality = %#v, want pragmatic", got)
	}
}

func assertCodexThreadConfig(t *testing.T, got dto.ThreadConfig, threadID, model, effort, approvals string) {
	t.Helper()

	if got.ThreadID != threadID || got.Provider != "codex" || !got.SupportsThreadOverride {
		t.Fatalf("config identity = %#v, want thread/provider/support", got)
	}
	want := dto.ThreadConfigValues{Model: model, Effort: effort, Approvals: approvals}
	if got.Override != want {
		t.Fatalf("Override = %#v, want %#v", got.Override, want)
	}
	if got.Effective != want {
		t.Fatalf("Effective = %#v, want %#v", got.Effective, want)
	}
}
