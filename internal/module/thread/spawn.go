package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idempotency"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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

// startPendingThread 启动待处理线程。
func (s *service) startPendingThread(ctx context.Context, req StartRequest, agentID string) (StartResult, error) {
	if s == nil || s.threadStore == nil {
		return StartResult{}, errors.New("thread store is not configured")
	}
	createdAt := time.Now().Unix()
	displayName := resolveDisplayName(ctx, s.threadStore, agentID, req.Prompt, req.Name)
	pendingStored := storedThreadConfig{
		Model:           strings.TrimSpace(req.Model),
		Effort:          strings.TrimSpace(req.Effort),
		Approvals:       strings.TrimSpace(req.ApprovalPolicy),
		Personality:     strings.TrimSpace(req.Personality),
		Provider:        strings.TrimSpace(req.Provider),
		PromptKey:       strings.TrimSpace(req.PromptKey),
		AgentKey:        strings.TrimSpace(req.AgentKey),
		ToolSurfaceMode: strings.TrimSpace(req.ToolSurfaceMode),
		Runtime:         clone.RuntimeConfigMap(req.Config),
	}
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
	if err := s.threadStore.Upsert(ctx, newThreadUpsertParams(threadstore.Thread{
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
	})); err != nil {
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

// isThreadPendingLaunch 判断线程待处理启动是否可用。
func (s *service) isThreadPendingLaunch(ctx context.Context, threadID string) (bool, error) {
	if s == nil || s.threadStore == nil {
		return false, nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, nil
	}
	row, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return noPendingLaunch()
		}
		return false, err
	}
	if row == nil {
		return false, nil
	}
	return row.PendingLaunch, nil
}

func noPendingLaunch() (bool, error) {
	return false, nil
}

func (s *service) acquirePendingLaunchLock(threadID string) *sync.Mutex {
	m, _ := s.pendingLaunchMu.LoadOrStore(threadID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// SpawnIfNeeded 处理spawnifneeded。
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
	if s == nil || s.threadStore == nil {
		return errors.New("thread store is not configured")
	}
	return nil
}

// loadPendingLaunchRow 加载待处理启动row。
func (s *service) loadPendingLaunchRow(ctx context.Context, threadID string) (*threadstore.Thread, bool, error) {
	row, err := s.threadStore.GetByThreadID(ctx, threadID)
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

// buildPendingSpawnRequest 构建待处理spawn请求。
func buildPendingSpawnRequest(row *threadstore.Thread, agentID, userInputForRouter, requestCWD string) (StartRequest, error) {
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

// cleanupFailedPendingLaunch 处理cleanupfailed待处理启动。
func (s *service) cleanupFailedPendingLaunch(ctx context.Context, threadID, agentID string, cause error) error {
	if cause == nil || s == nil || s.threadStore == nil {
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

// runPendingSpawn 运行待处理spawn。
func (s *service) runPendingSpawn(
	ctx context.Context,
	req *StartRequest,
	row *threadstore.Thread,
	agentID, threadID string,
) error {
	if req.PromptAssemblyRef == nil {
		req.PromptAssemblyRef = s.promptAssembly
	}
	snapshot := *req
	parallelStart := time.Now()

	g, gCtx := errgroup.WithContext(ctx)
	var assemblyInput contract.StartInput
	var cleanupScratchpad func()

	g.Go(func() error { return s.resolveRoutedPrompt(gCtx, req) })
	g.Go(func() error {
		var aerr error
		assemblyInput, cleanupScratchpad, aerr = s.buildStartAssemblyInput(gCtx, snapshot, agentID)
		return aerr
	})
	if err := g.Wait(); err != nil {
		if cleanupScratchpad != nil {
			cleanupScratchpad()
		}
		return fmt.Errorf("thread: assembly input: %w", err)
	}
	foldRouterOutputIntoAssemblyInput(&assemblyInput, req)
	pkglogger.Info("thread: pending spawn parallel prep done",
		"duration_ms", time.Since(parallelStart).Milliseconds(),
		"prompt_key", req.PromptKey,
		"agent_key", req.AgentKey)

	agentLaunched := false
	cleanupOnFailure := true
	defer cleanupPendingSpawn(ctx, s, &cleanupOnFailure, cleanupScratchpad, &agentLaunched, agentID)
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
	publishPendingSpawnLaunched(s, req, row, session, agentID, threadID, displayName)
	return nil
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

// cleanupPendingSpawn 处理cleanup待处理spawn。
func cleanupPendingSpawn(
	ctx context.Context,
	s *service,
	active *bool,
	cleanupScratchpad func(),
	agentLaunched *bool,
	agentID string,
) {
	if active == nil || !*active {
		return
	}
	if cleanupScratchpad != nil {
		cleanupScratchpad()
	}
	if agentLaunched != nil && *agentLaunched {
		s.stopAgent(ctx, agentID)
	}
}

// publishPendingSpawnLaunched emits thread.launched after pending spawn commit.
// publishPendingSpawnLaunched 发布待处理spawnlaunched。
func publishPendingSpawnLaunched(
	s *service,
	req *StartRequest,
	row *threadstore.Thread,
	session contract.Session,
	agentID, threadID, displayName string,
) {
	effectiveModel, effectiveCWD, _ := enrichFromSessionConfig(session, req.Model, req.CWD)
	providerUUID := resolvedProviderUUID(session)
	rolloutPath := session.RolloutPath()
	spawnedState := newThreadState(threadStateStartKind, threadStateFields{
		PublicThreadID:   threadID,
		AgentID:          agentID,
		ParentAgentID:    req.ParentAgentID,
		AgentType:        req.AgentType,
		AgentMemoryScope: req.AgentMemoryScope,
		ProviderThreadID: recoverableProviderThreadID(req.Provider, providerUUID, threadID, rolloutPath, ""),
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
}

const (
	temporarySubagentTool = "spawn_agent"
	managedSubagentTool   = "orchestration_launch_agent"
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

// applyPersistentSubagentToolPolicy 应用persistentsubagent工具策略。
func applyPersistentSubagentToolPolicy(enabledTools []string, flags map[string]bool) []string {
	if !persistentSubagentDefaultEnabled(flags) || len(enabledTools) == 0 {
		return enabledTools
	}
	hasManaged := false
	hasSpawn := false
	for _, tool := range enabledTools {
		switch strings.TrimSpace(tool) {
		case managedSubagentTool:
			hasManaged = true
		case temporarySubagentTool:
			hasSpawn = true
		}
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

// applyTitleExtractionFallback updates the display name by extracting a title from
// the user prompt if the thread is currently unnamed or holds the fallback title.
func applyTitleExtractionFallback(displayName, prompt string) string {
	if displayName == "" || displayName == "新对话" {
		if ext := ExtractTitle(prompt); ext != "" {
			return ext
		}
	}
	return displayName
}
