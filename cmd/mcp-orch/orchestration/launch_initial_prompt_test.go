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

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherwire"
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

func TestService_LaunchAgentSnapshotRunsBeforeInitialPromptHook(t *testing.T) {
	events := make([]string, 0, 3)
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			events = append(events, "thread/start")
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
		"turn/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			events = append(events, "turn/start:"+req["thread_id"].(string))
			return map[string]any{"turn_id": "turn-initial"}, nil
		}),
	}), nil, nil, nil)

	_, err := svc.launchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID: "agent-1",
		Name:    "worker-agent",
		Prompt:  "please inspect the launch path",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	}, func(_ string, result LaunchResult) error {
		events = append(events, "record:"+strings.TrimSpace(result.ThreadID))
		return nil
	})

	if err != nil {
		t.Fatalf("launchAgentSnapshot() error = %v", err)
	}
	want := []string{"thread/start", "record:thread-1", "turn/start:thread-1"}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestService_LaunchAgentSnapshotStopsWhenBeforeInitialPromptHookFails(t *testing.T) {
	stopCalls := 0
	turnCalls := 0
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
		"turn/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			turnCalls++
			return map[string]any{"turn_id": "turn-initial"}, nil
		}),
		"thread/stop": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			stopCalls++
			return map[string]any{}, nil
		}),
	}), nil, nil, nil)

	_, err := svc.launchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID: "agent-1",
		Name:    "worker-agent",
		Prompt:  "please inspect the launch path",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	}, func(_ string, result LaunchResult) error {
		if result.ThreadID != "thread-1" {
			t.Fatalf("before prompt hook thread = %q, want thread-1", result.ThreadID)
		}
		return errors.New("spawn record failed")
	})

	if err == nil || !strings.Contains(err.Error(), "spawn record failed") {
		t.Fatalf("launchAgentSnapshot() error = %v, want spawn record failure", err)
	}
	if turnCalls != 0 {
		t.Fatalf("turn/start calls = %d, want 0 when before-prompt hook fails", turnCalls)
	}
	if stopCalls != 1 {
		t.Fatalf("thread/stop calls = %d, want 1 after before-prompt hook failure", stopCalls)
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

func TestForkedLaunchSubmitsInitialPromptToForkedThread(t *testing.T) {
	events := make([]string, 0, 2)
	var forkReq map[string]any
	var turnReq map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		launcherwire.MethodThreadStart: handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, errors.New("thread/start should not be called for forked launch")
		}),
		launcherwire.MethodThreadFork: handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			events = append(events, "thread/fork")
			forkReq = req
			return map[string]any{launcherwire.RespNewThreadID: "thread-child", launcherwire.RespAgentID: "agent-child"}, nil
		}),
		launcherwire.MethodThreadNameSet: handler.New(func(_ context.Context, _ map[string]any) (struct{}, error) {
			return struct{}{}, nil
		}),
		launcherwire.MethodTurnStart: handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			events = append(events, "turn/start")
			turnReq = req
			return map[string]any{launcherwire.RespTurnID: "turn-initial"}, nil
		}),
	}), nil, nil, nil)
	parent := svc.newAgentLocked("agent-parent")
	parent.state = agentdto.StateIdle
	parent.threadID = "thread-parent"
	parent.remoteThreadID = "thread-parent"
	parent.remoteAgentID = "agent-parent"
	svc.agents[parent.id] = parent

	snapshot, err := svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID:     "agent-child",
		Name:        "forked child",
		ParentID:    "agent-parent",
		ContextMode: "forked",
		Prompt:      "continue with inherited context",
		Cwd:         t.TempDir(),
		Command:     []string{"ignored"},
	})

	if err != nil {
		t.Fatalf("LaunchAgentSnapshot() error = %v", err)
	}
	if strings.Join(events, ",") != "thread/fork,turn/start" {
		t.Fatalf("events = %#v, want thread/fork then turn/start", events)
	}
	if forkReq[launcherwire.ParamThreadID] != "thread-parent" {
		t.Fatalf("thread/fork parent thread_id = %#v, want thread-parent", forkReq[launcherwire.ParamThreadID])
	}
	if turnReq[launcherwire.ParamThreadID] != "thread-child" {
		t.Fatalf("turn/start thread_id = %#v, want thread-child", turnReq[launcherwire.ParamThreadID])
	}
	rawInput, _ := json.Marshal(turnReq[launcherwire.ParamInput])
	if !strings.Contains(string(rawInput), "continue with inherited context") {
		t.Fatalf("turn/start input = %s, want initial prompt", string(rawInput))
	}
	if snapshot.AgentID != "agent-child" || snapshot.ThreadID != "thread-child" {
		t.Fatalf("snapshot = %#v, want child agent on forked thread", snapshot)
	}
}

func TestForkedLaunchRejectsMissingParentThread(t *testing.T) {
	tests := []struct {
		name        string
		setupParent func(*service)
		wantErr     string
	}{
		{
			name:    "parent missing",
			wantErr: "parent agent",
		},
		{
			name: "parent without remote thread",
			setupParent: func(svc *service) {
				parent := svc.newAgentLocked("agent-parent")
				parent.state = agentdto.StateIdle
				svc.agents[parent.id] = parent
			},
			wantErr: "remote thread id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{}), nil, nil, nil)
			if tt.setupParent != nil {
				tt.setupParent(svc)
			}

			_, err := svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
				AgentID:     "agent-child",
				Name:        "forked child",
				ParentID:    "agent-parent",
				ContextMode: "forked",
				Prompt:      "continue",
				Cwd:         t.TempDir(),
				Command:     []string{"ignored"},
			})

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LaunchAgentSnapshot() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
