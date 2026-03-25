package thread

import (
	"context"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

func TestServiceSetConfigConfiguresModelAndEffort(t *testing.T) {
	t.Parallel()

	model := "gpt-5.4"
	effort := "high"
	session := &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{"gpt-5.4"},
		readConfigResult: dto.ThreadConfig{
			ThreadID: "thread-1",
			Provider: "codex",
			Effective: dto.ThreadConfigValues{
				Model:  "gpt-5.4",
				Effort: "high",
			},
		},
	}
	sessions := &stubSessionProvider{session: session}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc := NewService(silentLogger(), nil, bindings, sessions, nil, nil, nil, nil)

	cfg, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{
		Model:  &model,
		Effort: &effort,
	})
	if err != nil {
		t.Fatalf("SetConfig() error = %v", err)
	}
	if session.configureCalls != 1 {
		t.Fatalf("configureCalls = %d, want 1", session.configureCalls)
	}
	if session.configurePatch.Model == nil || *session.configurePatch.Model != model {
		t.Fatalf("configurePatch.Model = %#v, want %q", session.configurePatch.Model, model)
	}
	if session.configurePatch.Effort == nil || *session.configurePatch.Effort != effort {
		t.Fatalf("configurePatch.Effort = %#v, want %q", session.configurePatch.Effort, effort)
	}
	if cfg.Effective.Model != model || cfg.Effective.Effort != effort {
		t.Fatalf("SetConfig() = %#v", cfg)
	}
}

func TestServiceSetConfigRejectsInvalidEffort(t *testing.T) {
	t.Parallel()

	effort := "turbo"
	session := &stubSession{threadID: "thread-1"}
	sessions := &stubSessionProvider{session: session}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc := NewService(silentLogger(), nil, bindings, sessions, nil, nil, nil, nil)

	if _, err := svc.SetConfig(context.Background(), "thread-1", dto.ThreadConfigPatch{Effort: &effort}); err == nil {
		t.Fatal("SetConfig() error = nil, want invalid effort")
	}
	if session.configureCalls != 0 {
		t.Fatalf("configureCalls = %d, want 0", session.configureCalls)
	}
}

func TestServiceReadRuntimeConfigUsesSessionSnapshot(t *testing.T) {
	t.Parallel()

	session := &stubSession{
		threadID: "thread-1",
		runtimeConfig: map[string]any{
			"modelProvider":         "openai",
			"developerInstructions": "be precise",
			"sandbox": map[string]any{
				"type": "workspace-write",
			},
			"toolRouting": map[string]any{
				"mode":                "dynamic",
				"routerProvider":      "router-x",
				"confidenceThreshold": 0.9,
				"timeoutSec":          11,
			},
		},
	}
	sessions := &stubSessionProvider{session: session}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc, ok := NewService(silentLogger(), nil, bindings, sessions, nil, nil, nil, nil).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	got, err := svc.ReadRuntimeConfig(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadRuntimeConfig() error = %v", err)
	}
	if got["modelProvider"] != "openai" || got["developerInstructions"] != "be precise" {
		t.Fatalf("ReadRuntimeConfig() = %#v", got)
	}
	sandbox, _ := got["sandbox"].(map[string]any)
	if sandbox["type"] != "workspace-write" {
		t.Fatalf("sandbox = %#v, want workspace-write", sandbox)
	}
	toolRouting, _ := got["toolRouting"].(map[string]any)
	if toolRouting["mode"] != "dynamic" || toolRouting["routerProvider"] != "router-x" {
		t.Fatalf("toolRouting = %#v", toolRouting)
	}
}
