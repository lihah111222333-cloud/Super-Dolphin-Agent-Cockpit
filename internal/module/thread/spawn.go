package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idempotency"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func isPendingLaunchIntent(req StartRequest) bool {
	if !req.DeferSpawn {
		return false
	}
	if strings.TrimSpace(req.Prompt) != "" {
		return false
	}
	if strings.TrimSpace(req.BaseInstructions) != "" {
		return false
	}
	if strings.TrimSpace(req.DeveloperInstructions) != "" {
		return false
	}
	return true
}

// pendingLaunchRuntimeConfig 构造待启动线程首轮 spawn 会复用的 runtime 配置。
// 顶层 sandbox 在 eager 路径由 buildStartSessionConfig 写入；pending 路径必须落库前补齐，避免真实启动时丢掉权限设置。
func pendingLaunchRuntimeConfig(req StartRequest) map[string]any {
	runtimeConfig := clone.RuntimeConfigMap(req.Config)
	if rawSandbox := trimRawJSON(req.Sandbox); len(rawSandbox) > 0 {
		if runtimeConfig == nil {
			runtimeConfig = map[string]any{}
		}
		putConfigJSON(runtimeConfig, "sandbox", rawSandbox)
		if len(runtimeConfig) == 0 {
			return nil
		}
	}
	return runtimeConfig
}

// buildPendingStoredThreadConfig 生成 pending_launch 落库配置。
// 这里同时保存顶层 sandbox 和 runtime 快照，确保首轮 SpawnIfNeeded 可恢复启动权限与其它 provider 配置。
func buildPendingStoredThreadConfig(req StartRequest) storedThreadConfig {
	return storedThreadConfig{
		Model:           strings.TrimSpace(req.Model),
		Effort:          strings.TrimSpace(req.Effort),
		Approvals:       strings.TrimSpace(req.ApprovalPolicy),
		Personality:     strings.TrimSpace(req.Personality),
		Sandbox:         clone.RawMessage(req.Sandbox),
		Provider:        strings.TrimSpace(req.Provider),
		PromptKey:       strings.TrimSpace(req.PromptKey),
		AgentKey:        strings.TrimSpace(req.AgentKey),
		ToolSurfaceMode: strings.TrimSpace(req.ToolSurfaceMode),
		Runtime:         pendingLaunchRuntimeConfig(req),
	}
}

// startPendingThread 写入 pending_launch 线程，但不 fork provider 进程。
// 它持久化首轮启动所需配置，并立即发布 started，让 UI 能展示“待输入后启动”的线程。
func (s *service) startPendingThread(ctx context.Context, req StartRequest, agentID string) (StartResult, error) {
	if s == nil || s.threadStore == nil {
		return StartResult{}, errors.New("thread store is not configured")
	}
	createdAt := time.Now().UnixMilli()
	displayName := resolveDisplayName(ctx, s.threadStore, agentID, req.Prompt, req.Name)
	pendingStored := buildPendingStoredThreadConfig(req)
	configOverride, err := encodeStoredThreadConfig(pendingStored)
	if err != nil {
		return StartResult{}, fmt.Errorf("thread: encode pending config: %w", err)
	}
	state := newThreadState(threadStateStartKind, threadStateFields{
		AgentID:          agentID,
		ParentAgentID:    req.ParentAgentID,
		AgentType:        req.AgentType,
		AgentMemoryScope: req.AgentMemoryScope,
		Provider:         req.Provider,
		CWD:              req.CWD,
		Model:            req.Model,
		Name:             displayName,
		Prompt:           displayName,
		ConfigOverride:   configOverride,
		CreatedAt:        createdAt,
		AgentKey:         strings.TrimSpace(req.AgentKey),
		OwnerThreadID:    req.OwnerThreadID,
		PendingLaunch:    true,
	})
	if err := s.upsertThread(ctx, threadConfigStoreRecord{
		ThreadID:         state.PublicThreadID,
		Name:             state.Name,
		Prompt:           state.Prompt,
		Model:            state.Model,
		Cwd:              state.CWD,
		Status:           statusCreated,
		CreatedAt:        state.CreatedAt,
		UpdatedAt:        createdAt,
		OwnerThreadID:    state.OwnerThreadID,
		ParentAgentID:    state.ParentAgentID,
		AgentType:        state.AgentType,
		AgentMemoryScope: state.AgentMemoryScope,
		ConfigOverride:   state.ConfigOverride,
		AgentKey:         state.AgentKey,
		PromptVersionID:  nil,
		PendingLaunch:    true,
	}); err != nil {
		return StartResult{}, fmt.Errorf("thread: upsert pending_launch row: %w", err)
	} else if intentID := strings.TrimSpace(req.LaunchIntentID); intentID != "" {
		s.launchIntentByThread.Store(state.PublicThreadID, intentID)
	}
	s.publishThreadStarted(state)
	return StartResult{
		ThreadID:      state.PublicThreadID,
		AgentID:       agentID,
		SessionID:     state.PublicThreadID,
		Status:        statusCreated,
		Model:         state.Model,
		Provider:      req.Provider,
		ModelProvider: req.ModelProvider,
		CWD:           state.CWD,
		PendingLaunch: true,
		AgentKey:      state.AgentKey,
		PromptKey:     pendingStored.PromptKey,
	}, nil
}

// isThreadPendingLaunch 判断 thread store 中的线程是否仍处于 pending_launch。
// store 未装配、thread 为空或 id 缺失都按非 pending 处理，实际写路径仍由调用方 fail-fast。
func (s *service) isThreadPendingLaunch(ctx context.Context, threadID string) (bool, error) {
	store := s.threadConfigStorePort()
	if store == nil {
		return false, nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, nil
	}
	row, err := store.GetByThreadID(ctx, threadID)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if row == nil {
		return false, nil
	}
	return row.PendingLaunch, nil
}

func (s *service) acquirePendingLaunchLock(threadID string) *sync.Mutex {
	m, _ := s.pendingLaunchMu.LoadOrStore(threadID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// SpawnIfNeeded 在 pending_launch 线程收到首个真实用户输入时拉起 provider 会话。
// 同一 thread_id 通过专属锁串行化，launch intent 的已知错误会直接返回，失败后会标记线程并发布停止事件。
func (s *service) SpawnIfNeeded(ctx context.Context, threadID, userInputForRouter, requestCWD string) (launched bool, routing SpawnRouting, err error) {
	ctx = util.NonNilContext(ctx)
	threadID = strings.TrimSpace(threadID)
	span := s.beginThreadTraceSpan(ctx, "thread.spawn_if_needed", threadID, threadID, platformobs.NewCodeAnchor("internal/module/thread/spawn.go", "thread.(*service).SpawnIfNeeded", 138), nil)
	ctx = span.ctx
	defer func() { s.finishThreadTraceSpan(span, err) }()
	if err := validateSpawnIfNeededInputs(s, threadID); err != nil {
		return false, SpawnRouting{}, err
	}

	mu := s.acquirePendingLaunchLock(threadID)
	mu.Lock()
	defer mu.Unlock()
	if err, ok := idempotency.MappedError(&s.launchIntentByThread, &s.launchIntentRegistry, threadID); ok {
		return false, SpawnRouting{}, err
	}
	row, needSpawn, err := s.loadPendingLaunchRow(ctx, threadID)
	if err != nil || !needSpawn {
		return false, SpawnRouting{}, err
	}

	agentID := threadID
	req, err := buildPendingSpawnRequest(row, agentID, userInputForRouter, requestCWD)
	if err != nil {
		return false, SpawnRouting{}, s.cleanupFailedPendingLaunch(ctx, threadID, agentID, err)
	}
	req = s.injectParentCodexIdentityForStart(ctx, req)
	req, err = s.injectDefaultCodexIdentityForStart(req)
	if err != nil {
		return false, SpawnRouting{}, s.cleanupFailedPendingLaunch(ctx, threadID, agentID, err)
	}
	if err := s.runPendingSpawn(ctx, &req, row, agentID, threadID); err != nil {
		return false, SpawnRouting{}, s.cleanupFailedPendingLaunch(ctx, threadID, agentID, err)
	}
	return true, SpawnRouting{
		AgentKey:        req.AgentKey,
		AgentTitle:      req.AgentTitle,
		PromptKey:       req.PromptKey,
		PromptVersionID: req.PromptVersionID,
		PromptKeyStale:  req.PromptKeyStale,
	}, nil
}

func validateSpawnIfNeededInputs(s *service, threadID string) error {
	if threadID == "" {
		return errors.New("thread: SpawnIfNeeded requires thread_id")
	}
	if s == nil || s.threadConfigStorePort() == nil {
		return errors.New("thread store is not configured")
	}
	return nil
}

// loadPendingLaunchRow 读取待启动线程并判断是否仍需要 spawn。
// 非 pending 或状态已变化返回 needSpawn=false，让重复首轮请求保持幂等。
func (s *service) loadPendingLaunchRow(ctx context.Context, threadID string) (*threadConfigStoreRecord, bool, error) {
	row, err := s.getThread(ctx, threadID)
	if err != nil {
		if contract.IsNotFound(err) {
			return nil, false, fmt.Errorf("thread: %w", err)
		}
		return nil, false, fmt.Errorf("thread: load pending row: %w", err)
	}
	if row == nil || !row.PendingLaunch {
		return nil, false, nil
	}
	if row.Status != statusCreated && row.Status != "" {
		return nil, false, nil
	}
	return row, true, nil
}

// buildPendingSpawnRequest 从 pending row 和首轮输入重建 StartRequest。
// 存储 CWD 是权威值，请求 CWD 只用于校验；若规范化后 agent id 被改写，直接报错阻断错误绑定。
func buildPendingSpawnRequest(row *threadConfigStoreRecord, agentID, userInputForRouter, requestCWD string) (StartRequest, error) {
	cwd, err := resolvePendingLaunchCWD(row.Cwd, requestCWD)
	if err != nil {
		return StartRequest{}, err
	}
	storedCfg, err := decodeStoredThreadConfig(row.ConfigOverride)
	if err != nil {
		return StartRequest{}, err
	}
	req := StartRequest{
		AgentID:          agentID,
		ParentAgentID:    row.ParentAgentID,
		AgentType:        row.AgentType,
		AgentMemoryScope: row.AgentMemoryScope,
		CWD:              cwd,
		Model:            util.FirstNonEmpty(storedCfg.Model, row.Model),
		Name:             row.Prompt,
		Prompt:           strings.TrimSpace(userInputForRouter),
		OwnerThreadID:    row.OwnerThreadID,
		Provider:         storedCfg.Provider,
		Effort:           storedCfg.Effort,
		Personality:      storedCfg.Personality,
		ApprovalPolicy:   storedCfg.Approvals,
		Sandbox:          clone.RawMessage(storedCfg.Sandbox),
		AgentKey:         util.FirstNonEmpty(storedCfg.AgentKey, row.AgentKey),
		PromptKey:        storedCfg.PromptKey,
		ToolSurfaceMode:  storedCfg.ToolSurfaceMode,
		Config:           clone.RuntimeConfigMap(storedCfg.Runtime),
	}
	normalized, normalizedAgentID, err := normalizeStartRequest(req)
	if err != nil {
		return StartRequest{}, fmt.Errorf("thread: normalize pending spawn: %w", err)
	}
	if normalizedAgentID != agentID {
		return StartRequest{}, fmt.Errorf("thread: normalize rewrote agent_id (%s -> %s); pending row is tied to the original id", agentID, normalizedAgentID)
	}
	return normalized, nil
}

// cleanupFailedPendingLaunch 处理 pending_launch spawn 失败后的状态收口。
// 带 launch intent 的线程会记录保留错误、标记 failed 并发布 stopped；无 intent 的早期失败只释放进程内锁。
func (s *service) cleanupFailedPendingLaunch(ctx context.Context, threadID, agentID string, cause error) error {
	if cause == nil || s == nil || s.threadConfigStorePort() == nil {
		return cause
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return cause
	}
	_, hasLaunchIntent := s.launchIntentByThread.Load(threadID)
	if !hasLaunchIntent {
		s.pendingLaunchMu.Delete(threadID)
		return cause
	}
	s.retainLaunchIntentFailure(threadID, cause)
	if err := s.updateThreadStatus(ctx, threadID, statusFailed); err != nil {
		cause = idempotency.Retain(errors.Join(cause, fmt.Errorf("thread: mark failed pending_launch row %q: %w", threadID, err)))
		s.retainLaunchIntentFailure(threadID, cause)
		return cause
	}
	s.pendingLaunchMu.Delete(threadID)
	s.publishThreadStopped(threadID, agentID, statusFailed, cause.Error())
	return cause
}

func (s *service) retainLaunchIntentFailure(threadID string, cause error) {
	if s == nil || cause == nil {
		return
	}
	if intentID, ok := s.launchIntentByThread.Load(strings.TrimSpace(threadID)); ok {
		s.launchIntentRegistry.RetainError(intentID.(string), idempotency.Retain(cause))
	}
}

func resolvePendingLaunchCWD(storedCWD, requestCWD string) (string, error) {
	stored := strings.TrimSpace(storedCWD)
	if stored == "" || stored == "." {
		return "", errors.New("thread: pending launch cwd is required")
	}
	requested := strings.TrimSpace(requestCWD)
	if requested != "" && comparablePromptCWD(stored) != comparablePromptCWD(requested) {
		return "", fmt.Errorf("thread: pending launch cwd mismatch: stored cwd %q request cwd %q", stored, requested)
	}
	return stored, nil
}

// runPendingSpawn 执行 pending_launch 的真实启动流程。
// prompt 路由和 assembly 并行准备；provider 启动后必须成功持久化 session，失败路径会关闭已拉起 agent 并清理 scratchpad。
func (s *service) runPendingSpawn(
	ctx context.Context,
	req *StartRequest,
	row *threadConfigStoreRecord,
	agentID, threadID string,
) (err error) {
	if req.PromptAssemblyRef == nil {
		req.PromptAssemblyRef = s.promptAssembly
	}
	snapshot := *req
	parallelStart := time.Now()

	g, gCtx := errgroup.WithContext(ctx)
	var assemblyInput contract.StartInput
	var cleanupScratchpad func() error

	g.Go(func() error { return s.resolveRoutedPrompt(gCtx, req) })
	g.Go(func() error {
		var aerr error
		assemblyInput, cleanupScratchpad, aerr = s.buildStartAssemblyInput(gCtx, snapshot, agentID)
		return aerr
	})
	if err := g.Wait(); err != nil {
		prepErr := fmt.Errorf("thread: assembly input: %w", err)
		if cleanupScratchpad != nil {
			if cleanupErr := cleanupScratchpad(); cleanupErr != nil {
				prepErr = errors.Join(prepErr, cleanupErr)
			}
		}
		return prepErr
	}
	foldRouterOutputIntoAssemblyInput(&assemblyInput, req)
	pkglogger.Info("thread: pending spawn parallel prep done",
		"duration_ms", time.Since(parallelStart).Milliseconds(),
		"prompt_key", req.PromptKey,
		"agent_key", req.AgentKey)

	agentLaunched := false
	cleanupOnFailure := true
	defer func() {
		if cleanupErr := cleanupPendingSpawn(ctx, s, &cleanupOnFailure, cleanupScratchpad, &agentLaunched, agentID); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	assembly, err := resolveStartPromptAssembly(ctx, *req, assemblyInput)
	if err != nil {
		return fmt.Errorf("thread: prompt assembly: %w", err)
	}
	displayName := resolveDisplayName(ctx, s.threadStore, agentID, req.Prompt, assembly.DisplayName)
	displayName = applyTitleExtractionFallback(displayName, req.Prompt)
	displayName = prependAgentBadge(displayName, req.AgentTitle, req.AgentKey)
	if err := s.launchAgent(ctx, agentID, req.CWD, displayName, req.ParentAgentID, req.AgentType, req.AgentMemoryScope, req.Provider, req.Model); err != nil {
		return idempotency.Retain(fmt.Errorf("thread: launch agent: %w", err))
	}
	agentLaunched = true
	session, err := s.establishStartedSession(ctx, *req, assemblyInput, assembly, agentID)
	if err != nil {
		return fmt.Errorf("thread: establish session: %w", err)
	}
	if _, err := s.persistStartedSession(ctx, *req, assemblyInput, assembly, agentID, displayName, session); err != nil {
		return fmt.Errorf("thread: persist launched session: %w", err)
	}
	cleanupOnFailure = false
	s.pendingLaunchMu.Delete(threadID)
	return publishPendingSpawnLaunched(ctx, s, req, row, session, agentID, threadID, displayName)
}

func foldRouterOutputIntoAssemblyInput(assemblyInput *contract.StartInput, req *StartRequest) {
	if assemblyInput == nil || req == nil {
		return
	}
	assemblyInput.BaseInstructions = req.BaseInstructions
	assemblyInput.DeveloperInstructions = req.DeveloperInstructions
	assemblyInput.PromptKey = strings.TrimSpace(req.PromptKey)
	assemblyInput.BaseInstructionBlocks = append(
		[]contract.BaseInstructionBlock(nil),
		req.BaseInstructionBlocks...,
	)
}

func prependAgentBadge(displayName, agentTitle, agentKey string) string {
	key := strings.TrimSpace(agentKey)
	if key == "" || strings.EqualFold(key, "main") {
		return displayName
	}
	label := strings.TrimSpace(agentTitle)
	if label == "" {
		label = key
	}
	prefix := "[" + label + "] "
	if strings.HasPrefix(displayName, prefix) {
		return displayName
	}
	return prefix + displayName
}

// cleanupPendingSpawn 是 runPendingSpawn 的失败清理钩子。
// active 为真时释放临时 scratchpad；若 provider 已经启动但持久化未完成，还会停止新 agent 防止孤儿进程。
func cleanupPendingSpawn(
	ctx context.Context,
	s *service,
	active *bool,
	cleanupScratchpad func() error,
	agentLaunched *bool,
	agentID string,
) error {
	if active == nil || !*active {
		return nil
	}
	var cleanupErr error
	if cleanupScratchpad != nil {
		cleanupErr = cleanupScratchpad()
	}
	if agentLaunched != nil && *agentLaunched {
		s.stopAgent(ctx, agentID)
	}
	return cleanupErr
}

// publishPendingSpawnLaunched 在 pending spawn 持久化完成后发布 thread.launched。
// 事件使用 session 中解析出的 provider 身份和有效配置，保证 UI 收到的是可恢复的最终线程状态。
func publishPendingSpawnLaunched(
	ctx context.Context,
	s *service,
	req *StartRequest,
	row *threadConfigStoreRecord,
	session contract.Session,
	agentID, threadID, displayName string,
) error {
	effectiveModel, effectiveCWD, _ := enrichFromSessionConfig(session, req.Model, req.CWD)
	providerUUID := resolvedProviderUUID(session)
	rolloutPath := session.RolloutPath()
	binding, err := s.threadBindingStorePort().GetByAgentID(ctx, agentID)
	if err != nil {
		return fmt.Errorf("thread: read persisted pending spawn binding: %w", err)
	}
	if binding == nil {
		return errors.New("thread: persisted pending spawn binding is required")
	}
	providerThreadID := strings.TrimSpace(binding.ProviderThreadID)
	spawnedState := newThreadState(threadStateStartKind, threadStateFields{
		PublicThreadID:   threadID,
		AgentID:          agentID,
		ParentAgentID:    req.ParentAgentID,
		AgentType:        req.AgentType,
		AgentMemoryScope: req.AgentMemoryScope,
		ProviderThreadID: providerThreadID,
		Provider:         req.Provider,
		CWD:              effectiveCWD,
		Model:            effectiveModel,
		Name:             displayName,
		Prompt:           displayName,
		RolloutPath:      rolloutPath,
		SessionUUID:      providerUUID,
		CreatedAt:        row.CreatedAt,
		AgentKey:         req.AgentKey,
		PromptVersionID:  req.PromptVersionID,
		OwnerThreadID:    req.OwnerThreadID,
	})
	s.publishThreadLaunched(spawnedState)
	return nil
}

const (
	temporarySubagentTool = "spawn_agent"
)

func persistentSubagentDefaultEnabled(flags map[string]bool) bool {
	if len(flags) == 0 {
		return false
	}
	for name, enabled := range flags {
		if !enabled {
			continue
		}
		switch normalizeSessionFlagName(name) {
		case "persistentsubagentdefault", "managedsubagentdefault", "uipersistentsubagentdefault":
			return true
		}
	}
	return false
}

// applyPersistentSubagentToolPolicy 在 managed 子代理默认开启时隐藏临时 spawn_agent 工具。
// 只有两个工具同时存在才移除临时入口，避免缺少 managed 工具时把唯一可用入口删掉。
func applyPersistentSubagentToolPolicy(enabledTools []string, flags map[string]bool) []string {
	if !persistentSubagentDefaultEnabled(flags) || len(enabledTools) == 0 {
		return enabledTools
	}
	hasManaged := false
	hasSpawn := false
	for _, tool := range enabledTools {
		managed, spawn := subagentToolPolicyFlags(tool)
		hasManaged = hasManaged || managed
		hasSpawn = hasSpawn || spawn
	}
	if !hasManaged || !hasSpawn {
		return enabledTools
	}
	filtered := make([]string, 0, len(enabledTools)-1)
	for _, tool := range enabledTools {
		if strings.TrimSpace(tool) == temporarySubagentTool {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func subagentToolPolicyFlags(tool string) (managed, spawn bool) {
	switch strings.TrimSpace(tool) {
	case temporarySubagentTool:
		return false, true
	default:
		return contract.IsOrchestrationLaunchTool(tool), false
	}
}

// applyTitleExtractionFallback 在默认标题下尝试从首轮 prompt 提取展示名。
// 已有用户标题保持不变，避免 pending spawn 完成时覆盖手动命名。
func applyTitleExtractionFallback(displayName, prompt string) string {
	if displayName == "" || displayName == "新对话" {
		if ext := ExtractTitle(prompt); ext != "" {
			return ext
		}
	}
	return displayName
}
