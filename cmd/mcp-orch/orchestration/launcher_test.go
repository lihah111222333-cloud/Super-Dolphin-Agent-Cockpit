package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
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
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	require.Equal(t, "thread-1", got.ThreadID)
	require.Equal(t, "thread-1", agent.remoteThreadID)
	require.Equal(t, "Worker UI", started["name"])
	require.Equal(t, "agent-1", started["agent_id"])
	// thread/start must not receive a separate `prompt` key: the server treats
	// it as a legacy alias for `name` and rejects (-32602) when the two differ.
	require.NotContains(t, started, "prompt")
	require.Equal(t, "worker", started["agent_type"])
	require.Equal(t, "agent-root", started["parent_agent_id"])
	require.Equal(t, "local", started["agent_memory_scope"])
	require.NoError(t, launcher.Stop(context.Background(), agent))
	require.Equal(t, "thread-1", stopped["thread_id"])
}

func TestRemoteLauncher_DisabledToolsUseStartConfig(t *testing.T) {
	var started map[string]any
	launcher := remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
	})

	_, err := launcher.Launch(context.Background(), &agentRuntime{id: "agent-1"}, LaunchRequest{
		Env: []string{"AGENT_DISABLED_TOOLS=Read, Bash"},
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if _, ok := started["disabled_tools"]; ok {
		t.Fatalf("thread/start got top-level disabled_tools=%#v; want config.disallowed_tools", started["disabled_tools"])
	}
	cfg, ok := started["config"].(map[string]any)
	if !ok {
		t.Fatalf("thread/start config = %#v, want object", started["config"])
	}
	if got := cfg["disallowed_tools"]; got != "Read, Bash" {
		t.Fatalf("config.disallowed_tools = %#v, want %q", got, "Read, Bash")
	}
}

func TestRemoteLauncher_Archive(t *testing.T) {
	var archived map[string]any
	launcher := remoteLocalLauncher(t, handler.Map{
		"thread/archive": handler.New(func(_ context.Context, req map[string]any) (struct{}, error) {
			archived = req
			return struct{}{}, nil
		}),
	})
	agent := &agentRuntime{id: "agent-1", remoteThreadID: "thread-1"}
	if err := launcher.Archive(context.Background(), agent); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if archived["thread_id"] != "thread-1" {
		t.Fatalf("Archive() archived=%#v, want thread_id thread-1", archived)
	}
}

func TestRemoteLauncher_LaunchUsesExplicitNameOnly(t *testing.T) {
	var started map[string]any
	launcher := remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
	})
	_, err := launcher.Launch(context.Background(), &agentRuntime{id: "dag-runtime-audit"}, LaunchRequest{
		Name:   "dag-runtime-audit",
		Prompt: "调研任务：定位 DAG runtime 路径",
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if started["name"] != "dag-runtime-audit" {
		t.Fatalf("Launch() name = %#v, want explicit name", started["name"])
	}
	if started["agent_id"] != "dag-runtime-audit" {
		t.Fatalf("Launch() agent_id = %#v, want dag-runtime-audit", started["agent_id"])
	}
	if _, ok := started["prompt"]; ok {
		t.Fatalf("Launch() started contains prompt=%#v; prompt must be submitted as a turn, not as name input", started["prompt"])
	}
}

// ---------------------------------------------------------------------------
// Regression guard: explicit managed-agent names must NEVER be auto-renamed.
//
// Root cause (2026-04-25): managedAgentLaunchDisplayName and
// maybeUpdateRemoteManagedAgentName treated slug-like names such as
// "dag-runtime-audit" as technical placeholders and replaced them with a
// prompt-derived title such as "调研任务". That bypassed the allowed naming
// paths: explicit launch name, UI start name, or thread/name/set.
//
// If you are reading this because a test broke: the naming policy is
// intentional. DO NOT reintroduce prompt-derived managed-agent names.
// ---------------------------------------------------------------------------

func TestNamePolicy_ManagedLaunchPreservesExplicitNames(t *testing.T) {
	names := []string{"dag-runtime-audit", "dag-entry-audit", "worker-agent", "helper_tmp", "research.review", "111"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var started map[string]any
			launcher := remoteLocalLauncher(t, handler.Map{
				"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
					started = req
					return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
				}),
			})
			_, err := launcher.Launch(context.Background(), &agentRuntime{id: "agent-" + name}, LaunchRequest{
				Name:   name,
				Prompt: "调研任务：定位 DAG runtime 路径，并输出 file:line 证据",
			})
			if err != nil {
				t.Fatalf("Launch() error = %v", err)
			}
			if started["name"] != name {
				t.Fatalf("REGRESSION: Launch() name = %#v, want %q (explicit name must NOT be replaced by prompt-derived title)", started["name"], name)
			}
		})
	}
}

func TestNamePolicy_SubmitTurnNeverAutoRenames(t *testing.T) {
	names := []string{"dag-runtime-audit", "dag-entry-audit", "worker-agent", "helper_tmp", "research.review", "111"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var renamed map[string]any
			svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
				"thread/name/set": handler.New(func(_ context.Context, req map[string]any) (struct{}, error) {
					renamed = req
					return struct{}{}, nil
				}),
				"turn/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					return map[string]any{"turn_id": "turn-1"}, nil
				}),
			}), nil, nil, nil)
			agent := svc.newAgentLocked("agent-" + name)
			agent.state, agent.remoteThreadID, agent.name = agentdto.StateIdle, "thread-1", name
			svc.agents[agent.id] = agent
			if err := svc.SubmitTurn(context.Background(), TurnSubmission{
				AgentID: agent.id,
				Inputs:  []shareddto.InputItem{{Type: "text", Content: "调研任务：定位 DAG runtime 路径，并输出 file:line 证据"}},
			}); err != nil {
				t.Fatalf("SubmitTurn() error = %v", err)
			}
			if renamed != nil {
				t.Fatalf("REGRESSION: thread/name/set was called for name %q -> %#v (turn submission must NOT auto-rename)", name, renamed)
			}
			if agent.name != name {
				t.Fatalf("REGRESSION: agent.name changed from %q to %q (turn submission must NOT auto-rename)", name, agent.name)
			}
		})
	}
}

func TestNamePolicy_ManagedAgentLaunchDisplayNameIgnoresPrompt(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		prompt string
		want   string
	}{
		{name: "slug preserved", input: "dag-runtime-audit", prompt: "调研任务：定位 DAG runtime 路径", want: "dag-runtime-audit"},
		{name: "worker preserved", input: "worker-agent", prompt: "请负责定位登录回调 500 根因", want: "worker-agent"},
		{name: "digit preserved", input: "111", prompt: "定位回调根因", want: "111"},
		{name: "empty stays empty", input: "", prompt: "调研任务：不能变成名字", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := managedAgentLaunchDisplayName(tt.input)
			if got != tt.want {
				t.Fatalf("managedAgentLaunchDisplayName(%q, %q) = %q, want %q", tt.input, tt.prompt, got, tt.want)
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
	agent := svc.agents["remote-1"]
	if agent == nil || agent.cmd != nil || agent.remoteThreadID != "thread-1" || agent.remoteAgentID != "remote-1" || agent.state != agentdto.StateIdle {
		t.Fatalf("agent = %#v", agent)
	}
}

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
	if agent.queue.Len() != 0 || agent.activeTurnID != "turn-1" || agent.state != agentdto.StateTurnRunning || got["thread_id"] != "thread-1" {
		t.Fatalf("agent=%#v req=%#v", agent, got)
	}
	if renamed != nil || agent.name != "worker-agent" {
		t.Fatalf("rename=%#v agent=%#v", renamed, agent)
	}
}

func TestService_SubmitTurnRemoteModeDoesNotHoldServiceLockDuringRPC(t *testing.T) {
	turnStarted := make(chan struct{})
	releaseTurn := make(chan struct{})
	stopCalls := make(chan struct{}, 1)
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"turn/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			close(turnStarted)
			<-releaseTurn
			return map[string]any{"turn_id": "turn-remote"}, nil
		}),
		"thread/stop": handler.New(func(_ context.Context, _ map[string]any) (struct{}, error) {
			stopCalls <- struct{}{}
			return struct{}{}, nil
		}),
	}), nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state, agent.remoteThreadID, agent.name = agentdto.StateIdle, "thread-1", "worker-agent"
	svc.agents[agent.id] = agent

	submitDone := make(chan error, 1)
	go func() {
		submitDone <- svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, Inputs: []shareddto.InputItem{{Type: "text", Content: "work"}}})
	}()
	select {
	case <-turnStarted:
	case <-time.After(time.Second):
		t.Fatal("turn/start was not called")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- svc.StopAgent(context.Background(), agent.id) }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopAgent() error while turn/start blocked = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("StopAgent() blocked behind remote SubmitTurn RPC")
	}
	select {
	case <-stopCalls:
	case <-time.After(time.Second):
		t.Fatal("thread/stop was not called")
	}

	close(releaseTurn)
	select {
	case <-submitDone:
	case <-time.After(time.Second):
		t.Fatal("SubmitTurn() did not finish after release")
	}
}

func TestService_LaunchWithRemoteStoresExplicitDisplayName(t *testing.T) {
	var started map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
		"turn/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"turn_id": "turn-1"}, nil
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
	agent := svc.agents["remote-1"]
	if agent == nil || agent.name != "worker-agent" || started["name"] != "worker-agent" {
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

// ---------------------------------------------------------------------------
// Regression guard: child agents spawned via orchestration_launch_agent must
// inherit cwd from their parent when the tool call omits it.
//
// Root cause (2026-05-15, bug "纯抽奖"): the orchestration_launch_agent tool
// schema makes cwd optional, and parent LLMs typically omit it. Without
// inheritance, req.Cwd reaches the launcher as "", propagates through
// thread/start to agent_threads.cwd, and the sidebar snapshot omits the cwd
// key entirely (sidebar_compat.go gates the key on agent.CWD != ""). On the
// frontend, thread-store-view.js getThreadsByMode treats an absent cwd as
// "include in every project view" (`if (!cwd) return true`), so the spawned
// child shows up in every non-dot project's list.
//
// DO NOT delete these tests without first removing the corresponding frontend
// "empty cwd ⇒ include" branch — they together prevent the regression.
// ---------------------------------------------------------------------------

func TestService_LaunchAgent_InheritsParentCwdWhenChildOmits(t *testing.T) {
	var started map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
	}), nil, nil, nil)
	parent := svc.newAgentLocked("parent-1")
	parent.cwd = "/repo/foo"
	svc.agents[parent.id] = parent
	if err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:  "child-1",
		ParentID: "parent-1",
		Command:  []string{"ignored"},
	}); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	if got, _ := started["cwd"].(string); got != "/repo/foo" {
		t.Fatalf("REGRESSION: thread/start cwd = %q, want %q (child must inherit parent's cwd when its own is empty)", got, "/repo/foo")
	}
}

func TestService_LaunchAgent_RespectsExplicitChildCwd(t *testing.T) {
	var started map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
	}), nil, nil, nil)
	parent := svc.newAgentLocked("parent-1")
	parent.cwd = "/repo/foo"
	svc.agents[parent.id] = parent
	if err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID:  "child-1",
		ParentID: "parent-1",
		Cwd:      "/repo/bar",
		Command:  []string{"ignored"},
	}); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	if got, _ := started["cwd"].(string); got != "/repo/bar" {
		t.Fatalf("explicit child cwd overridden: thread/start cwd = %q, want %q", got, "/repo/bar")
	}
}

func TestService_LaunchAgentSnapshot_InheritsParentCwdWhenChildOmits(t *testing.T) {
	var started map[string]any
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, req map[string]any) (map[string]any, error) {
			started = req
			return map[string]any{"thread": map[string]any{"id": "thread-1"}, "agentId": "remote-1"}, nil
		}),
	}), nil, nil, nil)
	parent := svc.newAgentLocked("parent-1")
	parent.cwd = "/repo/foo"
	svc.agents[parent.id] = parent
	if _, err := svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID:  "child-1",
		ParentID: "parent-1",
		Command:  []string{"ignored"},
	}); err != nil {
		t.Fatalf("LaunchAgentSnapshot() error = %v", err)
	}
	if got, _ := started["cwd"].(string); got != "/repo/foo" {
		t.Fatalf("REGRESSION: LaunchAgentSnapshot thread/start cwd = %q, want %q", got, "/repo/foo")
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
	return startRPCServerWithOptions(t, methods, nil)
}

func startPushRPCServer(t *testing.T, methods handler.Map) (string, *int32) {
	return startRPCServerWithOptions(t, methods, &jrpc2.ServerOptions{AllowPush: true})
}

func startRPCServerWithOptions(t *testing.T, methods handler.Map, opts *jrpc2.ServerOptions) (string, *int32) {
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
			go func(c net.Conn) { _ = jrpc2.NewServer(methods, opts).Start(channel.Line(c, c)).WaitStatus() }(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), &accepts
}
