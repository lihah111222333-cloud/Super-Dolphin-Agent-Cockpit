package standalone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/claudewire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/codexwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

var ErrExecutionNotReady = errors.New("capability_not_supported: requested CLI capability is not enabled")

func unsupportedCLICapability(capability, provider string) error {
	return errors.Join(ErrExecutionNotReady, contract.NewCapabilityError(capability, provider))
}

// Execution 仅拥有 CLI runtime 句柄；逻辑任务、状态机和终态写入仍归 orchestration。
type Execution struct {
	cfg      Config
	mu       sync.Mutex
	sessions map[string]*cliSession
	// nextLaunchSeq is only used when Execution is called directly, outside
	// orchestration's lifecycle owner. Owner-supplied fences always win, while
	// this counter prevents unrelated direct sessions from sharing a constant.
	nextLaunchSeq uint64
	sink          func(context.Context, turndto.TurnTerminalV2)
	receiptSink   func(context.Context, turndto.TurnTerminalV2) error
	exitSink      func(contract.CLIProcessExit)
	closed        bool
	launches      sync.WaitGroup
	store         SessionStore
}

type cliSession struct {
	mu                       sync.Mutex
	process                  *codexwire.Process
	claude                   *claudewire.Process
	threadID, turnID, output string
	request                  contract.LaunchRequest
	provider                 Provider
	failed                   error
	// terminalReceiptErr distinguishes a transient consumer acknowledgement
	// failure from a provider/session failure. The canonical terminal remains
	// pending until the receipt is accepted, after which only this error may be
	// cleared so an unrelated provider failure is never hidden.
	terminalReceiptErr error
	submitting         bool
	completedTurnID    string
	interruptRequestID string
	stopped            bool
	// activated is set only after the provider has returned a usable session
	// identity and the process exit fence is bound. A reservation that fails
	// before that point is intentionally retained: its external side effect is
	// uncertain and must not be retried under the same AgentID.
	activated         bool
	processSettled    bool
	launching         bool
	launchCancel      context.CancelFunc
	launchSeq         uint64
	claudeInitialized bool
	// claudeAttachPending closes the gap between the reader goroutine receiving
	// system/init and launchClaude attaching the returned process handle.
	// Readiness must not be signalled while this fence is set.
	claudeAttachPending bool
	pendingClaudeResult *claudewire.Event
	// pendingTerminal is a single bounded canonical receipt slot. It is claimed
	// outside session.mu before invoking a sink, so re-entrant callbacks cannot
	// deadlock or issue duplicate provider work.
	pendingTerminal *pendingTerminalReceipt
	readyCh         chan struct{}
	readyOnce       sync.Once
	readyErr        error
}

// NewExecution 创建实例内唯一 CLI 句柄表；不读取全局 provider 配置。
func NewExecution(cfg Config) *Execution {
	return &Execution{cfg: cfg, sessions: make(map[string]*cliSession)}
}

// BindTerminalSink 由编排 service 在运行前绑定唯一终态消费者。
func (e *Execution) BindTerminalSink(sink func(context.Context, turndto.TurnTerminalV2)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sink = sink
	e.receiptSink = nil
}

// BindTerminalSinkWithReceipt 绑定可确认接收结果的内部终态消费者。
// 旧 BindTerminalSink 保留给直接调用方；生产编排优先使用此窄口，避免
// provider 已结束但 canonical terminal 被消费者静默丢弃后仍报告成功。
func (e *Execution) BindTerminalSinkWithReceipt(sink func(context.Context, turndto.TurnTerminalV2) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.receiptSink = sink
	e.sink = nil
}

// BindProcessExitSink 绑定受管 CLI 的一次性进程退出通知；消费端应只处理 Unexpected 事件。
func (e *Execution) BindProcessExitSink(sink func(contract.CLIProcessExit)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.exitSink = sink
}

// Launch 使用配置中的官方 CLI 创建真实会话；不执行 LaunchRequest.Command。
func (e *Execution) Launch(ctx context.Context, req contract.LaunchRequest) (orchestration.LaunchResult, error) {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return orchestration.LaunchResult{}, err
	}
	provider, model, err := e.launchProfile(req)
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	if err := e.requireSessionStore(); err != nil {
		return orchestration.LaunchResult{}, err
	}
	session, err := e.reserveSessionWithContext(ctx, req, provider)
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	defer func() {
		e.finishLaunch(session)
		e.launches.Done()
	}()
	ctx, cancel := session.bindLaunchContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return orchestration.LaunchResult{}, err
	}
	var result orchestration.LaunchResult
	if provider.Name == "claude" {
		result, err = e.launchClaude(ctx, session, req, provider, model)
	} else {
		result, err = e.launchCodex(ctx, session, req, provider, model)
	}
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	if err := e.persistSession(ctx, req, result.ThreadID, provider.Name, model); err != nil {
		e.failSession(session, err)
		// Cleanup is an owner-internal action and must not be skipped merely
		// because the request context was canceled after provider startup.
		return orchestration.LaunchResult{}, errors.Join(err, e.stopSession(session))
	}
	return result, nil
}

// stopSession 是 owner 内部清理窄口；即使请求上下文已取消也必须回收进程。
// Public Stop remains context-gated; rollback of a partially published launch must always reclaim
// the process even when its request context is already canceled.
func (e *Execution) stopSession(session *cliSession) error {
	if session == nil {
		return contract.ErrAgentNotFound
	}
	session.stopLaunch()
	session.mu.Lock()
	process := session.process
	claude := session.claude
	session.mu.Unlock()
	var closeErr error
	if claude != nil {
		closeErr = claude.Close()
	} else if process != nil {
		closeErr = process.Close()
	}
	session.mu.Lock()
	if (process != nil || claude != nil) && !runningCLIProcess(process, claude) {
		session.processSettled = true
	}
	session.mu.Unlock()
	// Stop is also used by launcher recovery immediately before a relaunch.
	// Only an activated, fully settled session may release its AgentID; a
	// pre-activation reservation remains a fail-closed tombstone.
	e.reclaimSettledSession(session)
	return closeErr
}

// bindLaunchContext 将启动取消归属当前会话；停止先发生时立即取消，避免发布后遗漏。
func (s *cliSession) bindLaunchContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.launchCancel = cancel
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		cancel()
	}
	return ctx, cancel
}

// stopLaunch 标记停止并在锁外取消握手和持久化，不能持锁等待 provider。
func (s *cliSession) stopLaunch() {
	s.mu.Lock()
	s.stopped = true
	cancel := s.launchCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.signalReady(errors.New("CLI session stopped before readiness"))
}

// launchCodex 完成官方 app-server handshake 和 thread 创建。
func (e *Execution) launchCodex(ctx context.Context, session *cliSession, req contract.LaunchRequest, provider Provider, model string) (orchestration.LaunchResult, error) {
	process, err := codexwire.StartWithExit(ctx, provider.Executable, req.Cwd, cliEnvironment(provider), e.notificationHandler(session), contract.CLIProcessExitBinding{
		AgentID: req.AgentID, LaunchSeq: session.launchSeq, OnExit: e.processExitHandler(session),
	})
	if err != nil {
		e.failSession(session, err)
		return orchestration.LaunchResult{}, err
	}
	session.mu.Lock()
	if session.stopped {
		session.mu.Unlock()
		return orchestration.LaunchResult{}, errors.Join(errors.New("CLI launch cancelled while starting"), process.Close())
	}
	session.process = process
	session.mu.Unlock()
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	err = process.Client.CallResult(ctx, "thread/start", map[string]any{
		"cwd": req.Cwd, "model": model, "approvalPolicy": "never", "sandbox": provider.Sandbox,
		"baseInstructions": req.Instructions,
	}, &result)
	if err == nil && result.Thread.ID == "" {
		err = errors.New("Codex thread/start returned empty thread id")
	}
	if err != nil {
		e.failSession(session, err)
		return orchestration.LaunchResult{}, errors.Join(err, process.Close())
	}
	session.mu.Lock()
	if session.stopped {
		session.mu.Unlock()
		return orchestration.LaunchResult{}, errors.Join(errors.New("CLI launch cancelled before activation"), process.Close())
	}
	session.threadID = result.Thread.ID
	if err := process.BindExitFence(req.AgentID, result.Thread.ID, session.launchSeq); err != nil {
		session.mu.Unlock()
		e.failSession(session, err)
		return orchestration.LaunchResult{}, errors.Join(err, process.Close())
	}
	session.activated = true
	session.mu.Unlock()
	session.signalReady(nil)
	return orchestration.LaunchResult{ThreadID: result.Thread.ID, RemoteAgentID: req.AgentID}, nil
}

// reserveSession 在进程启动前预留唯一身份，失败或未知结果不会盲目重发 launch。
func (e *Execution) reserveSession(req contract.LaunchRequest, provider Provider) (*cliSession, error) {
	return e.reserveSessionWithContext(context.Background(), req, provider)
}

// reserveSessionWithContext 在启动前占用 AgentID，并绑定 owner 或本地 launch fence。
func (e *Execution) reserveSessionWithContext(ctx context.Context, req contract.LaunchRequest, provider Provider) (*cliSession, error) {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || req.AgentID == "" || (e.sink == nil && e.receiptSink == nil) {
		return nil, errors.New("CLI execution requires active owner, agent id and terminal sink")
	}
	if _, exists := e.sessions[req.AgentID]; exists {
		return nil, errors.New("CLI launch identity is already reserved; reconcile before retry")
	}
	launchSeq, err := e.reserveLaunchSequence(ctx)
	if err != nil {
		return nil, err
	}
	session := &cliSession{request: req, provider: provider, launchSeq: launchSeq, readyCh: make(chan struct{}), launching: true}
	e.sessions[req.AgentID] = session
	e.launches.Add(1)
	return session, nil
}

// reserveLaunchSequence 在 owner fence 存在时复用其代际，否则为直接调用分配单调序列。
func (e *Execution) reserveLaunchSequence(ctx context.Context) (uint64, error) {
	launchSeq, ownerFence := contract.LaunchSeqFromContext(ctx)
	if ownerFence {
		if launchSeq > e.nextLaunchSeq {
			e.nextLaunchSeq = launchSeq
		}
	} else {
		if e.nextLaunchSeq == ^uint64(0) {
			return 0, errors.New("CLI launch sequence exhausted")
		}
		e.nextLaunchSeq++
		launchSeq = e.nextLaunchSeq
	}
	if launchSeq == 0 {
		return 0, errors.New("CLI launch sequence is required")
	}
	return launchSeq, nil
}

// finishLaunch closes the in-memory launch window and retries cleanup that may
// have been deferred by a concurrent Stop. It never removes an uncertain
// pre-activation reservation.
func (e *Execution) finishLaunch(session *cliSession) {
	if session == nil {
		return
	}
	session.mu.Lock()
	session.launching = false
	session.mu.Unlock()
	e.reclaimSettledSession(session)
}

// reclaimSettledSession 仅移除生命周期已闭合且进程停止的 provider session；指针检查防止迟到回调删除新会话。
func (e *Execution) reclaimSettledSession(session *cliSession) {
	if session == nil {
		return
	}
	session.mu.Lock()
	activated := session.activated
	processSettled := session.processSettled
	settled := session.stopped || session.failed != nil
	launching := session.launching
	pendingTerminal := session.pendingTerminal != nil
	process, claude := session.process, session.claude
	running := runningCLIProcess(process, claude)
	session.mu.Unlock()
	// A strict receipt failure keeps the session addressable until the retained
	// canonical terminal is acknowledged. Reclaiming here would drop the only
	// in-memory recovery handle even though the provider event is already done.
	if !activated || !processSettled || !settled || launching || running || pendingTerminal {
		return
	}
	e.mu.Lock()
	if current := e.sessions[session.request.AgentID]; current == session {
		// Retire the object before dropping the map reference. Provider readers
		// may still deliver buffered notifications through this exact callback;
		// the existing stopped fence keeps those late events out of the next
		// session's terminal stream.
		session.mu.Lock()
		session.stopped = true
		session.mu.Unlock()
		delete(e.sessions, session.request.AgentID)
	}
	e.mu.Unlock()
}

func runningCLIProcess(process *codexwire.Process, claude *claudewire.Process) bool {
	return (process != nil && process.Running()) || (claude != nil && claude.Running())
}

// processExitHandler 校验真实 session/代际后转发一次性进程退出事件。
func (e *Execution) processExitHandler(session *cliSession) func(contract.CLIProcessExit) {
	return func(event contract.CLIProcessExit) {
		if !event.Valid() || event.AgentID != session.request.AgentID || event.LaunchSeq != session.launchSeq {
			return
		}
		session.mu.Lock()
		currentSessionID := session.threadID
		session.mu.Unlock()
		if currentSessionID == "" || currentSessionID != event.SessionID {
			return
		}
		if event.Unexpected {
			err := event.Err
			if err == nil {
				err = errors.New("CLI process exited unexpectedly")
			}
			e.failSession(session, err)
		}
		session.mu.Lock()
		session.processSettled = true
		session.mu.Unlock()
		// Also retry cleanup for an explicit Stop whose provider callback was
		// re-entrant: Close may return before the reader finishes, so the
		// eventual exit callback is the first point at which Running() is false.
		e.reclaimSettledSession(session)
		e.mu.Lock()
		sink := e.exitSink
		e.mu.Unlock()
		if sink != nil {
			sink(event)
		}
	}
}

// WaitForSessionReady 等待 provider 完成真实握手；Claude 必须先收到匹配的 system/init。
// timeout 是硬截止时间，进程退出、上下文取消或握手失败均 fail-fast。
func (e *Execution) WaitForSessionReady(ctx context.Context, agentID string, timeout time.Duration) error {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return err
	}
	session, err := e.session(agentID)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		return fmt.Errorf("CLI session readiness timeout must be positive: %w", context.DeadlineExceeded)
	}
	if err := session.readyState(); err != nil {
		return err
	}
	session.mu.Lock()
	readyCh, process, claude := session.readyCh, session.process, session.claude
	providerName, initialized, threadID := session.provider.Name, session.claudeInitialized, session.threadID
	session.mu.Unlock()
	if readyCh == nil {
		return readinessWithoutChannel(providerName, initialized, threadID, process, claude)
	}
	return e.waitForReadiness(ctx, session, timeout, readyCh, claude, providerName)
}

// readinessWithoutChannel 处理已完成或缺失 readiness fence 的同步检查。
func readinessWithoutChannel(providerName string, initialized bool, threadID string, process *codexwire.Process, claude *claudewire.Process) error {
	if providerName == "claude" && initialized {
		if claude != nil && claude.Running() {
			return nil
		}
		return errors.New("Claude process exited before system/init")
	}
	if providerName != "claude" && threadID != "" && process != nil && process.Running() {
		return nil
	}
	return errors.New("CLI session readiness fence is unavailable")
}

// waitForReadiness 等待 readiness、退出、取消或硬超时中的首个结果。
func (e *Execution) waitForReadiness(ctx context.Context, session *cliSession, timeout time.Duration, readyCh <-chan struct{}, claude *claudewire.Process, providerName string) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var done <-chan struct{}
	if claude != nil {
		done = claude.Done()
	}
	for {
		select {
		case <-readyCh:
			return e.confirmReadiness(session, providerName)
		case <-done:
			failure := claude.Failure()
			if failure == nil {
				failure = errors.New("Claude process exited before system/init")
			}
			e.failSession(session, failure)
			return failure
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("CLI session readiness timed out after %s: %w", timeout, context.DeadlineExceeded)
		}
	}
}

// confirmReadiness 被 readiness 通知唤醒后再次验证状态与真实进程存活。
func (e *Execution) confirmReadiness(session *cliSession, providerName string) error {
	if err := session.readyState(); err != nil {
		return err
	}
	session.mu.Lock()
	currentClaude := session.claude
	running := providerName != "claude" || (currentClaude != nil && currentClaude.Running())
	session.mu.Unlock()
	if running {
		return nil
	}
	if providerName == "claude" {
		return errors.New("Claude process exited before system/init")
	}
	return errors.New("Codex process exited before session readiness")
}

// readyState 返回握手错误；调用方随后仍需检查底层进程存活。
func (s *cliSession) readyState() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readyErr != nil {
		return s.readyErr
	}
	if s.failed != nil {
		return s.failed
	}
	if s.provider.Name == "claude" {
		return s.claudeReadyState()
	}
	return s.codexReadyState()
}

func (s *cliSession) claudeReadyState() error {
	if !s.claudeInitialized || s.claudeAttachPending {
		return nil
	}
	if s.claude == nil || !s.claude.Running() {
		return errors.New("Claude process exited before system/init")
	}
	return nil
}

func (s *cliSession) codexReadyState() error {
	if s.threadID != "" && s.process != nil && s.process.Running() {
		return nil
	}
	return nil
}

func (s *cliSession) signalReady(err error) {
	s.mu.Lock()
	if err != nil && s.readyErr == nil {
		s.readyErr = err
	}
	readyCh := s.readyCh
	s.mu.Unlock()
	if readyCh != nil {
		s.readyOnce.Do(func() { close(readyCh) })
	}
}

// launchProfile 校验 workspace 与显式模型范围，不按 PATH 或默认模型降级。
func (e *Execution) launchProfile(req contract.LaunchRequest) (Provider, string, error) {
	if err := validateCLIWorkspace(e.cfg.WorkspaceRoot, req.Cwd); err != nil {
		return Provider{}, "", err
	}
	name, model := launchEnvironmentValue(req.Env, "AGENT_PROVIDER"), launchEnvironmentValue(req.Env, "AGENT_MODEL")
	for _, provider := range e.cfg.Providers {
		if provider.Name != name {
			continue
		}
		if provider.Name != "codex" && provider.Name != "claude" {
			return Provider{}, "", ErrExecutionNotReady
		}
		if !slices.Contains(provider.Models, model) {
			return Provider{}, "", fmt.Errorf("selected model must be explicitly configured for %s", name)
		}
		return provider, model, nil
	}
	return Provider{}, "", errors.New("CLI provider is not configured")
}

// validateCLIWorkspace 按实际目录解析别名，拒绝指向工作区外的符号链接及不存在目录。
// 此启动前校验不是 OS sandbox，不能作为并发恶意路径替换的完整隔离证据。
func validateCLIWorkspace(root, cwd string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(cwd) {
		return errors.New("CLI workspace and cwd must be absolute")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return errors.New("CLI workspace cannot be resolved")
	}
	resolvedCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return errors.New("CLI cwd cannot be resolved")
	}
	info, err := os.Stat(resolvedCWD)
	if err != nil || !info.IsDir() {
		return errors.New("CLI cwd must be an existing directory")
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCWD)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("CLI launch cwd is outside configured workspace")
	}
	return nil
}

// failSession 保存启动或协议故障，不把未知状态恢复为可启动。
func (e *Execution) failSession(session *cliSession, err error) {
	if err == nil {
		return
	}
	session.mu.Lock()
	session.failed = err
	session.submitting = false
	session.mu.Unlock()
	session.signalReady(err)
	// A failure may be reported after the provider has already settled (for
	// example, an asynchronous protocol/exit callback). ReclaimSettledSession
	// rechecks process and launch fences, so calling it here cannot discard an
	// active or uncertain reservation.
	e.reclaimSettledSession(session)
}

func launchEnvironmentValue(env []string, name string) string {
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == name {
			return value
		}
	}
	return ""
}
