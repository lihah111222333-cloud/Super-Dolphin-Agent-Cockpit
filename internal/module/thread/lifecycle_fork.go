package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

// Fork 从当前 provider 历史分出一个新 thread。
// 它复用旧 thread 的 prompt snapshot，再接上新的 provider session；不要重新跑 start 路由。
func (s *service) Fork(ctx context.Context, threadID string) (ForkResult, error) {
	ctx = util.NonNilContext(ctx)
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return ForkResult{}, err
	}
	meta, provider, cwd, identity, config, configOverride, err := s.resolveForkContext(ctx, threadID, binding.Provider, binding.Cwd, binding.CodexHome, binding.CodexInstanceKey, binding.CodexModelProvider)
	if err != nil {
		return ForkResult{}, err
	}
	result, err := session.ForkThread(ctx, dto.ForkRequest{ThreadID: historyTargetID(binding, threadID)})
	if err != nil {
		return ForkResult{}, err
	}
	newThreadID := strings.TrimSpace(result.NewThreadID)
	if newThreadID == "" {
		return ForkResult{}, errors.New("fork thread id is required")
	}
	displayName := continuationName(strings.TrimSpace(meta.Name))
	snapshot, err := s.resolveStablePromptSnapshot(ctx, threadID, provider, contract.PromptAssemblySnapshot{})
	if err != nil {
		return ForkResult{}, err
	}
	state := threadStateFields{PublicThreadID: newThreadID, OwnerThreadID: historyTargetID(binding, threadID), AgentID: newThreadID, ParentAgentID: meta.ParentAgentID, AgentType: meta.AgentType, AgentMemoryScope: meta.AgentMemoryScope, Provider: provider, CWD: cwd, Model: meta.Model, Name: displayName, Prompt: displayName, ConfigOverride: configOverride, CodexHome: identity.Home, CodexInstanceKey: identity.InstanceKey, CodexModelProvider: identity.ModelProvider, CreatedAt: time.Now().Unix()}
	if err := s.persistThreadState(ctx, newThreadState(threadStateForkKind, state), true); err != nil {
		return ForkResult{}, err
	}
	if !promptSnapshotBlank(snapshot) {
		if err := s.savePromptSnapshot(ctx, newThreadID, contract.StartAssembly{Snapshot: snapshot}); err != nil {
			return ForkResult{}, fmt.Errorf("save fork prompt snapshot for %q: %w", strings.TrimSpace(newThreadID), err)
		}
	}
	if err := s.kickoffForkSession(ctx, state, meta, provider, cwd, displayName, newThreadID, snapshot, identity, config); err != nil {
		return ForkResult{}, err
	}
	return ForkResult{NewThreadID: newThreadID, ForkedFrom: bindingPublicThreadID(binding, threadID), KickoffState: ForkKickoffState("created_only")}, nil
}

// kickoffForkSession 启动 fork 的 provider session，并在成功后补齐最终 thread 状态。
func (s *service) kickoffForkSession(ctx context.Context, state threadStateFields, meta threadMeta, provider, cwd, displayName, newThreadID string, snapshot contract.PromptAssemblySnapshot, identity contract.CodexIdentity, config map[string]any) error {
	if err := s.launchAgent(ctx, newThreadID, cwd, displayName, meta.ParentAgentID, meta.AgentType, meta.AgentMemoryScope, provider, meta.Model); err != nil {
		return err
	}
	forkedSession, err := s.resumeForkSession(ctx, ResumeRequest{Provider: provider, AgentID: newThreadID, ThreadID: newThreadID, CWD: cwd, Model: meta.Model, PromptSnapshot: snapshot, Config: clone.RuntimeConfigMap(config), CodexHome: identity.Home, CodexInstanceKey: identity.InstanceKey, CodexModelProvider: identity.ModelProvider})
	if err != nil {
		s.stopAgent(ctx, newThreadID)
		return err
	}
	if err := s.bindSessionGeneration(ctx, newThreadID); err != nil {
		s.stopAgent(ctx, newThreadID)
		return err
	}
	fillForkProviderState(&state, forkedSession)
	finalState := newThreadState(threadStateForkKind, state)
	bindingOutcome, err := s.maybeRegisterThreadBinding(ctx, finalState, true)
	if err != nil {
		s.stopAgent(ctx, newThreadID)
		return err
	}
	if err := s.persistStartedThread(ctx, finalState, bindingOutcome); err != nil {
		s.stopAgent(ctx, newThreadID)
		return err
	}
	return nil
}

func fillForkProviderState(state *threadStateFields, session contract.Session) {
	state.ProviderThreadID = resolvedProviderUUID(session)
	state.RolloutPath = session.RolloutPath()
	state.SessionUUID = state.ProviderThreadID
}

// resolveForkContext 只从 thread meta 和 binding 取 provider/cwd。
// fork 不猜默认 provider；cwd 冲突时直接返回错误。
func (s *service) resolveForkContext(ctx context.Context, threadID, bindingProvider, bindingCWD, bindingHome, bindingKey, bindingModelProvider string) (threadMeta, string, string, contract.CodexIdentity, map[string]any, json.RawMessage, error) {
	meta := s.lookupThreadMeta(ctx, threadID)
	cwd, err := resolveForkCWD(meta.CWD, bindingCWD)
	if err != nil {
		return threadMeta{}, "", "", contract.CodexIdentity{}, nil, nil, err
	}
	provider := strings.TrimSpace(bindingProvider)
	if provider == "" {
		return threadMeta{}, "", "", contract.CodexIdentity{}, nil, nil, errors.New("fork provider is required")
	}
	identity, config, raw, err := resolveLifecycleCodexIdentity("thread/fork", provider, bindingHome, bindingKey, bindingModelProvider, meta.ConfigOverride)
	if err != nil {
		return threadMeta{}, "", "", contract.CodexIdentity{}, nil, nil, err
	}
	return meta, provider, cwd, identity, config, raw, nil
}

// resolveLifecycleCodexIdentity 从 binding 和线程 runtime 中提取 Codex 身份。
// 任一来源出现 partial identity 都直接失败；完整身份会 canonicalize 后写回 runtime config。
func resolveLifecycleCodexIdentity(action, provider, home, key, modelProvider string, raw json.RawMessage) (contract.CodexIdentity, map[string]any, json.RawMessage, error) {
	stored, err := decodeStoredThreadConfig(raw)
	if err != nil {
		return contract.CodexIdentity{}, nil, nil, fmt.Errorf("%s: decode source config: %w", strings.TrimSpace(action), err)
	}
	runtimeValues := collectResumeCodexIdentityValues(ResumeRequest{}, stored.Runtime)
	raw, identity, ok, err := canonicalizeResumeStoredThreadConfig(provider, raw, home, key, modelProvider, false, stored.Runtime, runtimeValues.hasAny())
	if err != nil {
		return contract.CodexIdentity{}, nil, nil, fmt.Errorf("%s: %w", strings.TrimSpace(action), err)
	}
	if ok {
		if stored, err = decodeStoredThreadConfig(raw); err != nil {
			return contract.CodexIdentity{}, nil, nil, err
		}
	}
	return identity, clone.RuntimeConfigMap(stored.Runtime), clone.RawMessage(raw), nil
}

// Recover 重新接上 binding 指向的 provider session。
// 它复用 thread meta、runtime config 和已有 snapshot，只刷新 binding/thread 状态。
func (s *service) Recover(ctx context.Context, threadID string) (RecoverResult, error) {
	ctx = util.NonNilContext(ctx)
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
		ProviderThreadID:  util.FirstNonEmpty(providerThreadID, resolvedProviderUUID(session), binding.ProviderThreadID),
		AgentID:           agentID,
		ParentAgentID:     meta.ParentAgentID,
		AgentType:         meta.AgentType,
		AgentMemoryScope:  meta.AgentMemoryScope,
		Provider:          provider,
		CWD:               recoverCWD,
		Model:             meta.Model,
		Name:              displayName,
		Prompt:            displayName,
		RolloutPath:       util.FirstNonEmpty(binding.RolloutPath, session.RolloutPath()),
		SessionUUID:       util.FirstNonEmpty(binding.SessionUUID, resolvedProviderUUID(session)),
		ConfigOverride:    clone.RawMessage(meta.ConfigOverride),
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
	cwd := util.FirstNonEmpty(meta, binding)
	if cwd == "" || cwd == "." {
		return "", fmt.Errorf("thread %s cwd is required", strings.TrimSpace(action))
	}
	return cwd, nil
}
