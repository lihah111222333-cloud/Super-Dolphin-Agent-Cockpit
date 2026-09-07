package standalone

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/codexwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// ActivatePersistedRuntime 在 daemon 重启后用持久 thread 身份重建 Codex app-server session。
// 只接受 orchestration 从可信 binding 生成的 activation，不接受客户端自带 thread id。
func (e *Execution) ActivatePersistedRuntime(ctx context.Context, activation orchestration.PersistedRuntimeActivation) error {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(activation.Provider), "codex") {
		return unsupportedCLICapability(providerdto.CapThreadResume, strings.TrimSpace(activation.Provider))
	}
	if strings.TrimSpace(activation.AgentID) == "" || strings.TrimSpace(activation.ThreadID) == "" {
		return errors.New("persisted runtime activation requires agent and thread identity")
	}
	if err := validateCLIWorkspace(e.cfg.WorkspaceRoot, activation.CWD); err != nil {
		return err
	}
	provider, err := e.configuredProvider("codex")
	if err != nil {
		return err
	}
	if err := e.rejectExistingActivation(ctx, activation.AgentID); err != nil {
		if errors.Is(err, errActivationAlreadyRunning) {
			return nil
		}
		return err
	}
	req := contract.LaunchRequest{AgentID: activation.AgentID, Name: activation.AgentID, Cwd: activation.CWD,
		Env: []string{"AGENT_PROVIDER=codex"}}
	session, err := e.reserveSessionWithContext(ctx, req, provider)
	if err != nil {
		return err
	}
	defer func() {
		e.finishLaunch(session)
		e.launches.Done()
	}()
	launchCtx, cancel := session.bindLaunchContext(ctx)
	defer cancel()
	return e.resumeCodexThread(launchCtx, session, req, provider, activation.ThreadID)
}

var errActivationAlreadyRunning = errors.New("persisted runtime activation already running")

func (e *Execution) rejectExistingActivation(ctx context.Context, agentID string) error {
	existing, lookupErr := e.session(agentID)
	if lookupErr != nil {
		if errors.Is(lookupErr, contract.ErrAgentNotFound) {
			return nil
		}
		return lookupErr
	}
	if e.IsRunning(ctx, agentID) {
		return errActivationAlreadyRunning
	}
	return fmt.Errorf("persisted runtime activation conflicts with existing session: %s", sessionFailure(existing))
}

func (e *Execution) configuredProvider(name string) (Provider, error) {
	for _, provider := range e.cfg.Providers {
		if provider.Name == name {
			return provider, nil
		}
	}
	return Provider{}, fmt.Errorf("CLI provider is not configured: %s", name)
}

func sessionFailure(session *cliSession) string {
	if session == nil {
		return "missing session"
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.failed != nil {
		return session.failed.Error()
	}
	if session.stopped {
		return "session is stopped"
	}
	return "session is not running"
}

// resumeCodexThread 以可信持久 thread 身份重建 Codex 会话并重新绑定退出围栏。
func (e *Execution) resumeCodexThread(ctx context.Context, session *cliSession, req contract.LaunchRequest, provider Provider, threadID string) error {
	process, err := codexwire.StartWithExit(ctx, provider.Executable, req.Cwd, cliEnvironment(provider), e.notificationHandler(session), contract.CLIProcessExitBinding{
		AgentID: req.AgentID, LaunchSeq: session.launchSeq, OnExit: e.processExitHandler(session),
	})
	if err != nil {
		e.failSession(session, err)
		return err
	}
	session.mu.Lock()
	if session.stopped {
		session.mu.Unlock()
		return errors.Join(errors.New("CLI resume cancelled while starting"), process.Close())
	}
	session.process = process
	session.mu.Unlock()
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if recoverArchived, _ := ctx.Value(recoverArchivedThreadKey{}).(bool); recoverArchived {
		if err := prepareArchivedCodexRecovery(ctx, process.Client.CallResult, threadID, req.Cwd); err != nil {
			e.failSession(session, err)
			return errors.Join(err, process.Close())
		}
	}
	err = process.Client.CallResult(ctx, "thread/resume", map[string]any{
		"threadId": threadID, "cwd": req.Cwd, "sandbox": provider.Sandbox, "approvalPolicy": "never",
	}, &result)
	if err == nil {
		err = validateResumedThreadID(result.Thread.ID, threadID)
	}

	if err != nil {
		e.failSession(session, err)
		return errors.Join(err, process.Close())
	}
	session.mu.Lock()
	if session.stopped {
		session.mu.Unlock()
		return errors.Join(errors.New("CLI resume cancelled before activation"), process.Close())
	}
	session.threadID = result.Thread.ID
	if err := process.BindExitFence(req.AgentID, result.Thread.ID, session.launchSeq); err != nil {
		session.mu.Unlock()
		e.failSession(session, err)
		return errors.Join(err, process.Close())
	}
	session.activated = true
	session.mu.Unlock()
	session.signalReady(nil)
	return nil
}

// validateResumedThreadID 要求provider真实resume返回原thread，空值或外部身份均失败。
func validateResumedThreadID(actual, expected string) error {
	if actual == "" || actual != expected {
		return errors.New("Codex thread/resume returned empty or foreign thread id")
	}
	return nil
}
