package orchestration

import (
	"context"
	"errors"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"testing"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// durableRecoveryBackend 用确定性CLI接缝验证真实service恢复与下一turn的head接线。
type durableRecoveryBackend struct {
	cliBackendFixture
	reserved  bool
	unsettled bool
}

// Launch 模拟新provider会话，不改写编排agent身份。
func (b *durableRecoveryBackend) Launch(context.Context, LaunchRequest) (LaunchResult, error) {
	if b.reserved {
		return LaunchResult{}, errors.New("CLI launch identity is already reserved; reconcile before retry")
	}
	b.running, b.reserved = true, true
	return LaunchResult{ThreadID: "old-thread"}, nil
}

// TestRecoverDurableTerminalBeforeProjection 复现public stopped先于runtime投影并完整恢复后提交新turn。
func TestRecoverDurableTerminalBeforeProjection(t *testing.T) {
	t.Run("old-turn-outbox", TestCompletedTurnOutboxAfterProcessCleanup)
	t.Run("head-binding-failure", testRecoverHeadBindingFailure)
	t.Run("foreign-terminal", testRecoverForeignTerminal)
	t.Run("already-projected", testRecoverProjectedProcess)
	t.Run("failed-turn", testRecoverPendingFailedTurn)
	t.Run("unsettled-reservation", testRecoverUnsettledReservation)
	t.Run("adoption-failure", func(t *testing.T) { testRecoveryAdoptionFailure(t, false) })
	t.Run("adoption-and-write-failure", func(t *testing.T) { testRecoveryAdoptionFailure(t, true) })
	svc, agent := pendingProcessRecoveryFixture(t)
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatal(err)
	}
	if agent.state != agentdto.StateIdle || agent.sessionGeneration != agent.launchSeq {
		t.Fatalf("recovered runtime=%s generation=%d launch=%d", agent.state, agent.sessionGeneration, agent.launchSeq)
	}
	head, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if head.State != "active" || head.Generation != agent.launchSeq || head.PublicThreadID != "old-thread" {
		t.Fatalf("recovered head: %+v", head)
	}
	assertRecoveredNewTurn(t, svc, agent)
}

// assertRecoveredNewTurn 验证恢复会话提交新turn后仍能ACK旧process且不回写新runtime。
func assertRecoveredNewTurn(t *testing.T, svc *service, agent *agentRuntime) {
	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, ThreadID: agent.threadID, Inputs: []shareddto.InputItem{{Type: "text", Content: "after recovery"}}}); err != nil {
		t.Fatal(err)
	}
	head, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if head.ProviderTurnID != "cli-turn" || head.Generation != agent.launchSeq {
		t.Fatalf("new turn head: %+v", head)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "old-process", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	if agent.activeTurnID != "cli-turn" {
		t.Fatal("old process projection changed recovered turn")
	}
}

// pendingProcessRecoveryFixture 真实提交process终态但不消费outbox，保留stopping/public stopped分裂。
func pendingProcessRecoveryFixture(t *testing.T) (*service, *agentRuntime) {
	svc, agent := durableRecoveryRuntimeFixture(t)
	if _, err := svc.archiveAgentViaLauncher(context.Background(), agent.id, "stop"); err != nil {
		t.Fatal(err)
	}
	snapshot := AgentSnapshot{AgentID: agent.id, State: string(agent.state)}
	if err := svc.overlayPublicTerminalOutcome(context.Background(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if agent.state != agentdto.StateStopping || snapshot.State != "stopped" {
		t.Fatalf("split not reproduced runtime=%s public=%s", agent.state, snapshot.State)
	}
	return svc, agent
}

// durableRecoveryRuntimeFixture 建立真实idle session head并接入可恢复CLI后端。
func durableRecoveryRuntimeFixture(t *testing.T) (*service, *agentRuntime) {
	svc, _ := newTerminalOutcomeTestService(t)
	svc.lifecycle.sessionCleaner = &stopTestSessionCleaner{}
	backend := &durableRecoveryBackend{cliBackendFixture: cliBackendFixture{running: true}, reserved: true}
	launcher := &cliLauncher{backend: backend}
	svc.lifecycle.launcher = launcher
	svc.lifecycle.recovery.launcher = launcher
	svc.turns.launcher = launcher

	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.provider, agent.providerSource = "codex", "inferred"
	agent.threadID, agent.remoteThreadID = "old-thread", "old-thread"
	agent.launchSeq = 1
	agent.cwd = t.TempDir()
	agent.env = []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=test"}
	svc.registry.agents[agent.id] = agent
	if err := svc.BindSessionGeneration(context.Background(), agent.id, 1); err != nil {
		t.Fatal(err)
	}

	return svc, agent
}

// failingRecoveryHeadStore 保持读取真实durable状态，只拒绝新head激活。
type failingRecoveryHeadStore struct {
	contract.TerminalOutcomeCommitPort
	err error
}

// ActivateTerminalOutcomeHead 为恢复绑定注入明确的持久化失败。
func (s failingRecoveryHeadStore) ActivateTerminalOutcomeHead(context.Context, contract.TerminalOutcomeHeadActivation) (contract.TerminalOutcomeHead, error) {
	return contract.TerminalOutcomeHead{}, s.err
}

// testRecoverHeadBindingFailure 验证新head失败不发布idle且停止刚启动的provider。
func testRecoverHeadBindingFailure(t *testing.T) {
	svc, agent := pendingProcessRecoveryFixture(t)
	failure := errors.New("new head unavailable")
	svc.terminalOutcomes = failingRecoveryHeadStore{TerminalOutcomeCommitPort: svc.terminalOutcomes, err: failure}
	if err := svc.Recover(context.Background(), agent.id); !errors.Is(err, failure) {
		t.Fatalf("bind error=%v", err)
	}
	if agent.state != agentdto.StateFailed || svc.lifecycle.launcher.IsRunning(context.Background(), agent) {
		t.Fatalf("failed bind left runtime available: %s", agent.state)
	}
	head, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if head.State != "terminal" || head.Generation != 1 {
		t.Fatalf("failed bind changed old head: %+v", head)
	}
}

// testRecoverForeignTerminal 拒绝错位thread，不能用外部durable终态推进当前runtime。
func testRecoverForeignTerminal(t *testing.T) {
	svc, agent := pendingProcessRecoveryFixture(t)
	agent.remoteThreadID = "foreign"
	if err := svc.Recover(context.Background(), agent.id); err == nil {
		t.Fatal("foreign terminal accepted")
	}
	if agent.state != agentdto.StateStopping || agent.launchSeq != 1 {
		t.Fatal("foreign terminal mutated recovery")
	}
}

// testRecoverProjectedProcess 独立锁定已投影stop之后完整Recover遗漏generation绑定的第二根因。
func testRecoverProjectedProcess(t *testing.T) {
	svc, agent := pendingProcessRecoveryFixture(t)
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "before-recover", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatal(err)
	}
	if agent.sessionGeneration != agent.launchSeq {
		t.Fatalf("recovery omitted generation binding: generation=%d launch=%d", agent.sessionGeneration, agent.launchSeq)
	}
}

// testRecoverPendingFailedTurn 失败turn先公开failed时仅投影其失败真相再恢复。
func testRecoverPendingFailedTurn(t *testing.T) {
	svc, agent := durableRecoveryRuntimeFixture(t)
	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, ThreadID: agent.threadID, Inputs: []shareddto.InputItem{{Type: "text", Content: "fails"}}}); err != nil {
		t.Fatal(err)
	}
	template := terminalOutcomeEvent(false)
	ev := turndto.TurnCompleted{TurnHeader: template.TurnHeader, Success: false, Status: "failed"}
	ev.AgentID, ev.ThreadID, ev.TurnID = agent.id, agent.threadID, agent.activeTurnID
	terminal, err := turndto.NewTurnTerminalV2(ev, "failed-recovery-terminal")
	if err != nil {
		t.Fatal(err)
	}
	ev, err = turndto.AttachCanonicalTurnTerminal(ev, terminal)
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err != nil || !handled {
		t.Fatalf("failure commit: %v", err)
	}
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatal(err)
	}
	if agent.state != agentdto.StateIdle || agent.sessionGeneration != agent.launchSeq {
		t.Fatalf("failed-turn recovery incomplete: state=%s generation=%d launch=%d", agent.state, agent.sessionGeneration, agent.launchSeq)
	}
}

// Stop 模拟Execution删除已settled session后的严格缺失语义，不把重复Stop当成功。
func (b *durableRecoveryBackend) Stop(context.Context, string) error {
	if !b.reserved {
		return contract.ErrAgentNotFound
	}
	b.running = false
	if !b.unsettled {
		b.reserved = false
	}
	return nil
}

// Archive 与真实Execution一样在进程闭合后回收reservation。
func (b *durableRecoveryBackend) Archive(ctx context.Context, id string) error {
	return b.Stop(ctx, id)
}

// testRecoverUnsettledReservation 非活体不代表旧reservation可重用，Launch必须原样拒绝。
func testRecoverUnsettledReservation(t *testing.T) {
	svc, agent := pendingProcessRecoveryFixture(t)
	backend := svc.lifecycle.launcher.(*cliLauncher).backend.(*durableRecoveryBackend)
	backend.reserved, backend.unsettled = true, true
	if err := svc.Recover(context.Background(), agent.id); err == nil {
		t.Fatal("unsettled reservation was reused")
	}
	if !backend.reserved || backend.running || agent.state != agentdto.StateFailed {
		t.Fatal("failed recovery cleared unsettled reservation or published active runtime")
	}
}

// Recover 模拟同线程native恢复，不创建新binding或调用普通Launch。
func (b *durableRecoveryBackend) Recover(ctx context.Context, activation PersistedRuntimeActivation) (LaunchResult, error) {
	if b.reserved {
		return LaunchResult{}, errors.New("CLI launch identity is already reserved; reconcile before retry")
	}
	if _, ok := contract.LaunchSeqFromContext(ctx); !ok {
		return LaunchResult{}, errors.New("missing recovery generation")
	}
	b.running, b.reserved = true, true
	return LaunchResult{ThreadID: activation.ThreadID, RemoteAgentID: activation.AgentID}, nil
}

// recoveryFailureWritePort 注入新启动失败记录不可用，验证不得清除新围栏。
type recoveryFailureWritePort struct {
	contract.TerminalOutcomeCommitPort
}

func (recoveryFailureWritePort) CommitTerminalOutcome(context.Context, contract.TerminalOutcomeCommit) (contract.TerminalOutcomeCommitResult, error) {
	return contract.TerminalOutcomeCommitResult{}, errors.New("failure write unavailable")
}

// testRecoveryAdoptionFailure 真实head已提交后验证状态机失败投影、backend清理及失败写不可用分支。
func testRecoveryAdoptionFailure(t *testing.T, writeFails bool) {
	svc, agent := pendingProcessRecoveryFixture(t)
	reader := svc.terminalHeadReader
	backend := svc.lifecycle.launcher.(*cliLauncher).backend.(*durableRecoveryBackend)
	svc.lifecycle.recovery.launchCommit = recoveryCommitAdoptionFailure{recoveryLaunchCommitPort: svc.lifecycle.recovery.launchCommit, svc: svc, writeFails: writeFails}

	if err := svc.Recover(context.Background(), agent.id); err == nil {
		t.Fatal("adoption failure returned success")
	}
	if agent.threadID != "old-thread" || agent.sessionGeneration != 2 || backend.running {
		t.Fatal("committed identity was cleared or backend left running")
	}
	head, err := reader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveryAdoptionFailureState(t, agent, head, writeFails)
}

// assertRecoveryAdoptionFailureState 失败记录成功时收口failed，否则保留新active head和不可用recovering状态。
func assertRecoveryAdoptionFailureState(t *testing.T, agent *agentRuntime, head contract.DurableTerminalOutcomeHead, writeFails bool) {
	state, headState := agentdto.StateFailed, "terminal"
	if writeFails {
		state, headState = agentdto.StateRecovering, "active"
	}
	if agent.state != state || head.State != headState || head.Generation != 2 || head.PublicThreadID != "old-thread" {
		t.Fatalf("adoption failure state=%s head=%+v", agent.state, head)
	}
}

// recoveryCommitAdoptionFailure 在head已提交后拒绝启动状态发布，不绕过真实失败projector。
type recoveryCommitAdoptionFailure struct {
	recoveryLaunchCommitPort
	svc        *service
	writeFails bool
}

func (p recoveryCommitAdoptionFailure) commitLaunchSuccessLocked(context.Context, *agentRuntime) error {
	if p.writeFails {
		p.svc.terminalOutcomes = recoveryFailureWritePort{p.svc.terminalOutcomes}
	}
	return errors.New("adoption state write failed")
}
