package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
)

const statusForkCreating = "creating"

type forkKickoffError struct {
	err        error
	markFailed bool
}

// Error 返回 fork kickoff 的原始失败，保留 errors.Is/As 可见的错误文本。
func (e forkKickoffError) Error() string { return e.err.Error() }

// Unwrap 暴露被包装错误，方便上层判断 provider 或持久化的具体失败。
func (e forkKickoffError) Unwrap() error { return e.err }

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
	snapshot, err := s.resolveStablePromptSnapshot(ctx, threadID, provider, contract.PromptAssemblySnapshot{})
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
	state := threadStateFields{PublicThreadID: newThreadID, OwnerThreadID: historyTargetID(binding, threadID), AgentID: newThreadID, ParentAgentID: meta.ParentAgentID, AgentType: meta.AgentType, AgentMemoryScope: meta.AgentMemoryScope, Provider: provider, CWD: cwd, Model: meta.Model, Name: displayName, Prompt: displayName, ConfigOverride: configOverride, CodexHome: identity.Home, CodexInstanceKey: identity.InstanceKey, CodexModelProvider: identity.ModelProvider, CreatedAt: time.Now().UnixMilli()}
	forkState := newThreadState(threadStateForkKind, state)
	if err := s.persistCreatingForkStateWithPromptSnapshot(ctx, forkState, snapshot); err != nil {
		return ForkResult{}, err
	}
	if err := s.kickoffForkSession(ctx, state, meta, provider, cwd, displayName, newThreadID, snapshot, identity, config); err != nil {
		return ForkResult{}, s.handleForkKickoffFailure(ctx, forkState, err)
	}
	return ForkResult{NewThreadID: newThreadID, ForkedFrom: bindingPublicThreadID(binding, threadID), KickoffState: ForkKickoffCreatedOnly}, nil
}

// handleForkKickoffFailure 统一处理 creating fork 的失败出口。
// 可诊断为运行态失败的保留 failed 行；启动未完成的半成品会删除 row、binding 与 snapshot。
func (s *service) handleForkKickoffFailure(ctx context.Context, state threadState, err error) error {
	if shouldMarkForkFailed(err) {
		if markErr := s.markForkFailed(ctx, state); markErr != nil {
			return forkKickoffError{err: errors.Join(err, markErr), markFailed: true}
		}
		return err
	}
	if cleanupErr := s.cleanupForkCreatingState(ctx, state); cleanupErr != nil {
		return forkKickoffError{err: errors.Join(err, cleanupErr)}
	}
	return forkKickoffError{err: err}
}

// persistCreatingForkStateWithPromptSnapshot 先写入不可恢复的 creating fork 行。
// kickoff 未完成前不暴露为 created，失败路径可统一删除半成品 thread、binding 和 snapshot。
func (s *service) persistCreatingForkStateWithPromptSnapshot(ctx context.Context, state threadState, snapshot contract.PromptAssemblySnapshot) error {
	state, err := normalizeThreadState(state)
	if err != nil {
		return err
	}
	if err := s.ensurePublicThreadAvailable(ctx, state); err != nil {
		return err
	}
	bindingOutcome, err := s.maybeRegisterThreadBinding(ctx, state, true)
	if err != nil {
		return err
	}
	if err := s.upsertForkThreadStatus(ctx, state, statusForkCreating); err != nil {
		if rollbackErr := s.rollbackThreadBinding(ctx, bindingOutcome); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	if err := s.savePromptSnapshot(ctx, state.PublicThreadID, contract.StartAssembly{Snapshot: snapshot}); err != nil {
		if cleanupErr := s.cleanupForkCreatingState(ctx, state); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		return err
	}
	return nil
}

func (s *service) upsertForkThreadStatus(ctx context.Context, state threadState, status string) error {
	if s.threadStore == nil {
		return errors.New("thread: thread store is not configured")
	}
	displayName := strings.TrimSpace(util.FirstNonEmpty(state.Name, state.Prompt))
	return s.upsertThread(ctx, threadConfigStoreRecord{
		ThreadID:         state.PublicThreadID,
		Name:             displayName,
		Prompt:           displayName,
		Model:            state.Model,
		Cwd:              state.CWD,
		Status:           strings.TrimSpace(status),
		CreatedAt:        state.CreatedAt,
		UpdatedAt:        time.Now().UnixMilli(),
		OwnerThreadID:    state.OwnerThreadID,
		ParentAgentID:    state.ParentAgentID,
		AgentType:        state.AgentType,
		AgentMemoryScope: state.AgentMemoryScope,
		ConfigOverride:   clone.RawMessage(state.ConfigOverride),
		PromptVersionID:  state.PromptVersionID,
	})
}

func (s *service) cleanupForkCreatingState(ctx context.Context, state threadState) error {
	var cleanupErr error
	if store := s.threadBindingStorePort(); store != nil {
		cleanupErr = errors.Join(cleanupErr, store.DeleteByAgentID(ctx, state.AgentID))
	}
	if s.threadStore != nil {
		cleanupErr = errors.Join(cleanupErr, s.threadStore.DeleteByThreadID(ctx, state.PublicThreadID))
	}
	s.forgetThreadAgents(state.PublicThreadID, state.ProviderThreadID)
	return cleanupErr
}

func (s *service) markForkFailed(ctx context.Context, state threadState) error {
	return s.updateThreadStatus(ctx, state.PublicThreadID, statusFailed)
}

func shouldMarkForkFailed(err error) bool {
	var kickoffErr forkKickoffError
	return errors.As(err, &kickoffErr) && kickoffErr.markFailed
}

// kickoffForkSession 启动 fork 的 provider session，并在成功后补齐最终 thread 状态。
func (s *service) kickoffForkSession(ctx context.Context, state threadStateFields, meta threadMeta, provider, cwd, displayName, newThreadID string, snapshot contract.PromptAssemblySnapshot, identity contract.CodexIdentity, config map[string]any) error {
	if err := s.launchAgent(ctx, newThreadID, cwd, displayName, meta.ParentAgentID, meta.AgentType, meta.AgentMemoryScope, provider, meta.Model); err != nil {
		return err
	}
	forkedSession, err := s.resumeForkSession(ctx, ResumeRequest{Provider: provider, AgentID: newThreadID, ThreadID: newThreadID, ProviderThreadID: newThreadID, CWD: cwd, Model: meta.Model, PromptSnapshot: snapshot, Config: clone.RuntimeConfigMap(config), CodexHome: identity.Home, CodexInstanceKey: identity.InstanceKey, CodexModelProvider: identity.ModelProvider})
	if err != nil {
		s.stopAgent(ctx, newThreadID)
		return err
	}
	if err := s.bindSessionGeneration(ctx, newThreadID); err != nil {
		s.stopAgent(ctx, newThreadID)
		return forkKickoffError{err: err, markFailed: true}
	}
	fillForkProviderState(&state, forkedSession)
	finalState := newThreadState(threadStateForkKind, state)
	bindingOutcome, err := s.maybeRegisterThreadBinding(ctx, finalState, true)
	if err != nil {
		s.stopAgent(ctx, newThreadID)
		return err
	}
	if err := s.upsertPublicThread(ctx, finalState, bindingOutcome); err != nil {
		s.stopAgent(ctx, newThreadID)
		return err
	}
	if err := s.activateForkedSession(newThreadID); err != nil {
		stopErr := s.stopAgent(ctx, newThreadID)
		if s.sessions != nil {
			s.sessions.RemoveSession(newThreadID)
		}
		return forkKickoffError{err: errors.Join(err, stopErr), markFailed: true}
	}
	s.rememberStartedThread(finalState)
	s.publishThreadStarted(finalState)
	return nil
}

// activateForkedSession 在 durable fork 状态落盘后公开 pending session。
// Fork 不能像兼容 resume helper 那样吞掉缺失接口或 false，否则会返回一个不可路由的 created thread。
func (s *service) activateForkedSession(agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errors.New("thread: fork session agent id is required")
	}
	if s == nil || s.sessions == nil {
		return errors.New("thread: fork session provider is not configured")
	}
	activator, ok := s.sessions.(resumedSessionActivator)
	if !ok {
		return errors.New("thread: fork session provider does not support activation")
	}
	if !activator.ActivateSession(agentID) {
		return fmt.Errorf("thread: activate fork session %q failed", agentID)
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
	meta, err := s.requireThreadMeta(ctx, threadID)
	if err != nil {
		return threadMeta{}, "", "", contract.CodexIdentity{}, nil, nil, err
	}
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
	return identity, providerRuntimeConfig(stored.Runtime), clone.RawMessage(raw), nil
}

// Recover 重新接上 binding 指向的 provider session。
// 它复用 thread meta、runtime config 和已有 snapshot，只刷新 binding/thread 状态。
func (s *service) Recover(ctx context.Context, threadID string) (RecoverResult, error) {
	ctx = util.NonNilContext(ctx)
	binding, meta, err := s.resolveRecoverContext(ctx, threadID)
	if err != nil {
		return RecoverResult{}, err
	}
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
	mode, session, err := s.ensureRecoveredSession(ctx, binding.AgentID, provider, agentID, publicThreadID, providerThreadID)
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
		return RecoverResult{}, s.recoverPostResumeFailure(ctx, mode, agentID, err)
	}
	s.activateRecoveredSession(mode, agentID)
	if promptResumeRestoreRequiresInvalidation(recoverCWD, recoverCWD, s.cfg) {
		if err := s.invalidatePromptAssembly(ctx, contract.InvalidateResumeRestore); err != nil {
			return RecoverResult{}, s.recoverPostResumeFailure(ctx, mode, agentID, err)
		}
	}
	return RecoverResult{
		ThreadID:  publicThreadID,
		Status:    "recovering",
		Recovered: true,
		Mode:      mode,
	}, nil
}

func (s *service) resolveRecoverContext(ctx context.Context, threadID string) (*threadBindingStoreRecord, threadMeta, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return nil, threadMeta{}, err
	}
	meta, err := s.requireThreadMeta(ctx, threadID)
	if err != nil {
		return nil, threadMeta{}, err
	}
	return binding, meta, nil
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
) (string, contract.Session, error) {
	if session, err := s.lookupSession(agentID); err == nil {
		return "restore_launch", session, nil
	}
	var session contract.Session
	session, err := s.resumeSession(ctx, ResumeRequest{
		Provider:         provider,
		AgentID:          agentID,
		ThreadID:         publicThreadID,
		ProviderThreadID: providerThreadID,
	})
	if err != nil {
		return "", nil, err
	}
	if err := s.bindSessionGeneration(ctx, bindingAgentID); err != nil {
		return "", nil, s.resumePersistFailure(ctx, agentID, err)
	}
	return "relaunch_resume", session, nil
}

func (s *service) activateRecoveredSession(mode, agentID string) {
	if mode == "relaunch_resume" {
		s.activateResumedSession(agentID)
	}
}

func (s *service) recoverPostResumeFailure(ctx context.Context, mode, agentID string, err error) error {
	if mode != "relaunch_resume" {
		return err
	}
	return s.resumePersistFailure(ctx, agentID, err)
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
