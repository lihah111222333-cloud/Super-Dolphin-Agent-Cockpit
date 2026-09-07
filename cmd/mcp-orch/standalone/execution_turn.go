package standalone

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/codexwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// SubmitTurn 向真实 CLI thread 提交文本，并保留服务侧的 turn fence。
func (e *Execution) SubmitTurn(ctx context.Context, sub contract.TurnSubmission) (string, error) {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return "", err
	}
	input, err := codexInput(sub)
	if err != nil {
		return "", err
	}
	session, err := e.session(sub.AgentID)
	if err != nil {
		return "", err
	}
	if session.provider.Name == "claude" {
		return e.submitClaude(ctx, session, sub)
	}
	return e.submitCodex(ctx, session, input)
}

// submitCodex 固定会话指针执行真实 RPC，避免投递期间代际替换。
func (e *Execution) submitCodex(ctx context.Context, session *cliSession, input []map[string]string) (string, error) {
	process, threadID, err := session.beginTurn()
	if err != nil {
		return "", err
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := process.Client.CallResult(ctx, "turn/start", map[string]any{"threadId": threadID, "input": input}, &result); err != nil {
		e.failSession(session, err)
		return "", err
	}
	if result.Turn.ID == "" {
		err := errors.New("Codex turn/start returned empty turn id")
		e.failSession(session, err)
		return "", err
	}
	session.mu.Lock()
	session.submitting = false
	if session.completedTurnID != result.Turn.ID {
		session.turnID = result.Turn.ID
	}
	session.mu.Unlock()
	return result.Turn.ID, nil
}

// beginTurn 在一个锁内冻结投递窗口，拒绝停止、故障及重入请求。
func (s *cliSession) beginTurn() (*codexwire.Process, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil || s.stopped || s.turnID != "" || s.process == nil || s.submitting {
		return nil, "", errors.New("CLI session is unavailable or busy")
	}
	s.output, s.interruptRequestID = "", ""
	s.submitting = true
	return s.process, s.threadID, nil
}

// codexInput 拒绝尚未实现的多模态/skill/schema 投递，不静默丢字段。
func codexInput(sub contract.TurnSubmission) ([]map[string]string, error) {
	if len(sub.SelectedSkills) != 0 || sub.ManualSkillSelection || len(sub.OutputSchema) != 0 {
		return nil, ErrExecutionNotReady
	}
	input := make([]map[string]string, 0, len(sub.Inputs))
	for _, item := range sub.Inputs {
		if item.Type != "text" || item.Content == "" {
			return nil, errors.New("standalone CLI currently requires nonempty text input")
		}
		input = append(input, map[string]string{"type": "text", "text": item.Content})
	}
	if len(input) == 0 {
		return nil, errors.New("CLI turn input is required")
	}
	return input, nil
}

// Stop 关闭实际 CLI runtime；预留但未创建进程的失败项无需发出信号。
func (e *Execution) Stop(ctx context.Context, agentID string) error {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return err
	}
	session, err := e.session(agentID)
	if err != nil {
		return err
	}
	return e.stopSession(session)
}

// Archive 先请求 provider 归档，再关闭其受管 CLI 进程。
func (e *Execution) Archive(ctx context.Context, agentID string) error {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return err
	}
	session, err := e.session(agentID)
	if err != nil {
		return err
	}
	if session.provider.Name == "claude" {
		return unsupportedCLICapability(providerdto.CapThreadArchive, session.provider.Name)
	}
	session.mu.Lock()
	process, threadID := session.process, session.threadID
	session.stopped = true
	session.mu.Unlock()
	if process == nil || threadID == "" {
		return errors.New("CLI session has no thread to archive")
	}
	var result map[string]any
	err = process.Client.CallResult(ctx, "thread/archive", map[string]string{"threadId": threadID}, &result)
	return errors.Join(err, process.Close())
}

// Interrupt 对当前 provider turn 发出真实中断，终态由通知回传。
func (e *Execution) Interrupt(ctx context.Context, agentID, _ string) error {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return err
	}
	session, err := e.session(agentID)
	if err != nil {
		return err
	}
	process, ref, err := session.captureInterrupt(nil, 0)
	if err != nil {
		return err
	}
	return interruptCapturedTurn(ctx, process, ref)
}

// IsRunning 同时检查已激活 thread 和真实进程，不根据旧 metadata 推断存活。
func (e *Execution) IsRunning(ctx context.Context, agentID string) bool {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return false
	}
	session, err := e.session(agentID)
	if err != nil {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.claude != nil {
		return !session.stopped && session.claude.Running()
	}
	return session.threadID != "" && !session.stopped && session.process != nil && session.process.Running()
}

// Fork 从可信父 session 的 Codex thread 创建独立 child session，不以新空会话冒充分叉。
func (e *Execution) Fork(ctx context.Context, parentAgentID string, req contract.LaunchRequest) (orchestration.LaunchResult, error) {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return orchestration.LaunchResult{}, err
	}
	provider, model, err := e.launchProfile(req)
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	if provider.Name != "codex" {
		return orchestration.LaunchResult{}, unsupportedCLICapability(providerdto.CapThreadFork, provider.Name)
	}
	if err := e.requireSessionStore(); err != nil {
		return orchestration.LaunchResult{}, err
	}
	parent, err := e.session(parentAgentID)
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	parentThread, err := parent.forkSourceThread()
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	child, err := e.reserveSessionWithContext(ctx, req, provider)
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	defer func() {
		e.finishLaunch(child)
		e.launches.Done()
	}()
	launchCtx, cancel := child.bindLaunchContext(ctx)
	defer cancel()
	result, err := e.launchCodexFork(launchCtx, child, req, provider, model, parentThread)
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	if err := e.persistSession(launchCtx, req, result.ThreadID, provider.Name, model); err != nil {
		e.failSession(child, err)
		return orchestration.LaunchResult{}, errors.Join(err, e.stopSession(child))
	}
	return result, nil
}

// forkSourceThread 在父 session 锁内冻结 source identity，禁止从进行中的 turn 分叉。
func (s *cliSession) forkSourceThread() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider.Name != "codex" || s.process == nil || s.threadID == "" || s.stopped || s.failed != nil {
		return "", errors.New("Codex parent session is unavailable for fork")
	}
	if s.turnID != "" || s.submitting {
		return "", errors.New("Codex parent session must be idle before fork")
	}
	return s.threadID, nil
}

// launchCodexFork 启动独立 app-server，并要求 provider 返回非空 child thread identity。
func (e *Execution) launchCodexFork(ctx context.Context, session *cliSession, req contract.LaunchRequest, provider Provider, model, parentThread string) (orchestration.LaunchResult, error) {
	process, err := e.startCodexForkProcess(ctx, session, req, provider)
	if err != nil {
		e.failSession(session, err)
		return orchestration.LaunchResult{}, err
	}
	if err := attachCodexForkProcess(session, process); err != nil {
		return orchestration.LaunchResult{}, errors.Join(err, process.Close())
	}
	result, err := requestCodexFork(ctx, process, req, provider, model, parentThread)
	if err != nil {
		e.failSession(session, err)
		return orchestration.LaunchResult{}, errors.Join(err, process.Close())
	}
	if err := activateCodexForkSession(session, process, req.AgentID, result.Thread.ID); err != nil {
		e.failSession(session, err)
		return orchestration.LaunchResult{}, errors.Join(err, process.Close())
	}
	session.signalReady(nil)
	return orchestration.LaunchResult{ThreadID: result.Thread.ID, RemoteAgentID: req.AgentID}, nil
}

func (e *Execution) startCodexForkProcess(ctx context.Context, session *cliSession, req contract.LaunchRequest, provider Provider) (*codexwire.Process, error) {
	return codexwire.StartWithExit(ctx, provider.Executable, req.Cwd, cliEnvironment(provider), e.notificationHandler(session), contract.CLIProcessExitBinding{
		AgentID: req.AgentID, LaunchSeq: session.launchSeq, OnExit: e.processExitHandler(session),
	})
}

func attachCodexForkProcess(session *cliSession, process *codexwire.Process) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.stopped {
		return errors.New("CLI fork cancelled while starting")
	}
	session.process = process
	return nil
}

// requestCodexFork 以显式工作目录、模型、审批策略、沙箱和基础指令调用官方 thread/fork。
// 它拒绝空子线程 ID，并在响应携带来源 ID 时校验父线程绑定。
func requestCodexFork(ctx context.Context, process *codexwire.Process, req contract.LaunchRequest, provider Provider, model, parentThread string) (struct {
	Thread struct {
		ID           string `json:"id"`
		ForkedFromID string `json:"forkedFromId"`
	} `json:"thread"`
}, error) {
	var result struct {
		Thread struct {
			ID           string `json:"id"`
			ForkedFromID string `json:"forkedFromId"`
		} `json:"thread"`
	}
	err := process.Client.CallResult(ctx, "thread/fork", map[string]any{
		"threadId": parentThread, "cwd": req.Cwd, "model": model,
		"approvalPolicy": "never", "sandbox": provider.Sandbox, "baseInstructions": req.Instructions,
	}, &result)
	if err == nil && result.Thread.ID == "" {
		err = errors.New("Codex thread/fork returned empty child thread id")
	}
	if err == nil && result.Thread.ForkedFromID != "" && result.Thread.ForkedFromID != parentThread {
		err = errors.New("Codex thread/fork returned a foreign source thread")
	}
	return result, err
}

func activateCodexForkSession(session *cliSession, process *codexwire.Process, agentID, threadID string) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.stopped {
		return errors.New("CLI fork cancelled before activation")
	}
	if err := process.BindExitFence(agentID, threadID, session.launchSeq); err != nil {
		return err
	}
	session.threadID = threadID
	session.activated = true
	return nil
}

// Close 冻结新请求并逐一关闭本实例拥有的 CLI runtime。
func (e *Execution) Close() error {
	e.mu.Lock()
	e.closed = true
	sessions := make([]*cliSession, 0, len(e.sessions))
	for _, session := range e.sessions {
		sessions = append(sessions, session)
	}
	e.mu.Unlock()
	for _, session := range sessions {
		session.stopLaunch()
	}
	e.launches.Wait()
	var result error
	for _, session := range sessions {
		session.mu.Lock()
		process := session.process
		claude := session.claude
		session.mu.Unlock()
		if process != nil {
			result = errors.Join(result, process.Close())
		}
		if claude != nil {
			result = errors.Join(result, claude.Close())
		}
	}
	return result
}

func (e *Execution) session(agentID string) (*cliSession, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session := e.sessions[agentID]
	if session == nil {
		return nil, contract.ErrAgentNotFound
	}
	return session, nil
}

// cliEnvironment 保留 provider 自身环境，剥离 V3、数据库和父进程的 Git 仓库上下文。
func cliEnvironment(provider Provider) []string {
	var result []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if isGitWorkspaceEnvironment(key) {
			continue
		}
		if strings.HasPrefix(key, "GO_AGENT_") || strings.HasPrefix(key, "SUPER_DOLPHIN_") || key == "CODEX_HOME" || key == "CLAUDE_CONFIG_DIR" || key == "PROJECT_ROOT" {
			continue
		}
		result = append(result, entry)
	}
	if provider.Name == "claude" && provider.AuthMode == ClaudeAuthHostDefault {
		return contract.ScrubDatabaseEnv(result)
	}
	name := "CODEX_HOME"
	if provider.Name == "claude" {
		name = "CLAUDE_CONFIG_DIR"
	}
	return append(contract.ScrubDatabaseEnv(result), name+"="+provider.Home)
}

// isGitWorkspaceEnvironment 剥离 Git 局部上下文：单设 cwd 不能阻止继承变量将命令路由到另一仓库。
// 列表由真实 Git 的 --local-env-vars 回归测试约束；认证、作者等非仓库定位环境保持原样。
func isGitWorkspaceEnvironment(key string) bool {
	if runtime.GOOS == "windows" {
		key = strings.ToUpper(key)
	}
	switch key {
	case "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
		"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
		"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX", "GIT_SHALLOW_FILE", "GIT_COMMON_DIR":
		return true
	}
	return strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_")
}
