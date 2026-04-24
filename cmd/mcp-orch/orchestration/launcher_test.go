package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

func TestLauncherHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LAUNCHER_HELPER") == "1" {
		select {}
	}
}

func TestLocalLauncher_LaunchStop(t *testing.T) {
	agent := &agentRuntime{id: "agent-1", command: []string{os.Args[0], "-test.run=^TestLauncherHelperProcess$"}, env: []string{"GO_WANT_LAUNCHER_HELPER=1"}}
	launcher := NewLocalLauncher(nil, silentLogger()).(*localLauncher)
	if _, err := launcher.Launch(context.Background(), agent, LaunchRequest{}); err != nil || agent.cmd == nil || agent.cmd.Process == nil {
		t.Fatalf("Launch() err=%v cmd=%#v", err, agent.cmd)
	}
	if err := launcher.Stop(context.Background(), agent); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- agent.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait() timed out after Stop()")
	}
}

func TestRemoteLauncher_LaunchStop(t *testing.T) {
	var started, stopped map[string]any
	launcher := remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
		"thread/stop": handler.New(func(_ context.Context, req map[string]any) (struct{}, error) { stopped = req; return struct{}{}, nil }),
	})
	agent := &agentRuntime{id: "agent-1"}
	got, err := launcher.Launch(context.Background(), agent, LaunchRequest{
		Prompt:      "hello",
		Name:        "Worker UI",
		ParentID:    "agent-root",
		AgentType:   "worker",
		MemoryScope: "local",
	})
	if err != nil || got.ThreadID != "thread-1" || agent.remoteThreadID != "thread-1" {
		t.Fatalf("Launch() got=%#v err=%v started=%#v agent=%#v", got, err, started, agent)
	}
	if started["name"] != "Worker UI" {
		t.Fatalf("Launch() name = %#v, want Worker UI", started["name"])
	}
	// thread/start must not receive a separate `prompt` key: the server treats
	// it as a legacy alias for `name` and rejects (-32602) when the two differ.
	if _, ok := started["prompt"]; ok {
		t.Fatalf("Launch() started contains prompt=%#v; want no prompt field", started["prompt"])
	}
	if started["agent_type"] != "worker" || started["parent_agent_id"] != "agent-root" || started["agent_memory_scope"] != "local" {
		t.Fatalf("Launch() metadata = %#v, want agent_type/parent_agent_id/agent_memory_scope", started)
	}
	if err := launcher.Stop(context.Background(), agent); err != nil || stopped["thread_id"] != "thread-1" {
		t.Fatalf("Stop() err=%v stopped=%#v", err, stopped)
	}
}

func TestRemoteLauncher_LaunchUsesFriendlyTaskNameForTechnicalAgentName(t *testing.T) {
	var started map[string]any
	launcher := remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
	})
	_, err := launcher.Launch(context.Background(), &agentRuntime{id: "worker-agent"}, LaunchRequest{
		Name:   "worker-agent",
		Prompt: "请负责定位登录回调 500 根因，并给出最小修复方案",
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if started["name"] != "定位登录回调 500 根因" {
		t.Fatalf("Launch() name = %#v, want friendly task title", started["name"])
	}
}

func TestLooksTechnicalManagedAgentName_Pin(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "blank after normalize", input: " [ ] ", want: true},
		{name: "slash separated", input: "ops/worker", want: true},
		{name: "backslash separated", input: `ops\worker`, want: true},
		{name: "single generic token", input: "Worker", want: true},
		{name: "generic hyphenated tokens", input: "planner-reviewer", want: true},
		{name: "generic underscored tokens", input: "helper_tmp", want: true},
		{name: "generic dotted tokens", input: "research.review", want: true},
		{name: "mixed token with punctuation", input: "worker-api", want: true},
		{name: "delimiter only", input: "-", want: true},
		{name: "digits without punctuation", input: "worker2", want: true},
		{name: "spaced digits", input: "Worker 2", want: true},
		{name: "spaced punctuation", input: "Worker UI-v2", want: true},
		{name: "spaced friendly title", input: "Worker UI", want: false},
		{name: "compact friendly title", input: "Payments", want: false},
		{name: "spaced non ascii title", input: "修复 worker", want: false},
		{name: "compact non ascii title", input: "工程师", want: false},
		{name: "digit only name", input: "111", want: false},
		{name: "digit only single", input: "42", want: false},
		{name: "digit only leading zero", input: "007", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksTechnicalManagedAgentName(tt.input); got != tt.want {
				t.Fatalf("looksTechnicalManagedAgentName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestRemoteLauncher_SubmitTurn(t *testing.T) {
	var req map[string]any
	launcher := remoteLocalLauncher(t, handler.Map{
		"turn/start": handler.New(func(_ context.Context, got map[string]any) (map[string]any, error) {
			req = got
			return map[string]any{"turn_id": "turn-1"}, nil
		}),
	})
	turnID, err := launcher.SubmitTurn(context.Background(), &agentRuntime{remoteThreadID: "thread-1"}, TurnSubmission{
		Inputs:       []shareddto.InputItem{{Type: "text", Content: "hi"}},
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil || turnID != "turn-1" || req["thread_id"] != "thread-1" || req["output_schema"] == nil {
		t.Fatalf("SubmitTurn() turnID=%q err=%v req=%#v", turnID, err, req)
	}
}

func TestRemoteLauncher_ReconnectOnStopped(t *testing.T) {
	addr, accepts := startRPCServer(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}}, nil
		}),
	})
	launcher := NewRemoteLauncher(addr).(*remoteLauncher)
	t.Cleanup(func() { _ = launcher.Close() })
	for _, id := range []string{"agent-1", "agent-2"} {
		if _, err := launcher.Launch(context.Background(), &agentRuntime{id: id}, LaunchRequest{Command: []string{"ignored"}}); err != nil {
			t.Fatalf("Launch(%s) error = %v", id, err)
		}
		if id == "agent-1" {
			_ = launcher.client.Close()
		}
	}
	if got := atomic.LoadInt32(accepts); got < 2 {
		t.Fatalf("accepted connections = %d, want >= 2", got)
	}
}

func TestRemoteLauncher_RPCTimeout(t *testing.T) {
	launcher := remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := launcher.Launch(ctx, &agentRuntime{id: "agent-1"}, LaunchRequest{Command: []string{"ignored"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Launch() error = %v, want deadline exceeded", err)
	}
}

func TestService_LaunchWithLocal(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), NewLocalLauncher(nil, silentLogger()), nil, nil, nil)
	req := LaunchRequest{AgentID: "agent-1", Command: []string{os.Args[0], "-test.run=^TestLauncherHelperProcess$"}, Env: []string{"GO_WANT_LAUNCHER_HELPER=1"}}
	if err := svc.LaunchAgent(context.Background(), req); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	agent := svc.agents["agent-1"]
	t.Cleanup(func() { _ = stopProcess(agent.cmd); _ = agent.cmd.Wait() })
	if agent.cmd == nil || agent.remoteThreadID != "" || agent.state != agentdto.StateIdle {
		t.Fatalf("agent = %#v", agent)
	}
}

func TestService_LaunchWithRemote(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
	}), nil, nil, nil)
	if err := svc.LaunchAgent(context.Background(), LaunchRequest{AgentID: "agent-1", Command: []string{"ignored"}}); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	agent := svc.agents["agent-1"]
	if agent.cmd != nil || agent.remoteThreadID != "thread-1" || agent.remoteAgentID != "remote-1" || agent.state != agentdto.StateIdle {
		t.Fatalf("agent = %#v", agent)
	}
}

func TestService_SubmitTurnRemoteMode(t *testing.T) {
	var got, renamed map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/name/set": handler.New(func(_ context.Context, req map[string]any) (struct{}, error) {
			renamed = req
			return struct{}{}, nil
		}),
		"turn/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			got = req
			return map[string]any{"turn_id": "turn-1"}, nil
		}),
	}), nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state, agent.remoteThreadID, agent.name = agentdto.StateIdle, "thread-1", "worker-agent"
	svc.agents[agent.id] = agent
	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, Inputs: []shareddto.InputItem{{Type: "text", Content: "请负责定位登录回调 500 根因，并给出最小修复方案"}}}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}
	if agent.queue.Len() != 0 || agent.activeTurnID != "turn-1" || agent.state != agentdto.StateTurnStarting || got["thread_id"] != "thread-1" {
		t.Fatalf("agent=%#v req=%#v", agent, got)
	}
	if renamed["name"] != "定位登录回调 500 根因" || agent.name != "定位登录回调 500 根因" {
		t.Fatalf("rename=%#v agent=%#v", renamed, agent)
	}
}

func TestService_LaunchWithRemoteStoresFriendlyDisplayName(t *testing.T) {
	var started map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
	}), nil, nil, nil)
	if err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID: "worker-agent",
		Name:    "worker-agent",
		Prompt:  "请负责定位登录回调 500 根因，并给出最小修复方案",
		Command: []string{"ignored"},
	}); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	agent := svc.agents["worker-agent"]
	if agent == nil || agent.name != "定位登录回调 500 根因" || started["name"] != "定位登录回调 500 根因" {
		t.Fatalf("agent=%#v started=%#v", agent, started)
	}
}

func TestService_SubmitTurnLocalMode(t *testing.T) {
	starter := &stubTurnStarter{}
	svc := NewService(silentLogger(), event.NewDispatcher(), NewLocalLauncher(starter, silentLogger()), nil, starter, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state, agent.cmd = agentdto.StateIdle, &exec.Cmd{}
	svc.agents[agent.id] = agent
	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, ThreadID: "thread-1"}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}
	if agent.queue.Len() != 1 || agent.state != agentdto.StateTurnQueued {
		t.Fatalf("after submit queue=%d state=%q", agent.queue.Len(), agent.state)
	}
	if work := svc.claimTurnWork(context.Background()); len(work) != 1 || agent.queue.Len() != 0 || starter.startCalls != 0 || agent.state != agentdto.StateTurnStarting {
		t.Fatalf("after claim queue=%d work=%d startCalls=%d state=%q", agent.queue.Len(), len(work), starter.startCalls, agent.state)
	}
}

func remoteLocalLauncher(t *testing.T, methods handler.Map) *remoteLauncher {
	local := jrpcserver.NewLocal(methods, nil)
	t.Cleanup(func() { local.Close() })
	launcher := NewRemoteLauncher("local").(*remoteLauncher)
	launcher.client = local.Client
	t.Cleanup(func() { _ = launcher.Close() })
	return launcher
}

func startRPCServer(t *testing.T, methods handler.Map) (string, *int32) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	var accepts int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&accepts, 1)
			go func(c net.Conn) { _ = jrpc2.NewServer(methods, nil).Start(channel.Line(c, c)).WaitStatus() }(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), &accepts
}
