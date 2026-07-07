package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util/historyjsonl"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
)

const (
	defaultCodexInstanceKey   = "default"
	defaultCodexModelProvider = "super-dolphin-relay"
)

func runScratchpadCleanup(active *bool, cleanup func()) {
	if active == nil || !*active || cleanup == nil {
		return
	}
	cleanup()
}

// enrichFromSessionConfig 从会话配置补充线程。
func enrichFromSessionConfig(session contract.Session, reqModel, reqCWD string) (model, cwd string, port int) {
	model, cwd = reqModel, reqCWD
	rc, ok := session.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		return model, resolveRelativeCWD(cwd), 0
	}
	cfg := rc.RuntimeConfigSnapshot()
	if model == "" {
		model, _ = cfg["model"].(string)
	}
	if cwd == "" || cwd == "." {
		if v, _ := cfg["cwd"].(string); v != "" && v != "." {
			cwd = v
		}
	}
	if p, ok := cfg["port"].(int); ok && p > 0 {
		port = p
	}
	return model, resolveRelativeCWD(cwd), port
}

// sessionRuntimeCodexIdentityConfig 读取 session runtime 中显式出现的 Codex 身份原始值。
// key 一旦存在就交给上层校验，避免空字符串或错类型被当成缺失后又被其它来源补齐。
func sessionRuntimeCodexIdentityConfig(session contract.Session) (map[string]any, bool) {
	rc, ok := session.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		return nil, false
	}
	cfg := rc.RuntimeConfigSnapshot()
	identity := make(map[string]any, 3)
	for _, key := range []string{contract.CodexHomeKey, contract.CodexInstanceKeyKey, contract.CodexModelProviderKey} {
		if raw, ok := cfg[key]; ok {
			identity[key] = raw
		}
	}
	return identity, len(identity) > 0
}

// injectParentCodexIdentityForStart 在子线程启动时继承父 agent 的 Codex 身份配置。
func (s *service) injectParentCodexIdentityForStart(ctx context.Context, req StartRequest) StartRequest {
	parentID := strings.TrimSpace(req.ParentAgentID)
	store := s.threadBindingStorePort()
	if parentID == "" || store == nil {
		return req
	}
	parentBinding, err := store.GetByAgentID(ctx, parentID)
	if err != nil || parentBinding == nil {
		if s.logger != nil {
			s.logger.Warn("thread: child start parent codex identity lookup failed",
				"agent_id", req.AgentID,
				"parent_agent_id", parentID,
				"error", err)
		}
		return req
	}
	var injected bool
	req.Config, injected = injectParentCodexIdentity(req.Config, parentBinding)
	if s.logger != nil {
		s.logger.Warn("thread: child start parent codex identity lookup",
			"agent_id", req.AgentID,
			"parent_agent_id", parentID,
			"injected", injected,
			"parent_has_codex_home", strings.TrimSpace(parentBinding.CodexHome) != "",
			"parent_has_codex_instance_key", strings.TrimSpace(parentBinding.CodexInstanceKey) != "",
			"parent_has_codex_model_provider", strings.TrimSpace(parentBinding.CodexModelProvider) != "")
	}
	return req
}

// injectDefaultCodexIdentityForStart 为打包运行时的 Codex 启动请求补齐默认身份配置。
func (s *service) injectDefaultCodexIdentityForStart(req StartRequest) (StartRequest, error) {
	if strings.TrimSpace(req.Provider) != "codex" {
		return req, nil
	}
	allowed, err := contract.PackagedRuntimeFromEnv()
	if err != nil {
		return req, err
	}
	if !allowed {
		return req, nil
	}
	if req.Config == nil {
		req.Config = make(map[string]any)
	}
	if configutil.ConfigString(req.Config, "codexHome") == "" {
		home, err := contract.CanonicalAppManagedCodexHome()
		if err != nil {
			return req, err
		}
		req.Config["codexHome"] = home
	}
	if configutil.ConfigString(req.Config, "codexInstanceKey") == "" {
		req.Config["codexInstanceKey"] = defaultCodexInstanceKey
	}
	if configutil.ConfigString(req.Config, "codexModelProvider") == "" {
		req.Config["codexModelProvider"] = defaultCodexModelProvider
	}
	return req, nil
}

// injectDefaultCodexIdentityForResume 在恢复 Codex 会话前校验身份配置已完整存在。
func (s *service) injectDefaultCodexIdentityForResume(req ResumeRequest) (ResumeRequest, error) {
	if strings.TrimSpace(req.Provider) != "codex" {
		return req, nil
	}
	if err := validateResumeCodexIdentityPresentStrings(req); err != nil {
		return ResumeRequest{}, err
	}
	allowed, err := contract.PackagedRuntimeFromEnv()
	if err != nil {
		return req, err
	}
	if !allowed {
		return req, validateExplicitResumeCodexIdentity(req)
	}
	if strings.TrimSpace(req.CodexHome) == "" {
		if req.CodexHome, err = contract.CanonicalAppManagedCodexHome(); err != nil {
			return req, err
		}
	}
	if strings.TrimSpace(req.CodexInstanceKey) == "" {
		req.CodexInstanceKey = defaultCodexInstanceKey
	}
	if strings.TrimSpace(req.CodexModelProvider) == "" {
		req.CodexModelProvider = defaultCodexModelProvider
	}
	return req, validateExplicitResumeCodexIdentity(req)
}

// validateResumeCodexIdentityPresentStrings 只检查已出现的 resume 身份字段，默认补齐前不要求三元组完整。
func validateResumeCodexIdentityPresentStrings(req ResumeRequest) error {
	values := collectResumeCodexIdentityValues(req, req.Config)
	return errors.Join(
		validateResumeCodexIdentityPresentString(values.home, values.hasHome, contract.CodexHomeKey, contract.ErrCodexHomeRequired),
		validateResumeCodexIdentityPresentString(values.instanceKey, values.hasInstanceKey, contract.CodexInstanceKeyKey, contract.ErrCodexInstanceKeyRequired),
		validateResumeCodexIdentityPresentString(values.modelProvider, values.hasModelProvider, contract.CodexModelProviderKey, contract.ErrCodexModelProviderRequired),
	)
}

func validateResumeCodexIdentityPresentString(value any, present bool, key string, missing error) error {
	if !present {
		return nil
	}
	_, err := requireResumeCodexIdentityString(value, true, key, missing)
	return err
}

// injectParentCodexIdentity 将父 binding 的 Codex 身份补入子线程 runtime 配置。
// 父级 home、instance key、model provider 任一缺失都不注入；调用方已显式设置的字段不会被覆盖。
func injectParentCodexIdentity(cfg map[string]any, parent *threadBindingRecord) (map[string]any, bool) {
	home := strings.TrimSpace(parent.CodexHome)
	instanceKey := strings.TrimSpace(parent.CodexInstanceKey)
	modelProvider := strings.TrimSpace(parent.CodexModelProvider)
	if home == "" || instanceKey == "" || modelProvider == "" {
		return cfg, false
	}
	if cfg == nil {
		cfg = make(map[string]any)
	}
	injected := false
	for key, value := range map[string]string{
		"codexHome":          home,
		"codexInstanceKey":   instanceKey,
		"codexModelProvider": modelProvider,
	} {
		if configutil.ConfigString(cfg, key) == "" {
			cfg[key], injected = value, true
		}
	}
	return cfg, injected
}

// resolveRelativeCWD 只保留调用方显式传入的工作目录。
// "." 表示当前进程目录，不写入 thread 状态，避免恢复时误当成稳定工作区。
func resolveRelativeCWD(cwd string) string {
	if cwd = strings.TrimSpace(cwd); cwd == "." {
		return ""
	}
	return cwd
}

func comparablePromptCWD(cwd string) string {
	if cwd = strings.TrimSpace(cwd); cwd == "" || cwd == "." {
		return ""
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

func promptWorktreeState(cwd string, cfg *contract.Config) (string, bool) {
	resolved := comparablePromptCWD(cwd)
	if resolved == "" {
		return "", false
	}
	return resolved, resolvePromptGitContext(resolved, "", cfg).IsWorktree
}

func promptResumeRestoreRequiresInvalidation(prevCWD, nextCWD string, cfg *contract.Config) bool {
	_, prevWorktree := promptWorktreeState(prevCWD, cfg)
	_, nextWorktree := promptWorktreeState(nextCWD, cfg)
	return prevWorktree || nextWorktree
}

func promptWorktreeSwitchRequiresInvalidation(prevCWD, nextCWD string, cfg *contract.Config) bool {
	prevResolved, prevWorktree := promptWorktreeState(prevCWD, cfg)
	nextResolved, nextWorktree := promptWorktreeState(nextCWD, cfg)
	if prevResolved == nextResolved {
		return false
	}
	return prevWorktree || nextWorktree
}

func (s *service) lookupBindingCWD(ctx context.Context, agentID string) string {
	store := s.threadBindingStorePort()
	if store == nil {
		return ""
	}
	binding, err := store.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if err != nil || binding == nil {
		return ""
	}
	s.rememberBindingRecord(binding)
	return strings.TrimSpace(binding.Cwd)
}

// ReadThreadStateRuntimeConfig 读取线程状态运行时配置。
func (s *service) ReadThreadStateRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return nil, err
	}
	offline, err := s.buildOfflineConfig(ctx, threadID, binding)
	if err != nil {
		return nil, err
	}
	return clone.RuntimeConfigMap(offline.Runtime), nil
}

func buildLaunchRequest(
	agentID,
	cwd,
	name,
	parentID,
	agentType,
	memoryScope,
	provider,
	model string,
) (LaunchAgentRequest, error) {
	exe, err := os.Executable()
	if err != nil {
		return LaunchAgentRequest{}, err
	}
	return LaunchAgentRequest{
		AgentID:     strings.TrimSpace(agentID),
		Name:        strings.TrimSpace(name),
		ParentID:    strings.TrimSpace(parentID),
		AgentType:   strings.TrimSpace(agentType),
		MemoryScope: strings.TrimSpace(memoryScope),
		Cwd:         strings.TrimSpace(cwd),
		Command:     []string{exe},
		Env:         launchConfigEnv(provider, model),
	}, nil
}

func launchConfigEnv(provider, model string) []string {
	var env []string
	if provider = strings.TrimSpace(provider); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		env = append(env, "AGENT_MODEL="+model)
	}
	return env
}

func (s *service) launchAgent(
	ctx context.Context,
	agentID,
	cwd,
	name,
	parentID,
	agentType,
	memoryScope,
	provider,
	model string,
) error {
	if s == nil {
		return errors.New("thread: service is not configured")
	}
	if s.orchestration == nil {
		return errors.New("thread: orchestration service is not configured")
	}
	req, err := buildLaunchRequest(agentID, cwd, name, parentID, agentType, memoryScope, provider, model)
	if err != nil {
		return err
	}
	return s.orchestration.LaunchAgent(ctx, req)
}

func (s *service) recoverAgent(
	ctx context.Context,
	agentID,
	cwd,
	name,
	parentID,
	agentType,
	memoryScope,
	provider,
	model string,
) error {
	if s.orchestration == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if err := s.orchestration.Recover(ctx, agentID); err == nil {
		return nil
	}
	return s.launchAgent(ctx, agentID, cwd, name, parentID, agentType, memoryScope, provider, model)
}

func bindingPublicThreadID(binding *threadBindingStoreRecord, fallback string) string {
	if binding == nil {
		return strings.TrimSpace(fallback)
	}
	return util.FirstNonEmpty(binding.CodexThreadID, fallback)
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return time.Now().Unix()
}

func (s *service) maybeRegisterThreadBinding(
	ctx context.Context,
	state threadState,
	updateBinding bool,
) (bindingWriteOutcome, error) {
	if !updateBinding || s.threadBindingStorePort() == nil {
		return bindingWriteOutcome{}, nil
	}
	return s.registerThreadBinding(ctx, state)
}

func (s *service) persistStartedThread(
	ctx context.Context,
	state threadState,
	bindingOutcome bindingWriteOutcome,
) error {
	if err := s.upsertPublicThread(ctx, state, bindingOutcome); err != nil {
		return err
	}
	s.rememberStartedThread(state)
	s.publishThreadStarted(state)
	return nil
}

// upsertPublicThread 写入用户可见的 public thread 记录。
// 如果 thread store 写入失败，会回滚前面已经写成功的 binding，避免留下可恢复但列表不可见的半成品线程。
func (s *service) upsertPublicThread(
	ctx context.Context,
	state threadState,
	bindingOutcome bindingWriteOutcome,
) error {
	if s.threadStore == nil {
		return errors.New("thread: thread store is not configured")
	}
	displayName := strings.TrimSpace(util.FirstNonEmpty(state.Name, state.Prompt))
	err := s.upsertThread(ctx, threadConfigStoreRecord{
		ThreadID:         state.PublicThreadID,
		Name:             displayName,
		Prompt:           displayName,
		Model:            state.Model,
		Cwd:              state.CWD,
		Status:           statusCreated,
		CreatedAt:        state.CreatedAt,
		UpdatedAt:        time.Now().Unix(),
		OwnerThreadID:    state.OwnerThreadID,
		ParentAgentID:    state.ParentAgentID,
		AgentType:        state.AgentType,
		AgentMemoryScope: state.AgentMemoryScope,
		ConfigOverride:   clone.RawMessage(state.ConfigOverride),
		AgentKey:         state.AgentKey,
		PromptVersionID:  state.PromptVersionID,
	})
	if err == nil {
		return nil
	}
	if rollbackErr := s.rollbackThreadBinding(ctx, bindingOutcome); rollbackErr != nil {
		return errors.Join(err, rollbackErr)
	}
	return err
}

func (s *service) rememberStartedThread(state threadState) {
	s.rememberThreadAgent(state.PublicThreadID, state.AgentID)
	s.rememberThreadAgent(state.ProviderThreadID, state.AgentID)
}

func historyTargetID(binding *threadBindingStoreRecord, threadID string) string {
	requestedID := strings.TrimSpace(threadID)
	if binding == nil {
		return requestedID
	}
	publicThreadID := strings.TrimSpace(binding.CodexThreadID)
	agentID := strings.TrimSpace(binding.AgentID)
	if requestedID != "" && requestedID != publicThreadID && requestedID != agentID {
		return requestedID
	}
	return util.FirstNonEmpty(recoverableBindingProviderThreadID(binding), binding.ProviderThreadID, publicThreadID, agentID, requestedID)
}

func recoverableProviderThreadID(provider, providerUUID, publicThreadID, rolloutPath, codexHome string) string {
	providerThreadID := normalizeProviderThreadID(provider, providerUUID)
	if providerThreadID == "" || !identifier.LooksLikeUUID(providerThreadID) {
		return ""
	}
	if _, err := historyjsonl.ExistingProviderPath(historyjsonl.ReadRequest{
		Provider:         provider,
		RolloutPath:      rolloutPath,
		ThreadID:         publicThreadID,
		ProviderThreadID: providerThreadID,
		SessionUUID:      providerThreadID,
		CodexHome:        codexHome,
	}); err != nil {
		return ""
	}
	return providerThreadID
}

// recoverableBindingProviderThreadID 从 binding 中挑选可恢复的 provider thread UUID。
// 只有 provider_thread_id 或 session_uuid 同时看起来是 UUID 且本地历史存在时才返回，防止 resume 指向不存在的 provider 历史。
func recoverableBindingProviderThreadID(binding *threadBindingStoreRecord) string {
	if binding == nil {
		return ""
	}
	for _, candidate := range []string{binding.ProviderThreadID, binding.SessionUUID} {
		providerThreadID := normalizeProviderThreadID(binding.Provider, candidate)
		if providerThreadID == "" || !identifier.LooksLikeUUID(providerThreadID) {
			continue
		}
		if bindingHasProviderHistoryForUUID(binding, providerThreadID) {
			return providerThreadID
		}
	}
	return ""
}

func bindingHasProviderHistoryForUUID(binding *threadBindingStoreRecord, providerThreadID string) bool {
	if binding == nil {
		return false
	}
	providerThreadID = normalizeProviderThreadID(binding.Provider, providerThreadID)
	if providerThreadID == "" || !identifier.LooksLikeUUID(providerThreadID) {
		return false
	}
	_, err := historyjsonl.ExistingProviderPath(historyjsonl.ReadRequest{
		Provider:         binding.Provider,
		RolloutPath:      binding.RolloutPath,
		ThreadID:         binding.CodexThreadID,
		ProviderThreadID: providerThreadID,
		SessionUUID:      providerThreadID,
		CodexHome:        binding.CodexHome,
	})
	return err == nil
}

func toRef(thread threadConfigStoreRecord) Ref {
	name := strings.TrimSpace(util.FirstNonEmpty(thread.Name, thread.Prompt))
	return Ref{
		ID:        strings.TrimSpace(thread.ThreadID),
		Name:      name,
		AgentID:   strings.TrimSpace(thread.AgentID),
		Status:    strings.TrimSpace(thread.Status),
		CreatedAt: thread.CreatedAt, UpdatedAt: thread.UpdatedAt,
		CWD:   strings.TrimSpace(thread.Cwd),
		Model: strings.TrimSpace(thread.Model),
		Port:  int(thread.Port),
	}
}

func normalizeThreadID(threadID string) (string, error) {
	if id := strings.TrimSpace(threadID); id != "" {
		return id, nil
	}
	return "", errors.New("thread id is required")
}

func agentIDFromBinding(binding *threadBindingStoreRecord, fallback string) string {
	if binding != nil {
		if agentID := strings.TrimSpace(binding.AgentID); agentID != "" {
			return agentID
		}
	}
	return strings.TrimSpace(fallback)
}

func pageCount(totalCount, limit int) int {
	if totalCount <= 0 {
		return 0
	}
	if limit <= 0 || totalCount <= limit {
		return 1
	}
	pages := totalCount / limit
	if totalCount%limit != 0 {
		pages++
	}
	return pages
}

type messagePageReaderSession interface {
	ReadMessagesPage(ctx context.Context, threadID string, req dto.MessagePageRequest) (dto.MessagePageResult, error)
}

func (s *service) readMessagesPageSource(ctx context.Context, threadID string, binding *threadBindingStoreRecord, req dto.MessagePageRequest) (dto.MessagePageResult, error) {
	req.Limit = normalizeMessagesPageLimit(req.Limit)
	req.Before = strings.TrimSpace(req.Before)
	session, err := s.sessionForBinding(binding)
	if err == nil && session != nil {
		return s.readMessagesPageFromSession(ctx, threadID, binding, session, req)
	}
	if err == nil {
		err = contract.ErrSessionNotFound
	} else if !errors.Is(err, contract.ErrSessionNotFound) {
		return dto.MessagePageResult{}, err
	}
	historyReq := readMessagesHistoryRequestForSession(threadID, binding, nil)
	return historyjsonl.ReadProviderMessagesPageOrError(historyReq, req, err)
}

// readMessagesPageFromSession 从会话读取消息page。
func (s *service) readMessagesPageFromSession(ctx context.Context, threadID string, binding *threadBindingStoreRecord, session contract.Session, req dto.MessagePageRequest) (dto.MessagePageResult, error) {
	targetID := historyTargetID(binding, threadID)
	if pager, ok := session.(messagePageReaderSession); ok {
		return pager.ReadMessagesPage(ctx, targetID, req)
	}
	historyReq := readMessagesHistoryRequestForSession(threadID, binding, session)
	page, pageErr := historyjsonl.ReadProviderMessagesPage(historyReq, req)
	if pageErr == nil {
		return page, nil
	}
	if req.Before != "" || !historyjsonl.IsMissingProviderHistory(pageErr) {
		return dto.MessagePageResult{}, pageErr
	}
	messages, err := session.ReadHistory(ctx, targetID, req.Limit)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	return dto.MessagePageResult{Messages: messages}, nil
}

func readMessagesHistoryRequestForSession(threadID string, binding *threadBindingStoreRecord, session contract.Session) historyjsonl.ReadRequest {
	req := historyjsonl.ReadRequest{ThreadID: strings.TrimSpace(threadID)}
	if binding != nil {
		sessionID := ""
		if session != nil {
			sessionID = strings.TrimSpace(session.ThreadID())
		}
		req = historyjsonl.ReadRequest{
			Provider:         binding.Provider,
			RolloutPath:      binding.RolloutPath,
			ThreadID:         util.FirstNonEmpty(binding.CodexThreadID, sessionID, threadID),
			ProviderThreadID: binding.ProviderThreadID,
			SessionUUID:      util.FirstNonEmpty(binding.SessionUUID, sessionID),
			CodexHome:        binding.CodexHome,
		}
	}
	return req
}

func (s *service) sessionForBinding(binding *threadBindingStoreRecord) (contract.Session, error) {
	if binding == nil {
		return nil, errors.New("thread binding is not configured")
	}
	if s.sessions == nil {
		return nil, errors.New("session provider is not configured")
	}
	return s.sessions.GetSession(strings.TrimSpace(binding.AgentID))
}
