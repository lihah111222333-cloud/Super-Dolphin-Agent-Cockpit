package orchestration

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

// -----------------------------------------------------------------------------
// exit monitor 测试辅助函数。
// -----------------------------------------------------------------------------

// newP3TestService 构造仅包含 dispatcher 和 session cleaner 的最小 service。
// 每次调用都返回独立实例，测试可并行注入 svc.agents 后验证 runner actor。
func newP3TestService(t *testing.T) (*service, *event.Dispatcher, *stopTestSessionCleaner) {
	t.Helper()
	dispatcher := event.NewDispatcher()
	cleaner := &stopTestSessionCleaner{}
	svc := NewService(silentLogger(), dispatcher, nil, cleaner, nil, nil)
	return svc, dispatcher, cleaner
}

// spawnP3TestCmd 启动短生命周期子进程并注册到 exit monitor。
// 测试借它覆盖真实 cmd.Wait 路径，而不必等待生产默认的长超时。
func spawnP3TestCmd(t *testing.T, svc *service, agentID string, launchSeq uint64) *exec.Cmd {
	t.Helper()
	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start(): %v", err)
	}
	agent := svc.newAgentLocked(agentID)
	agent.cmd = cmd
	agent.state = agentdto.StateIdle
	agent.launchSeq = launchSeq
	agent.sessionGeneration = 42
	svc.agents[agent.id] = agent
	svc.exitMonitor.Arm(exitmonitor.Target{AgentID: agentID, LaunchSeq: launchSeq, Cmd: cmd})
	agent.monitoredSeq = launchSeq
	t.Cleanup(func() { stopAndDrainServiceTestAgent(t, svc, agent) })
	return cmd
}

// -----------------------------------------------------------------------------
// 测试 1：同一 (agentID, launchSeq) 只发布一次退出事件
// -----------------------------------------------------------------------------

func TestExitEventExactlyOnceByLaunchSeq(t *testing.T) {
	t.Parallel()
	svc, _, _ := newP3TestService(t)

	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.launchSeq = 5
	svc.agents[agent.id] = agent

	// 第一次 Emit 应触发状态迁移。
	svc.exitMonitor.Emit("agent-1", 5, nil)
	select {
	case result := <-svc.exitMonitor.ExitEvents():
		svc.handleProcessExit(context.Background(), result.AgentID, result.LaunchSeq, result.Err)
	case <-time.After(time.Second):
		t.Fatal("first Emit did not publish exit event")
	}

	svc.mu.RLock()
	lastExited := agent.lastExitedSeq
	svc.mu.RUnlock()
	if lastExited != 5 {
		t.Fatalf("lastExitedSeq after first Emit = %d, want 5", lastExited)
	}

	// 同一 (agentID, launchSeq) 的第二次 Emit 会被 monitor 栅栏吞掉，事件通道保持为空。
	svc.exitMonitor.Emit("agent-1", 5, errors.New("duplicate"))
	select {
	case ev := <-svc.exitMonitor.ExitEvents():
		t.Fatalf("second Emit should have been coalesced, got event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	// 即使调用方绕过 monitor 直接传入同一 seq，handleProcessExit 的 agent 级栅栏也会挡住重复迁移。
	svc.handleProcessExit(context.Background(), "agent-1", 5, errors.New("bypass"))
	svc.mu.RLock()
	lastExitedAfter := agent.lastExitedSeq
	svc.mu.RUnlock()
	if lastExitedAfter != 5 {
		t.Fatalf("lastExitedSeq after duplicate handleProcessExit = %d, want 5", lastExitedAfter)
	}
}

// -----------------------------------------------------------------------------
// 测试 2：stop 路径复用同一个退出归属方，不能重复迁移
// -----------------------------------------------------------------------------

func TestStopPathReusesExitOwner(t *testing.T) {
	t.Parallel()
	svc, _, _ := newP3TestService(t)

	agent := svc.newAgentLocked("agent-2")
	agent.state = agentdto.StateIdle
	agent.launchSeq = 7
	svc.agents[agent.id] = agent

	// 第一次：模拟 launcher stop 发布退出事件。
	svc.exitMonitor.Emit("agent-2", 7, nil)
	select {
	case result := <-svc.exitMonitor.ExitEvents():
		svc.handleProcessExit(context.Background(), result.AgentID, result.LaunchSeq, result.Err)
	case <-time.After(time.Second):
		t.Fatal("synthetic Emit did not publish")
	}

	// 第二次：模拟并发 cmd.Wait 送达同一 (agentID, seq)，栅栏和 exactly-once 通道必须吞掉它。
	svc.exitMonitor.Emit("agent-2", 7, errors.New("cmd.Wait race"))
	select {
	case ev := <-svc.exitMonitor.ExitEvents():
		t.Fatalf("expected no further events, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	svc.mu.RLock()
	lastExited := agent.lastExitedSeq
	svc.mu.RUnlock()
	if lastExited != 7 {
		t.Fatalf("lastExitedSeq = %d, want 7", lastExited)
	}
}

// -----------------------------------------------------------------------------
// 测试 3：runner shutdown 返回前必须等待 monitor.Drain
// -----------------------------------------------------------------------------

func TestShutdownDrainWaitOwner(t *testing.T) {
	t.Parallel()
	svc, _, _ := newP3TestService(t)
	cmd := spawnP3TestCmd(t, svc, "agent-3", 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- NewRunnerActor(silentLogger(), svc).Run(ctx) }()
	waitForAgentMonitor(t, svc, "agent-3", 1)

	// 取消 runner ctx 后，drainOnStop 必须先 Drain monitor 再让 Run 返回。
	// 测试中手动结束进程以触发 cmd.Wait；生产路径由 StopAllAgents 的 SIGTERM 完成。
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = cmd.Process.Kill()
	}()
	cancel()

	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not return after drainOnStop")
	}

	// 此时 Drain 应已等待所有 Arm goroutine 退出；第二次 Drain 必须幂等并立即返回。
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer drainCancel()
	if err := svc.exitMonitor.Drain(drainCtx); err != nil {
		t.Fatalf("post-shutdown Drain returned %v; expected quiescent monitor", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 4：强杀超时路径仍只发布一次退出事件
// -----------------------------------------------------------------------------

func TestKillTimeoutStillEmitsSingleExitEvent(t *testing.T) {
	t.Parallel()
	svc, _, _ := newP3TestService(t)
	spawnP3TestCmd(t, svc, "agent-4", 3)

	// 缩短等待窗口，让 waitForProcessExit 快速进入强杀分支并验证崩溃窗口只发布一次退出事件。
	svc.processExitWaitTimeout = 50 * time.Millisecond

	// 启动 actor 消费退出事件；进程会保持存活，直到 waitForProcessExit 的 forceKill 触发。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- NewRunnerActor(silentLogger(), svc).Run(ctx) }()
	waitForAgentMonitor(t, svc, "agent-4", 3)

	if err := svc.waitForProcessExit(context.Background(), "agent-4", 3); err != nil {
		// 成功强杀时 waitForProcessExit 返回 nil；非 nil 通常表示进程已先行退出，也属于可接受路径。
		t.Logf("waitForProcessExit returned %v (expected on force-kill path)", err)
	}

	// 等待 monitor 投递退出事件并推进 lastExitedSeq。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.RLock()
		seq := svc.agents["agent-4"].lastExitedSeq
		svc.mu.RUnlock()
		if seq >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	svc.mu.RLock()
	finalSeq := svc.agents["agent-4"].lastExitedSeq
	svc.mu.RUnlock()
	if finalSeq != 3 {
		t.Fatalf("lastExitedSeq = %d, want 3 (single exit event)", finalSeq)
	}

	// 同一 seq 的额外合成 Emit 必须被 monitor 与 handleProcessExit 的双重栅栏吞掉。
	svc.exitMonitor.Emit("agent-4", 3, errors.New("duplicate after kill"))
	select {
	case ev := <-svc.exitMonitor.ExitEvents():
		t.Fatalf("unexpected duplicate event after kill: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	<-runDone
}

// -----------------------------------------------------------------------------
// 测试 5：process exit 对每个 seq 只推进一次状态机
// -----------------------------------------------------------------------------

// TestProcessExitStateMachine 验证 handleProcessExit 对同一 (agentID, launchSeq) 只生效一次。
// lastExitedSeq 栅栏必须阻止重复调用再次触发状态机迁移、session cleanup 或 stopped/failed 发布；
// 具体目标状态由更宽的 launch/stop 测试覆盖，这里只锁定处理器层的幂等边界。
func TestProcessExitStateMachine(t *testing.T) {
	t.Parallel()
	svc, _, cleaner := newP3TestService(t)

	agent := svc.newAgentLocked("agent-5")
	agent.launchSeq = 9
	agent.sessionGeneration = 17
	svc.agents[agent.id] = agent

	svc.handleProcessExit(context.Background(), "agent-5", 9, errors.New("boom"))
	svc.mu.RLock()
	lastExited := agent.lastExitedSeq
	exitedAt := agent.exitedAt
	cmdNil := agent.cmd == nil
	svc.mu.RUnlock()
	if lastExited != 9 {
		t.Fatalf("lastExitedSeq = %d, want 9", lastExited)
	}
	if exitedAt == nil {
		t.Fatal("exitedAt was not set after first handleProcessExit")
	}
	if !cmdNil {
		t.Fatal("agent.cmd should be nil after first handleProcessExit")
	}
	if len(cleaner.removeGeneration) != 1 {
		t.Fatalf("session cleanup calls = %d, want 1", len(cleaner.removeGeneration))
	}

	// 同一 (agentID, launchSeq) 的重复 handleProcessExit 必须被栅栏挡住。
	// 不应发生额外 session cleanup、重复迁移或 lastExitedSeq 改写。
	svc.handleProcessExit(context.Background(), "agent-5", 9, errors.New("duplicate"))
	if len(cleaner.removeGeneration) != 1 {
		t.Fatalf("session cleanup calls after duplicate = %d, want 1 (fence broken)", len(cleaner.removeGeneration))
	}
	svc.mu.RLock()
	dupSeq := agent.lastExitedSeq
	svc.mu.RUnlock()
	if dupSeq != 9 {
		t.Fatalf("lastExitedSeq after duplicate = %d, want 9", dupSeq)
	}

	// 旧 seq 调用也必须是 no-op。
	svc.handleProcessExit(context.Background(), "agent-5", 5, nil)
	if len(cleaner.removeGeneration) != 1 {
		t.Fatalf("session cleanup calls after stale = %d, want 1", len(cleaner.removeGeneration))
	}
}

// -----------------------------------------------------------------------------
// monitor 单元层：Drain 拒绝后续 Arm，并等待进行中的 wait
// -----------------------------------------------------------------------------

func TestExitMonitorDrainClosesGate(t *testing.T) {
	t.Parallel()
	m := exitmonitor.New(silentLogger())

	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start(): %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := m.Drain(drainCtx); err != nil {
			t.Fatalf("Drain cleanup err = %v", err)
		}
	})

	m.Arm(exitmonitor.Target{AgentID: "a", LaunchSeq: 1, Cmd: cmd})

	// 在 goroutine 中 Drain，方便测试观察 gate 翻转。
	drainDone := make(chan error, 1)
	go func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		drainDone <- m.Drain(drainCtx)
	}()

	// 结束进程，让 Drain 可以收束。
	_ = cmd.Process.Kill()

	if err := <-drainDone; err != nil {
		t.Fatalf("Drain err = %v, want nil", err)
	}

	// Drain 后 Arm 必须拒绝新的目标。
	other := newLongRunningTestCommand()
	if err := other.Start(); err != nil {
		t.Fatalf("cmd.Start(): %v", err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	if armed := m.Arm(exitmonitor.Target{AgentID: "b", LaunchSeq: 1, Cmd: other}); armed {
		t.Fatal("Arm after Drain must return false")
	}
}

// -----------------------------------------------------------------------------
// stopTestSessionCleaner 由 stop_test.go 提供，本文件仅通过 newP3TestService 间接引用。
// -----------------------------------------------------------------------------

var _ sync.Locker = (*sync.Mutex)(nil) // keep sync import stable for future helpers
