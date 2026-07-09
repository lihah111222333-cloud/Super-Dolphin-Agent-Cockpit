package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func assertAgentAbsent(t *testing.T, svc *service, agentID string) {
	t.Helper()
	svc.registry.mu.Lock()
	defer svc.registry.mu.Unlock()
	if _, ok := svc.registry.agents[agentID]; ok {
		t.Fatalf("agent %q was created despite rejected launch", agentID)
	}
}

func launchServiceRejectingThreadStart(t *testing.T) (*service, *bool) {
	t.Helper()
	called := false
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			called = true
			t.Fatalf("thread/start called unexpectedly with req=%#v", req)
			return nil, nil
		}),
	}), nil, nil, nil)
	return svc, &called
}

func TestService_LaunchAgent_RejectsMissingCwdWithoutParentBeforeThreadStart(t *testing.T) {
	svc, called := launchServiceRejectingThreadStart(t)
	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID: "child-1",
		Command: []string{"ignored"},
	})
	if !errors.Is(err, contract.ErrLaunchCWDRequired) {
		t.Fatalf("LaunchAgent() error = %v, want ErrLaunchCWDRequired", err)
	}
	if *called {
		t.Fatal("thread/start was called")
	}
	assertAgentAbsent(t, svc, "child-1")
}

func TestService_LaunchAgentSnapshot_RejectsMissingCwdWithoutParentBeforeThreadStart(t *testing.T) {
	svc, called := launchServiceRejectingThreadStart(t)
	_, err := svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID: "child-1",
		Command: []string{"ignored"},
	})
	if !errors.Is(err, contract.ErrLaunchCWDRequired) {
		t.Fatalf("LaunchAgentSnapshot() error = %v, want ErrLaunchCWDRequired", err)
	}
	if *called {
		t.Fatal("thread/start was called")
	}
	assertAgentAbsent(t, svc, "child-1")
}

func TestService_LaunchAgent_RejectsMissingCwdWhenParentDoesNotExist(t *testing.T) {
	svc, called := launchServiceRejectingThreadStart(t)
	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:  "child-1",
		ParentID: "missing-parent",
		Command:  []string{"ignored"},
	})
	if !errors.Is(err, contract.ErrLaunchCWDRequired) {
		t.Fatalf("LaunchAgent() error = %v, want ErrLaunchCWDRequired", err)
	}
	if *called {
		t.Fatal("thread/start was called")
	}
	assertAgentAbsent(t, svc, "child-1")
}

func TestService_LaunchAgent_RejectsMissingCwdWhenParentHasNoCwd(t *testing.T) {
	svc, called := launchServiceRejectingThreadStart(t)
	parent := svc.newAgentLocked("parent-1")
	parent.cwd = ""
	svc.registry.agents[parent.id] = parent
	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:  "child-1",
		ParentID: "parent-1",
		Command:  []string{"ignored"},
	})
	if !errors.Is(err, contract.ErrLaunchCWDRequired) {
		t.Fatalf("LaunchAgent() error = %v, want ErrLaunchCWDRequired", err)
	}
	if *called {
		t.Fatal("thread/start was called")
	}
	assertAgentAbsent(t, svc, "child-1")
}

func TestService_LaunchAgent_RejectsWhitespaceCwdWithoutParentFallback(t *testing.T) {
	svc, called := launchServiceRejectingThreadStart(t)
	parent := svc.newAgentLocked("parent-1")
	parent.cwd = "/repo/parent"
	svc.registry.agents[parent.id] = parent
	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:  "child-1",
		ParentID: "parent-1",
		Cwd:      "   ",
		Command:  []string{"ignored"},
	})
	if !errors.Is(err, contract.ErrLaunchCWDInvalid) {
		t.Fatalf("LaunchAgent() error = %v, want ErrLaunchCWDInvalid", err)
	}
	if *called {
		t.Fatal("thread/start was called")
	}
	assertAgentAbsent(t, svc, "child-1")
}

func TestService_LaunchAgent_RejectsMissingCwdWhenPersistedParentHasNoCwd(t *testing.T) {
	svc, called := launchServiceRejectingThreadStart(t)
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "thread-parent", AgentID: "agent-parent", Name: "parent", Status: "created"},
	}}
	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:  "child-1",
		ParentID: "agent-parent",
		Command:  []string{"ignored"},
	})
	if !errors.Is(err, contract.ErrLaunchCWDRequired) {
		t.Fatalf("LaunchAgent() error = %v, want ErrLaunchCWDRequired", err)
	}
	if *called {
		t.Fatal("thread/start was called")
	}
	assertAgentAbsent(t, svc, "child-1")
}

func TestService_LaunchAgent_InheritsPersistedParentCwdWhenRuntimeMissing(t *testing.T) {
	var started map[string]any
	parentCWD := testCWD(t, "parent")
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-child"}, "agentId": "remote-child"}, nil
		}),
	}), nil, nil, nil)
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "thread-parent", AgentID: "agent-parent", Name: "parent", Cwd: parentCWD, Status: "created"},
	}}

	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:  "agent-child",
		ParentID: "agent-parent",
		Command:  []string{"ignored"},
	})
	if err != nil {
		t.Fatalf("LaunchAgent() error = %v, want persisted parent cwd inheritance", err)
	}
	if got, _ := started["cwd"].(string); got != parentCWD {
		t.Fatalf("thread/start cwd = %q, want persisted parent cwd", got)
	}
}

func TestService_LaunchAgent_InheritsRuntimeParentCwd(t *testing.T) {
	var started map[string]any
	parentCWD := testCWD(t, "runtime-parent")
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-child"}, "agentId": "remote-child"}, nil
		}),
	}), nil, nil, nil)
	parent := svc.newAgentLocked("agent-parent")
	parent.cwd = parentCWD
	svc.registry.agents[parent.id] = parent

	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:  "agent-child",
		ParentID: "agent-parent",
		Command:  []string{"ignored"},
	})
	if err != nil {
		t.Fatalf("LaunchAgent() error = %v, want runtime parent cwd inheritance", err)
	}
	if got, _ := started["cwd"].(string); got != parentCWD {
		t.Fatalf("thread/start cwd = %q, want runtime parent cwd", got)
	}
}

func TestService_LaunchAgentSnapshot_InheritsPersistedParentCwdWhenRuntimeMissing(t *testing.T) {
	var started map[string]any
	parentCWD := testCWD(t, "parent")
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-child"}, "agentId": "remote-child"}, nil
		}),
	}), nil, nil, nil)
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "thread-parent", AgentID: "agent-parent", Name: "parent", Cwd: parentCWD, Status: "created"},
	}}

	if _, err := svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{AgentID: "agent-child", ParentID: "agent-parent", Command: []string{"ignored"}}); err != nil {
		t.Fatalf("LaunchAgentSnapshot() error = %v, want persisted parent cwd inheritance", err)
	}
	if got, _ := started["cwd"].(string); got != parentCWD {
		t.Fatalf("thread/start cwd = %q, want persisted parent cwd", got)
	}
}

func TestService_LaunchAgent_RejectsInvalidCwdBeforeThreadStart(t *testing.T) {
	for _, cwd := range []string{".", "./", "relative", "relative/path", " /tmp/repo ", "   "} {
		t.Run(cwd, func(t *testing.T) {
			svc, called := launchServiceRejectingThreadStart(t)
			err := svc.LaunchAgent(context.Background(), LaunchRequest{
				AgentID: "child-1",
				Cwd:     cwd,
				Command: []string{"ignored"},
			})
			if !errors.Is(err, contract.ErrLaunchCWDInvalid) {
				t.Fatalf("LaunchAgent() error = %v, want ErrLaunchCWDInvalid", err)
			}
			if *called {
				t.Fatal("thread/start was called")
			}
			assertAgentAbsent(t, svc, "child-1")
		})
	}
}
