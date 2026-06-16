package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

// Fork 从当前 provider 历史分出一个新 thread。
// 它复用旧 thread 的 prompt snapshot，再接上新的 provider session；不要重新跑 start 路由。
func (s *service) Fork(ctx context.Context, threadID string) (ForkResult, error) {
	ctx = kernel.NonNilContext(ctx)
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return ForkResult{}, err
	}
	meta, provider, cwd, err := s.resolveForkContext(ctx, threadID, binding.Provider, binding.Cwd)
	if err != nil {
		return ForkResult{}, err
	}
	result, err := session.ForkThread(ctx, dto.ForkRequest{ThreadID: historyTargetID(binding, threadID)})
	if err != nil {
		return ForkResult{}, err
	}
	displayName := continuationName(strings.TrimSpace(meta.Name))
	newThreadID := strings.TrimSpace(result.NewThreadID)
	if newThreadID == "" {
		return ForkResult{}, errors.New("fork thread id is required")
	}
	snapshot, err := s.resolveStablePromptSnapshot(ctx, threadID, provider, contract.PromptAssemblySnapshot{})
	if err != nil {
		return ForkResult{}, err
	}
	agentID := newThreadID
	if err := s.launchAgent(
		ctx,
		agentID,
		cwd,
		displayName,
		meta.ParentAgentID,
		meta.AgentType,
		meta.AgentMemoryScope,
		provider,
		meta.Model,
	); err != nil {
		return ForkResult{}, err
	}
	forkedSession, err := s.resumeForkSession(ctx, ResumeRequest{
		Provider:       provider,
		AgentID:        agentID,
		ThreadID:       newThreadID,
		CWD:            cwd,
		Model:          meta.Model,
		PromptSnapshot: snapshot,
	})
	if err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	if err := s.bindSessionGeneration(ctx, agentID); err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	providerThreadID := resolvedProviderUUID(forkedSession)
	if err := s.persistThreadState(ctx, newThreadState(threadStateForkKind, threadStateFields{
		PublicThreadID:   newThreadID,
		ProviderThreadID: providerThreadID,
		OwnerThreadID:    historyTargetID(binding, threadID),
		AgentID:          agentID,
		ParentAgentID:    meta.ParentAgentID,
		AgentType:        meta.AgentType,
		AgentMemoryScope: meta.AgentMemoryScope,
		Provider:         provider,
		CWD:              cwd,
		Model:            meta.Model,
		Name:             displayName,
		Prompt:           displayName,
		RolloutPath:      forkedSession.RolloutPath(),
		SessionUUID:      resolvedProviderUUID(forkedSession),
		CreatedAt:        time.Now().Unix(),
	}), true); err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	return ForkResult{
		NewThreadID: newThreadID,
		ForkedFrom:  bindingPublicThreadID(binding, threadID),
	}, nil
}

// resolveForkContext 只从 thread meta 和 binding 取 provider/cwd。
// fork 不猜默认 provider；cwd 冲突时直接返回错误。
func (s *service) resolveForkContext(ctx context.Context, threadID, bindingProvider, bindingCWD string) (threadMeta, string, string, error) {
	meta := s.lookupThreadMeta(ctx, threadID)
	cwd, err := resolveForkCWD(meta.CWD, bindingCWD)
	if err != nil {
		return threadMeta{}, "", "", err
	}
	provider := strings.TrimSpace(bindingProvider)
	if provider == "" {
		return threadMeta{}, "", "", errors.New("fork provider is required")
	}
	return meta, provider, cwd, nil
}

// Recover 重新接上 binding 指向的 provider session。
// 它复用 thread meta、runtime config 和已有 snapshot，只刷新 binding/thread 状态。
func (s *service) Recover(ctx context.Context, threadID string) (RecoverResult, error) {
	ctx = kernel.NonNilContext(ctx)
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return RecoverResult{}, err
	}
	meta := s.lookupThreadMeta(ctx, threadID)
	displayName := strings.TrimSpace(meta.Name)
	agentID := strings.TrimSpace(binding.AgentID)
	provider := strings.TrimSpace(binding.Provider)
	publicThreadID := bindingPublicThreadID(binding, threadID)
	providerThreadID := recoverableBindingProviderThreadID(binding)
	recoverCWD, err := resolveRecoverCWD(meta.CWD, binding.Cwd)
	if err != nil {
		return RecoverResult{}, err
	}
	if err := s.requireRecoverProviderSession(agentID, publicThreadID, providerThreadID); err != nil {
		return RecoverResult{}, err
	}
	mode := "restore_launch"
	if err := s.recoverAgent(
		ctx,
		strings.TrimSpace(binding.AgentID),
		recoverCWD,
		displayName,
		meta.ParentAgentID,
		meta.AgentType,
		meta.AgentMemoryScope,
		provider,
		meta.Model,
	); err != nil {
		return RecoverResult{}, err
	}
	mode, err = s.ensureRecoveredSession(ctx, binding.AgentID, provider, agentID, publicThreadID, providerThreadID)
	if err != nil {
		return RecoverResult{}, err
	}
	session, err := s.lookupSession(agentID)
	if err != nil {
		return RecoverResult{}, err
	}
	if err := s.persistThreadState(ctx, newThreadState(threadStateRecoverKind, threadStateFields{
		RequestedThreadID: threadID,
		PublicThreadID:    publicThreadID,
		ProviderThreadID:  kernel.FirstNonEmpty(providerThreadID, resolvedProviderUUID(session), binding.ProviderThreadID),
		AgentID:           agentID,
		ParentAgentID:     meta.ParentAgentID,
		AgentType:         meta.AgentType,
		AgentMemoryScope:  meta.AgentMemoryScope,
		Provider:          provider,
		CWD:               recoverCWD,
		Model:             meta.Model,
		Name:              displayName,
		Prompt:            displayName,
		RolloutPath:       kernel.FirstNonEmpty(binding.RolloutPath, session.RolloutPath()),
		SessionUUID:       kernel.FirstNonEmpty(binding.SessionUUID, resolvedProviderUUID(session)),
		ConfigOverride:    kernel.CloneRawMessage(meta.ConfigOverride),
		CreatedAt:         meta.CreatedAt,
	}), true); err != nil {
		return RecoverResult{}, err
	}
	if promptResumeRestoreRequiresInvalidation(recoverCWD, recoverCWD, s.cfg) {
		if err := s.invalidatePromptAssembly(ctx, contract.InvalidateResumeRestore); err != nil {
			return RecoverResult{}, err
		}
	}
	return RecoverResult{
		ThreadID:  publicThreadID,
		Status:    "recovering",
		Recovered: true,
		Mode:      mode,
	}, nil
}

func (s *service) requireRecoverProviderSession(agentID, publicThreadID, providerThreadID string) error {
	if strings.TrimSpace(providerThreadID) != "" {
		return nil
	}
	if _, err := s.lookupSession(agentID); err == nil {
		return nil
	}
	return fmt.Errorf("thread recover provider session id is required for %q", strings.TrimSpace(publicThreadID))
}

func (s *service) ensureRecoveredSession(
	ctx context.Context,
	bindingAgentID string,
	provider, agentID, publicThreadID, providerThreadID string,
) (string, error) {
	if _, err := s.lookupSession(agentID); err == nil {
		return "restore_launch", nil
	}
	if _, err := s.resumeSession(ctx, ResumeRequest{
		Provider:         provider,
		AgentID:          agentID,
		ThreadID:         publicThreadID,
		ProviderThreadID: providerThreadID,
	}); err != nil {
		return "", err
	}
	if err := s.bindSessionGeneration(ctx, bindingAgentID); err != nil {
		s.stopAgent(ctx, bindingAgentID)
		return "", err
	}
	return "relaunch_resume", nil
}

func resolveForkCWD(metaCWD, bindingCWD string) (string, error) {
	return resolveLifecycleCWD("fork", metaCWD, bindingCWD)
}

func resolveRecoverCWD(metaCWD, bindingCWD string) (string, error) {
	return resolveLifecycleCWD("recover", metaCWD, bindingCWD)
}

// resolveLifecycleCWD 解析生命周期操作使用的工作目录。
func resolveLifecycleCWD(action, metaCWD, bindingCWD string) (string, error) {
	meta := strings.TrimSpace(metaCWD)
	binding := strings.TrimSpace(bindingCWD)
	if meta != "" && binding != "" && comparablePromptCWD(meta) != comparablePromptCWD(binding) {
		return "", fmt.Errorf("thread %s cwd mismatch: meta cwd %q binding cwd %q", strings.TrimSpace(action), meta, binding)
	}
	cwd := kernel.FirstNonEmpty(meta, binding)
	if cwd == "" || cwd == "." {
		return "", fmt.Errorf("thread %s cwd is required", strings.TrimSpace(action))
	}
	return cwd, nil
}
