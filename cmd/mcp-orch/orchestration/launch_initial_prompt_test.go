package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

func TestService_LaunchAgentSnapshotReturnsFinalAgentID(t *testing.T) {
	turnReq := make(chan map[string]any, 1)
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
		"turn/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			turnReq <- req
			return map[string]any{"turn_id": "turn-initial"}, nil
		}),
	}), nil, nil, nil)
	snapshot, err := svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID: "agent-1",
		Name:    "worker-agent",
		Prompt:  "please inspect the launch path",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	})
	if err != nil {
		t.Fatalf("LaunchAgentSnapshot() error = %v", err)
	}
	if snapshot.ID != "remote-1" || snapshot.AgentID != "remote-1" || snapshot.ThreadID != "thread-1" {
		t.Fatalf("snapshot identity = id:%q agent_id:%q thread:%q, want remote-1/remote-1/thread-1", snapshot.ID, snapshot.AgentID, snapshot.ThreadID)
	}
	select {
	case got := <-turnReq:
		if got["thread_id"] != "thread-1" {
			t.Fatalf("turn/start thread_id = %v, want thread-1", got["thread_id"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async launch prompt was not submitted within 5s")
	}
}

func TestService_LaunchAgentSnapshotFailsWhenInitialPromptSubmitFails(t *testing.T) {
	stopCalls := 0
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
		"turn/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, errors.New("turn submit blocked")
		}),
		"thread/stop": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			stopCalls++
			return map[string]any{}, nil
		}),
	}), nil, nil, nil)
	if _, err := svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID: "agent-1",
		Name:    "worker-agent",
		Prompt:  "please inspect the launch path",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	}); err == nil || !strings.Contains(err.Error(), "turn submit blocked") {
		t.Fatalf("LaunchAgentSnapshot() error = %v, want turn submit failure", err)
	}
	if stopCalls != 1 {
		t.Fatalf("thread/stop calls = %d, want 1 after initial prompt failure", stopCalls)
	}
}

func TestService_LaunchAgentSnapshotStopsAfterCanceledInitialPrompt(t *testing.T) {
	stopCalls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
		"turn/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			cancel()
			return nil, context.Canceled
		}),
		"thread/stop": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			stopCalls++
			return map[string]any{}, nil
		}),
	}), nil, nil, nil)
	if _, err := svc.LaunchAgentSnapshot(ctx, LaunchRequest{
		AgentID: "agent-1",
		Name:    "worker-agent",
		Prompt:  "please inspect the launch path",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	}); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("LaunchAgentSnapshot() error = %v, want context canceled", err)
	}
	if stopCalls != 1 {
		t.Fatalf("thread/stop calls = %d, want cleanup stop with independent context", stopCalls)
	}
}

func TestService_LaunchWithRemoteSubmitsInitialPrompt(t *testing.T) {
	var turnReq map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
		"turn/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			turnReq = req
			return map[string]any{"turn_id": "turn-initial"}, nil
		}),
	}), nil, nil, nil)
	if err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID: "agent-1",
		Name:    "worker-agent",
		Prompt:  "please inspect the launch path",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	}); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	agent := svc.agents["remote-1"]
	rawInput, _ := json.Marshal(turnReq["input"])
	if turnReq["thread_id"] != "thread-1" || !strings.Contains(string(rawInput), "please inspect the launch path") {
		t.Fatalf("turn/start request = %#v", turnReq)
	}
	if agent == nil || agent.activeTurnID != "turn-initial" || agent.state != agentdto.StateTurnRunning {
		t.Fatalf("agent after launch prompt = %#v", agent)
	}
}
