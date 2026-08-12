package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idempotency"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
)

// SessionStarter 是 contract.SessionStarter 的本地别名。
// 保留别名是为了让 thread 包内既有调用点不直接依赖 contract 包名。
type SessionStarter = contract.SessionStarter

// OrchestrationFacade 是 thread 服务调用编排模块的最小接口。
// thread 只关心 agent 生命周期动作，不直接依赖 orchestration 的内部 DAG 或 worker 实现。
type OrchestrationFacade interface {
	LaunchAgent(ctx context.Context, req LaunchAgentRequest) error
	StopAgent(ctx context.Context, agentID string) error
	Recover(ctx context.Context, agentID string) error
}

// SessionGenerationBinder 是 thread 绑定 provider session generation 的运行时端口。
type SessionGenerationBinder interface {
	BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error
}

// threadState 是 start/resume 成功后写入 thread store 和事件的状态快照。
// 它把 provider 身份、prompt 快照索引和 Codex 运行时身份放在一起，避免多处重复拼装。
type threadState struct {
	PublicThreadID, ProviderThreadID, OwnerThreadID, AgentID string
	ParentAgentID, AgentType, AgentMemoryScope, Provider     string
	CWD, Model, Name, Prompt, RolloutPath, SessionUUID       string
	CodexHome, ProviderRecoveryHome, CodexInstanceKey        string
	CodexModelProvider, AgentKey                             string
	ConfigOverride                                           json.RawMessage
	CreatedAt                                                int64
	PromptVersionID                                          *int64
	PendingLaunch                                            bool
}

// threadMeta 是恢复/分叉时从持久化 thread row 读取的轻量元信息。
// 它不包含运行时 session 指针，只承载重建请求所需的稳定字段。
type threadMeta struct {
	Name, Model, CWD, ParentAgentID, AgentType, AgentMemoryScope string
	ConfigOverride                                               json.RawMessage
	CreatedAt                                                    int64
}

// sessionGenerationProvider 暴露 provider session 的 generation 编号。
// 该编号用于停止/恢复时精确匹配当前 session，避免旧 goroutine 清理掉新 session。
type sessionGenerationProvider interface {
	SessionGeneration(agentID string) uint64
}

const (
	bindSessionGenerationDependency        = "thread.bind_session_generation"
	bindSessionGenerationStatusUnsupported = "unsupported"
)

type bindSessionGenerationStatusRecorder interface {
	RecordBindSessionGenerationSkipped(context.Context, bindGenerationStatusRecord) error
}

type bindGenerationStatusRecord struct {
	AgentID    string
	Dependency string
	Profile    contract.DependencyProfile
	Status     string
	Reason     string
}

// prepareStartRequest 完成启动前的请求规范化、Codex 身份注入和 agent id 预留。
// 返回的 release 必须在 startOnce 结束时执行，确保失败路径不会留下进程内 id 预留。
func (s *service) prepareStartRequest(ctx context.Context, req StartRequest) (StartRequest, string, func(), error) {
	callerProvidedID := strings.TrimSpace(req.AgentID) != ""
	req, agentID, err := normalizeStartRequest(req, s.agentIDGenerator)
	if err != nil {
		return req, "", nil, err
	}
	req = s.injectParentCodexIdentityForStart(ctx, req)
	req, err = s.injectDefaultCodexIdentityForStart(req)
	if err != nil {
		return req, "", nil, err
	}
	agentID, releaseAgentID, err := s.reserveUniqueStartAgentID(ctx, req, agentID, callerProvidedID)
	if err != nil {
		return req, "", nil, err
	}
	if releaseAgentID == nil {
		return req, "", nil, errors.New("thread: reserve agent_id failed")
	}
	req.AgentID = agentID
	return req, agentID, releaseAgentID, nil
}

// completeStart 串起 thread/start 的主流程：先选 prompt，再组 start 提示。
// provider 启动后再保存 thread 和 prompt snapshot，这个顺序不要调换。
func (s *service) completeStart(ctx context.Context, req StartRequest, agentID string) (result StartResult, err error) {
	// 路由必须早于 prompt assembly，路由产出的 BaseInstructions 才能进入组装；
	// AgentKey/PromptVersionID 等副产物也会通过 threadState 写入持久化记录。
	if err := s.resolveRoutedPrompt(ctx, &req); err != nil {
		return StartResult{}, err
	}
	if req.PromptAssemblyRef == nil {
		req.PromptAssemblyRef = s.promptAssembly
	}
	assemblyInput, cleanupScratchpad, err := s.buildStartAssemblyInput(ctx, req, agentID)
	if err != nil {
		return StartResult{}, err
	}
	cleanupOnFailure := true
	defer joinScratchpadCleanup(&err, &cleanupOnFailure, cleanupScratchpad)
	assembly, err := resolveStartPromptAssembly(ctx, req, assemblyInput)
	if err != nil {
		return StartResult{}, err
	}
	displayName := resolveDisplayName(ctx, s.threadStore, agentID, req.Prompt, assembly.DisplayName)
	if err := s.launchAgent(ctx, agentID, req.CWD, displayName, req.ParentAgentID,
		req.AgentType, req.AgentMemoryScope, req.Provider, req.Model); err != nil {
		return StartResult{}, idempotency.Retain(err)
	}
	session, err := s.establishStartedSession(ctx, req, assemblyInput, assembly, agentID)
	if err != nil {
		return StartResult{}, err
	}
	result, err = s.persistStartedSession(ctx, req, assemblyInput, assembly, agentID, displayName, session)
	if err != nil {
		return StartResult{}, err
	}
	cleanupOnFailure = false
	return result, nil
}

// Start 创建新的 public thread，并启动对应 provider。
// provider 和 cwd 必须明确给出，缺了就报错，不猜默认值。
func (s *service) Start(ctx context.Context, req StartRequest) (result StartResult, err error) {
	ctx = util.NonNilContext(ctx)
	span := s.beginThreadTraceSpan(ctx, "thread.start", req.AgentID, req.AgentID, platformobs.NewCodeAnchor("internal/module/thread/lifecycle.go", "thread.(*service).Start", 126), map[string]any{"provider": strings.TrimSpace(req.Provider)})
	ctx = span.ctx
	defer func() {
		if result.ThreadID != "" {
			span.threadID = result.ThreadID
		}
		if result.AgentID != "" {
			span.agentID = result.AgentID
		}
		s.finishThreadTraceSpan(span, err)
	}()
	if req.LaunchIntentID = strings.TrimSpace(req.LaunchIntentID); req.LaunchIntentID == "" {
		return s.startOnce(ctx, req)
	}
	intentID, err := idempotency.NormalizeKey("thread/start: launch_intent_id", req.LaunchIntentID)
	if err != nil {
		return StartResult{}, err
	}
	req.LaunchIntentID = intentID
	result, err = s.launchIntentRegistry.DoJSON(intentID, startRequestFingerprint(req), func() (StartResult, error) {
		return s.startOnce(ctx, req)
	})
	if err == nil && result.ThreadID != "" {
		s.launchIntentByThread.Store(result.ThreadID, intentID)
	}
	return result, err
}

// CompleteLaunchIntent 完成启动意图并写入最终线程标识。
func (s *service) CompleteLaunchIntent(_ context.Context, threadID string) {
	threadID = strings.TrimSpace(threadID)
	s.pendingLaunchMu.Delete(threadID)
	idempotency.ForgetMappedUnlessError(&s.launchIntentByThread, &s.launchIntentRegistry, threadID)
}

// startRequestFingerprint 生成 launch intent 幂等键使用的请求快照。
// 临时字段和路由副产物会被清空，避免同一用户意图因内部补全字段不同而重复启动。
func startRequestFingerprint(req StartRequest) StartRequest {
	req.LaunchIntentID, req.AgentTitle, req.PromptAssemblyRef, req.PromptVersionID, req.PromptKeyStale = "", "", nil, nil, false
	return req
}

// startOnce 执行一次实际启动流程，不处理 launch intent 的幂等封装。
// agent id 预留在函数退出时释放，持久化唯一性由 thread/binding store 继续保证。
func (s *service) startOnce(ctx context.Context, req StartRequest) (StartResult, error) {
	req, agentID, releaseAgentID, err := s.prepareStartRequest(ctx, req)
	if err != nil {
		return StartResult{}, err
	}
	defer releaseAgentID()
	if isPendingLaunchIntent(req) {
		return s.startPendingThread(ctx, req, agentID)
	}
	return s.completeStart(ctx, req, agentID)
}

// Resume 只把已有 thread 接回 provider。
// 它复用保存过的 thread、binding 和 snapshot，不重新走 thread/start。
func (s *service) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	ctx = util.NonNilContext(ctx)
	requestedThreadID := strings.TrimSpace(req.ThreadID)
	if requestedThreadID != "" {
		if reason, blocked := s.resumeLifecycleBlockReason(ctx, requestedThreadID, nil); blocked {
			return ResumeResult{}, resumeLifecycleError(requestedThreadID, reason)
		}
	}
	req, state, err := s.resolveResumeRequest(ctx, req)
	if err != nil {
		return ResumeResult{}, err
	}
	if reason, blocked := s.resumeLifecycleBlockReason(ctx, req.ThreadID, nil); blocked {
		return ResumeResult{}, resumeLifecycleError(req.ThreadID, reason)
	}
	req.Provider = util.FirstNonEmpty(req.Provider, state.Provider)
	req.Model = util.FirstNonEmpty(req.Model, state.Model)
	req.CWD = util.FirstNonEmpty(req.CWD, state.CWD, s.lookupBindingCWD(ctx, req.AgentID))
	displayName := resolveDisplayName(ctx, s.threadStore, req.AgentID, "", state.Prompt)
	snapshot, err := s.resolveResumePromptSnapshot(ctx, req, state)
	if err != nil {
		return ResumeResult{}, err
	}
	req.PromptSnapshot = snapshot
	session, err := s.establishResumedSession(ctx, req, state, displayName)
	if err != nil {
		if isUnrecoverableResumeError(err) {
			s.degradeLostResume(ctx, req.ThreadID, req.AgentID, err)
			return ResumeResult{}, resumeLostError(err)
		}
		return ResumeResult{}, err
	}
	return s.persistResumedSession(ctx, req, state, displayName, session)
}

// establishStartedSession 启动 provider session 并绑定 generation。
// 任一步失败都会尝试停止已创建的 agent，并用 RetainOnError 保留原始启动错误。
func (s *service) establishStartedSession(
	ctx context.Context,
	req StartRequest,
	input contract.StartInput,
	assembly contract.StartAssembly,
	agentID string,
) (contract.Session, error) {
	if _, err := s.startSession(ctx, req, input, assembly, agentID); err != nil {
		return nil, idempotency.RetainOnError(err, s.stopAgent(ctx, agentID))
	}
	if err := s.bindSessionGeneration(ctx, agentID); err != nil {
		return nil, idempotency.RetainOnError(err, s.cleanupFailedStartedSession(ctx, agentID))
	}
	session, err := s.lookupSession(agentID)
	if err != nil {
		return nil, idempotency.RetainOnError(err, s.cleanupFailedStartedSession(ctx, agentID))
	}
	return session, nil
}

// persistStartedSession 保存 start 成功后的 thread、binding 和 prompt snapshot。
// snapshot 保存失败会清理已写记录，避免留下不能 resume/fork 的半成品线程。
func (s *service) persistStartedSession(
	ctx context.Context,
	req StartRequest,
	input contract.StartInput,
	assembly contract.StartAssembly,
	agentID, displayName string,
	session contract.Session,
) (StartResult, error) {
	providerUUID, err := requireStartedProviderUUID(session, req.Provider, agentID)
	if err != nil {
		return StartResult{}, idempotency.RetainOnError(err, s.cleanupFailedStartedSession(ctx, agentID))
	}
	effectiveModel, effectiveCWD, _ := enrichFromSessionConfig(session, req.Model, req.CWD)
	identity, err := resolveStartedSessionCodexIdentity(req.Provider, req.Config, session)
	if err != nil {
		return StartResult{}, idempotency.RetainOnError(err, s.cleanupFailedStartedSession(ctx, agentID))
	}
	codexHome, codexInstanceKey, codexModelProvider := identity.Home, identity.InstanceKey, identity.ModelProvider
	storedConfig := buildStartStoredThreadConfig(req, input, assembly, session)
	if codexHome != "" {
		storedConfig.Runtime[contract.CodexHomeKey] = codexHome
		storedConfig.Runtime[contract.CodexInstanceKeyKey] = codexInstanceKey
		storedConfig.Runtime[contract.CodexModelProviderKey] = codexModelProvider
	}
	configOverride, err := encodeStoredThreadConfig(storedConfig)
	if err != nil {
		return StartResult{}, idempotency.RetainOnError(err, s.cleanupFailedStartedSession(ctx, agentID))
	}
	rolloutPath := session.RolloutPath()
	claudeHome := resumeRuntimeConfigString(req.Config, "claudeHome", "claude_home", "history_dir")
	providerThreadID, err := recoverableProviderThreadID(req.Provider, providerUUID, rolloutPath, codexHome, claudeHome)
	if err != nil {
		return StartResult{}, idempotency.RetainOnError(err, s.cleanupFailedStartedSession(ctx, agentID))
	}
	state := newThreadState(threadStateStartKind, threadStateFields{
		AgentID:              agentID,
		ParentAgentID:        req.ParentAgentID,
		AgentType:            req.AgentType,
		AgentMemoryScope:     req.AgentMemoryScope,
		ProviderThreadID:     providerThreadID,
		Provider:             req.Provider,
		CWD:                  effectiveCWD,
		Model:                effectiveModel,
		Name:                 displayName,
		Prompt:               displayName,
		RolloutPath:          rolloutPath,
		SessionUUID:          providerUUID,
		ConfigOverride:       configOverride,
		CodexHome:            codexHome,
		ProviderRecoveryHome: providerRecoveryHome(req.Provider, codexHome, claudeHome),
		CodexInstanceKey:     codexInstanceKey,
		CodexModelProvider:   codexModelProvider,
		CreatedAt:            time.Now().UnixMilli(),
		AgentKey:             req.AgentKey,
		PromptVersionID:      req.PromptVersionID,
		OwnerThreadID:        req.OwnerThreadID,
	})
	publicThreadID := state.PublicThreadID
	providerThreadID = state.ProviderThreadID
	if err := s.persistThreadStateWithPromptSnapshot(ctx, state, true, assembly, true); err != nil {
		return StartResult{}, idempotency.RetainOnError(err, s.cleanupFailedStartedSession(ctx, agentID))
	}
	return newStartResult(req, publicThreadID, agentID, providerUUID, providerThreadID, effectiveModel, effectiveCWD), nil
}

// resolveStartedSessionCodexIdentity 解析 start 持久化所需的 Codex 身份；runtime 显式字段必须先通过完整校验。
func resolveStartedSessionCodexIdentity(provider string, config map[string]any, session contract.Session) (contract.CodexIdentity, error) {
	if !strings.EqualFold(strings.TrimSpace(provider), "codex") {
		return contract.CodexIdentity{}, nil
	}
	effective, ok := sessionRuntimeCodexIdentityConfig(session)
	if !ok {
		effective = make(map[string]any, 3)
		for _, key := range []string{contract.CodexHomeKey, contract.CodexInstanceKeyKey, contract.CodexModelProviderKey} {
			if raw, ok := config[key]; ok {
				effective[key] = raw
			}
		}
		ok = len(effective) > 0
	}
	if !ok {
		return contract.CodexIdentity{}, contract.ErrCodexHomeRequired
	}
	return contract.ResolveCodexIdentity(effective)
}

// establishResumedSession 恢复已有线程会话并绑定 provider session。
func (s *service) establishResumedSession(
	ctx context.Context,
	req ResumeRequest,
	state resumeState,
	displayName string,
) (contract.Session, error) {
	if reason, blocked := s.resumeLifecycleBlockReason(ctx, req.ThreadID, nil); blocked {
		return nil, resumeLifecycleError(req.ThreadID, reason)
	}
	if s.sessions != nil {
		s.sessions.RemoveSession(req.AgentID)
	}
	if err := s.launchAgent(
		ctx,
		req.AgentID,
		req.CWD,
		displayName,
		state.ParentAgentID,
		state.AgentType,
		state.AgentMemoryScope,
		req.Provider,
		req.Model,
	); err != nil {
		return nil, err
	}
	session, err := s.resumeResolvedRequestSession(ctx, req)
	if err != nil {
		s.stopAgent(ctx, req.AgentID)
		return nil, err
	}
	if session == nil {
		s.stopAgent(ctx, req.AgentID)
		return nil, errors.New("thread: resumed session is nil")
	}
	if err := s.bindSessionGeneration(ctx, req.AgentID); err != nil {
		s.stopAgent(ctx, req.AgentID)
		return nil, err
	}
	return session, nil
}

// persistResumedSession 刷新 resume 后的 binding/thread 记录。
// binding 冲突时要停掉旧 session，避免后续 provider 事件写到错误线程。
func (s *service) persistResumedSession(
	ctx context.Context,
	req ResumeRequest,
	state resumeState,
	displayName string,
	session contract.Session,
) (ResumeResult, error) {
	threadState, err := s.buildResumedThreadState(ctx, req, state, displayName, session)
	if err != nil {
		return ResumeResult{}, err
	}
	publicThreadID := threadState.PublicThreadID
	providerThreadID := threadState.ProviderThreadID
	if err := s.persistThreadState(ctx, threadState, true); err != nil {
		s.logResumePersistFailure(req.AgentID, publicThreadID, providerThreadID, err)
		if isBindingConflictError(err) {
			if s.logger != nil {
				s.logger.Error("thread: binding conflict on resume — killing zombie session",
					"agent_id", req.AgentID,
					"stale_provider_thread_id", providerThreadID)
			}
			return ResumeResult{}, fmt.Errorf("resume aborted due to binding conflict: %w", s.resumePersistFailure(ctx, req.AgentID, err))
		}
		return ResumeResult{}, fmt.Errorf("persist resumed thread state: %w", s.resumePersistFailure(ctx, req.AgentID, err))
	}
	s.activateResumedSession(req.AgentID)
	if promptResumeRestoreRequiresInvalidation(state.StoredCWD, req.CWD, s.cfg) {
		if err := s.invalidatePromptAssembly(ctx, contract.InvalidateResumeRestore); err != nil {
			return ResumeResult{}, err
		}
	}
	return ResumeResult{
		ThreadID:  publicThreadID,
		SessionID: util.FirstNonEmpty(providerThreadID, publicThreadID),
		Status:    "resumed",
		Model:     threadState.Model,
		CWD:       req.CWD,
	}, nil
}

// buildResumedThreadState 解析 provider identity 并构造待持久化的 resume 快照。
func (s *service) buildResumedThreadState(
	ctx context.Context,
	req ResumeRequest,
	state resumeState,
	displayName string,
	session contract.Session,
) (threadState, error) {
	model, provider := util.FirstNonEmpty(req.Model, state.Model), util.FirstNonEmpty(req.Provider, state.Provider)
	codexHome, codexInstanceKey, codexModelProvider := util.FirstNonEmpty(req.CodexHome, state.CodexHome), util.FirstNonEmpty(req.CodexInstanceKey, state.CodexInstanceKey), util.FirstNonEmpty(req.CodexModelProvider, state.CodexModelProvider)
	reqIdentityResolved := req.CodexHome != "" && req.CodexInstanceKey != "" && req.CodexModelProvider != ""
	runtimeIdentity, hasRuntimeIdentity := sessionRuntimeCodexIdentityConfig(session)
	configOverride, identity, ok, err := canonicalizeResumeStoredThreadConfig(provider, state.ConfigOverrideRaw, codexHome, codexInstanceKey, codexModelProvider, reqIdentityResolved, runtimeIdentity, hasRuntimeIdentity)
	if err != nil {
		return threadState{}, s.resumePersistFailure(ctx, req.AgentID, err)
	}
	codexHome, codexInstanceKey, codexModelProvider = resolvedResumeCodexIdentity(
		codexHome, codexInstanceKey, codexModelProvider, identity, ok,
	)
	rolloutPath := util.FirstNonEmpty(state.RolloutPath, session.RolloutPath())
	sessionUUID := util.FirstNonEmpty(resolvedProviderUUID(session), state.SessionUUID, req.ProviderThreadID, state.ProviderThreadID)
	recoveryHome := state.ProviderRecoveryHome
	recoveryCodexHome := codexHome
	if strings.EqualFold(provider, "codex") && recoveryCodexHome == "" {
		recoveryCodexHome = recoveryHome
	}
	claudeHome := util.FirstNonEmpty(req.ClaudeHome, state.ClaudeHome)
	if strings.EqualFold(provider, "claude") && claudeHome == "" {
		claudeHome = recoveryHome
	}
	providerThreadID, err := s.recoverResumedProviderThreadID(ctx, req.AgentID, provider, sessionUUID, rolloutPath, recoveryCodexHome, claudeHome)
	if err != nil {
		return threadState{}, err
	}
	return newThreadState(threadStateResumeKind, threadStateFields{
		RequestedThreadID:    req.ThreadID,
		PublicThreadID:       state.PublicThreadID,
		ProviderThreadID:     providerThreadID,
		AgentID:              req.AgentID,
		ParentAgentID:        state.ParentAgentID,
		AgentType:            state.AgentType,
		AgentMemoryScope:     state.AgentMemoryScope,
		Provider:             provider,
		CWD:                  req.CWD,
		Model:                model,
		Name:                 displayName,
		Prompt:               displayName,
		RolloutPath:          rolloutPath,
		SessionUUID:          sessionUUID,
		ConfigOverride:       configOverride,
		CodexHome:            codexHome,
		ProviderRecoveryHome: providerRecoveryHome(provider, recoveryCodexHome, claudeHome),
		CodexInstanceKey:     codexInstanceKey,
		CodexModelProvider:   codexModelProvider,
		CreatedAt:            state.CreatedAt,
	}), nil
}

// resolvedResumeCodexIdentity 在 canonical identity 可用时替换存量字段。
func resolvedResumeCodexIdentity(
	codexHome, codexInstanceKey, codexModelProvider string,
	identity contract.CodexIdentity,
	ok bool,
) (string, string, string) {
	if !ok {
		return codexHome, codexInstanceKey, codexModelProvider
	}
	return identity.Home, identity.InstanceKey, identity.ModelProvider
}

// recoverResumedProviderThreadID 解析 resume 身份并统一执行失败清理。
func (s *service) recoverResumedProviderThreadID(
	ctx context.Context,
	agentID, provider, sessionUUID, rolloutPath, codexHome, claudeHome string,
) (string, error) {
	providerThreadID, err := recoverableProviderThreadID(provider, sessionUUID, rolloutPath, codexHome, claudeHome)
	if err != nil {
		return "", s.resumePersistFailure(ctx, agentID, err)
	}
	return providerThreadID, nil
}

type resumedSessionActivator interface {
	ActivateSession(agentID string) bool
}

func (s *service) activateResumedSession(agentID string) {
	if s == nil || s.sessions == nil {
		return
	}
	activator, ok := s.sessions.(resumedSessionActivator)
	if !ok {
		return
	}
	activator.ActivateSession(agentID)
}

func (s *service) resumePersistFailure(ctx context.Context, agentID string, cause error) error {
	if cleanupErr := s.cleanupFailedResumeRuntime(ctx, agentID); cleanupErr != nil {
		return errors.Join(cause, cleanupErr)
	}
	return cause
}

// cleanupFailedResumeRuntime 清理已恢复但未成功持久化的 runtime。
// provider session 先从本地管理器移除，再停止 orchestration agent，避免 pending session 泄露。
func (s *service) cleanupFailedResumeRuntime(ctx context.Context, agentID string) error {
	if s == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	if s.sessions != nil {
		s.sessions.RemoveSession(agentID)
	}
	return s.stopAgent(ctx, agentID)
}

// cleanupFailedStartedSession 清理已创建但未成功持久化的 start runtime。
// 先按 generation 关闭/移除 provider session，再停止 orchestration agent，避免清理竞态误删新 session。
func (s *service) cleanupFailedStartedSession(ctx context.Context, agentID string) error {
	if s == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	var cleanupErr error
	if s.sessions != nil {
		var generation uint64
		if provider, ok := s.sessions.(sessionGenerationProvider); ok {
			generation = provider.SessionGeneration(agentID)
		}
		session, err := s.sessions.GetSession(agentID)
		switch {
		case err == nil && session != nil:
			cleanupErr = errors.Join(cleanupErr, session.Close(ctx))
		case err != nil && !errors.Is(err, contract.ErrSessionNotFound):
			cleanupErr = errors.Join(cleanupErr, err)
		}
		s.removeStoppedSession(agentID, generation)
	}
	return errors.Join(cleanupErr, s.stopAgent(ctx, agentID))
}

func (s *service) logResumePersistFailure(agentID, threadID, providerThreadID string, err error) {
	if s == nil || s.logger == nil {
		return
	}
	conflict := isBindingConflictError(err)
	s.logger.Warn("thread: resume persist failed",
		"error", err,
		"binding_conflict", conflict,
		"event_emitted", false,
		"agent_id", agentID,
		"thread_id", threadID,
		"provider_thread_id", providerThreadID,
	)
}

// persistThreadState 持久化线程状态和运行时标识。
func (s *service) persistThreadState(ctx context.Context, state threadState, updateBinding bool) error {
	state, err := normalizeThreadState(state)
	if err != nil {
		return err
	}
	if err := s.ensurePublicThreadAvailable(ctx, state); err != nil {
		return err
	}
	if state.PublicThreadID == "" || state.AgentID == "" {
		return errors.New("thread and agent ids are required")
	}
	if s.logger != nil {
		fields := []any{
			"agent_id", state.AgentID,
			"parent_agent_id", state.ParentAgentID,
			"agent_type", state.AgentType,
			"agent_memory_scope", state.AgentMemoryScope,
			"provider", state.Provider,
			"provider_thread_id", state.ProviderThreadID,
			"public_thread_id", state.PublicThreadID,
			"session_uuid", state.SessionUUID,
			"update_binding", updateBinding,
		}
		fields = append(fields, platformshared.SafePathLogFields("rollout_path", state.RolloutPath)...)
		s.logger.Debug("thread: persistThreadState binding snapshot", fields...)
	}
	bindingOutcome, err := s.maybeRegisterThreadBinding(ctx, state, updateBinding)
	if err != nil {
		return err
	}
	return s.persistStartedThread(ctx, state, bindingOutcome)
}

// persistThreadStateWithPromptSnapshot 先写 durable row/binding 和 prompt snapshot，再按需发布 Started。
// snapshot 写失败时回滚已写入的 row/binding，避免 UI 看到一个不能 resume/fork 的线程。
func (s *service) persistThreadStateWithPromptSnapshot(
	ctx context.Context,
	state threadState,
	updateBinding bool,
	assembly contract.StartAssembly,
	publishStarted bool,
) error {
	state, err := normalizeThreadState(state)
	if err != nil {
		return err
	}
	if err := s.ensurePublicThreadAvailable(ctx, state); err != nil {
		return err
	}
	if state.PublicThreadID == "" || state.AgentID == "" {
		return errors.New("thread and agent ids are required")
	}
	bindingOutcome, err := s.maybeRegisterThreadBinding(ctx, state, updateBinding)
	if err != nil {
		return err
	}
	if err := s.upsertPublicThread(ctx, state, bindingOutcome); err != nil {
		return err
	}
	if err := s.savePromptSnapshot(ctx, state.PublicThreadID, assembly); err != nil {
		if cleanupErr := s.cleanupThreadStateAfterSnapshotFailure(ctx, state); cleanupErr != nil {
			return idempotency.Retain(errors.Join(err, cleanupErr))
		}
		return err
	}
	if publishStarted {
		s.rememberStartedThread(state)
		s.publishThreadStarted(state)
	}
	return nil
}

func (s *service) cleanupThreadStateAfterSnapshotFailure(ctx context.Context, state threadState) error {
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

func (s *service) lookupThreadMeta(ctx context.Context, threadID string) (threadMeta, error) {
	thread, err := s.getThread(ctx, threadID)
	if err != nil {
		if contract.IsNotFound(err) {
			return threadMeta{}, fmt.Errorf("thread %q missing", strings.TrimSpace(threadID))
		}
		return threadMeta{}, err
	}
	if thread == nil {
		return threadMeta{}, fmt.Errorf("thread %q missing", strings.TrimSpace(threadID))
	}
	return threadMeta{
		Name:             strings.TrimSpace(thread.Prompt),
		Model:            strings.TrimSpace(thread.Model),
		CWD:              strings.TrimSpace(thread.Cwd),
		ParentAgentID:    strings.TrimSpace(thread.ParentAgentID),
		AgentType:        strings.TrimSpace(thread.AgentType),
		AgentMemoryScope: strings.TrimSpace(thread.AgentMemoryScope),
		ConfigOverride:   clone.RawMessage(thread.ConfigOverride),
		CreatedAt:        thread.CreatedAt,
	}, nil
}

func (s *service) requireThreadMeta(ctx context.Context, threadID string) (threadMeta, error) {
	return s.lookupThreadMeta(ctx, threadID)
}

func (s *service) stopAgent(ctx context.Context, agentID string) error {
	return s.stopManagedAgent(ctx, strings.TrimSpace(agentID), true)
}

func (s *service) rememberThreadAgent(threadID, agentID string) {
	threadID, agentID = strings.TrimSpace(threadID), strings.TrimSpace(agentID)
	if threadID == "" || agentID == "" {
		return
	}
	s.threadAgentsMu.Lock()
	defer s.threadAgentsMu.Unlock()
	if s.threadAgents == nil {
		s.threadAgents = make(map[string]string)
	}
	s.threadAgents[threadID] = agentID
}
func (s *service) lookupThreadAgent(threadID string) string {
	if threadID = strings.TrimSpace(threadID); threadID == "" {
		return ""
	}
	s.threadAgentsMu.RLock()
	defer s.threadAgentsMu.RUnlock()
	return s.threadAgents[threadID]
}
func (s *service) forgetThreadAgent(threadID string) {
	if threadID = strings.TrimSpace(threadID); threadID == "" {
		return
	}
	s.threadAgentsMu.Lock()
	defer s.threadAgentsMu.Unlock()
	delete(s.threadAgents, threadID)
}

func resolvedProviderUUID(session contract.Session) string {
	if session == nil {
		return ""
	}
	if id, err := providerrecovery.CanonicalizeUUID(strings.TrimSpace(session.ThreadID())); err == nil {
		return id
	}
	return ""
}

func requireStartedProviderUUID(session contract.Session, provider, agentID string) (string, error) {
	id := resolvedProviderUUID(session)
	if id != "" {
		return id, nil
	}
	if allowDeferredStartedProviderUUID(session, provider, agentID) {
		return "", nil
	}
	return "", fmt.Errorf("thread: provider session UUID required to start agent %q (%s)", strings.TrimSpace(agentID), strings.TrimSpace(provider))
}

func allowDeferredStartedProviderUUID(session contract.Session, provider, agentID string) bool {
	if session == nil || !strings.EqualFold(strings.TrimSpace(provider), "claude") {
		return false
	}
	threadID, agentID := strings.TrimSpace(session.ThreadID()), strings.TrimSpace(agentID)
	return threadID == "" || threadID == agentID || strings.HasPrefix(strings.ToLower(threadID), "agent_")
}

// isBindingConflictError 判断错误是否为 binding 唯一性冲突。
// provider_thread_id 或 public_thread_id 已属于其它 agent 时，不能发布 thread.Started，
// 否则前端会缓存错误 provider id，后续历史加载会因 provider_mismatch 变成空 UI。
func isBindingConflictError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already bound to agent") ||
		strings.Contains(msg, "already bound to provider") ||
		strings.Contains(msg, "already bound to public thread")
}
