package claudecli

import (
	"context"
	"reflect"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
)

type logWatcherIdentity struct {
	sessionID string
	threadID  string
}

type logWatcherStartState struct {
	identity   logWatcherIdentity
	generation uint64
	history    *historyBackend
	oldWatcher *sessionLogWatcher
}

type stagedSessionState struct {
	model                    string
	displayModel             string
	config                   cliLaunchConfig
	manifest                 dto.MCPManifest
	settingsChanged          bool
	appliedPendingModel      bool
	appliedPendingModelText  string
	appliedPendingEffort     bool
	appliedPendingEffortText string
}

func (s *session) detachLogWatcherLocked() *sessionLogWatcher {
	if s == nil {
		return nil
	}
	watcher := s.logWatcher
	s.logWatcher = nil
	s.logWatcherGen++
	return watcher
}

func (s *session) setContextWindowForTransport(tr *transport, contextWindow int) {
	if s == nil || contextWindow <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transport != tr {
		return
	}
	s.sessionContextWindow = contextWindow
}

func (s *session) startLogWatcherIfCurrent(tr *transport) {
	state, ok := s.prepareLogWatcherStart(tr)
	if !ok {
		return
	}
	if state.oldWatcher != nil {
		state.oldWatcher.stopAndWait()
	}
	watcher := s.newCurrentLogWatcher(tr, state)
	watcher.start()
	if s.installLogWatcherIfCurrent(tr, state, watcher) {
		return
	}
	watcher.stopAndWait()
}

// prepareLogWatcherStart 准备日志watcher起点。
func (s *session) prepareLogWatcherStart(tr *transport) (logWatcherStartState, bool) {
	if s == nil || tr == nil || s.history == nil {
		return logWatcherStartState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := logWatcherIdentity{
		sessionID: strings.TrimSpace(s.sessionID),
		threadID:  strings.TrimSpace(s.threadID),
	}
	if s.transport != tr || identity.sessionID == "" || !identifier.IsClaudeCLISessionUUID(identity.sessionID) {
		return logWatcherStartState{}, false
	}
	state := logWatcherStartState{
		identity:   identity,
		generation: s.logWatcherGen + 1,
		history:    s.history,
		oldWatcher: s.logWatcher,
	}
	s.logWatcher = nil
	s.logWatcherGen = state.generation
	return state, true
}

func (s *session) newCurrentLogWatcher(tr *transport, state logWatcherStartState) *sessionLogWatcher {
	return newSessionLogWatcher(sessionLogWatcherConfig{
		Logger:       s.logger,
		PollInterval: defaultSessionLogWatcherPollInterval(),
		ResolvePath: func() (string, error) {
			return state.history.sessionPath(state.identity.sessionID)
		},
		OnUsage: func(usage sessionLogUsage) {
			s.dispatchTokenUsageIfCurrent(tr, state.identity, state.generation, usage)
		},
	})
}

func (s *session) installLogWatcherIfCurrent(tr *transport, state logWatcherStartState, watcher *sessionLogWatcher) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.logWatcherMatchesLocked(tr, state) {
		return false
	}
	s.logWatcher = watcher
	return true
}

func (s *session) logWatcherMatchesLocked(tr *transport, state logWatcherStartState) bool {
	return s.transport == tr &&
		s.logWatcherGen == state.generation &&
		strings.TrimSpace(s.sessionID) == state.identity.sessionID &&
		strings.TrimSpace(s.threadID) == state.identity.threadID
}

// dispatchTokenUsageIfCurrent 派发令牌usageif当前。
func (s *session) dispatchTokenUsageIfCurrent(tr *transport, identity logWatcherIdentity, generation uint64, usage sessionLogUsage) {
	usageSessionID := strings.TrimSpace(usage.SessionID)
	if usageSessionID != "" && !strings.EqualFold(usageSessionID, identity.sessionID) {
		return
	}
	timestamp := strings.TrimSpace(usage.Timestamp)
	if timestamp == "" {
		timestamp = time.Now().Format(time.RFC3339Nano)
	}
	s.mu.Lock()
	if s.transport != tr ||
		s.logWatcherGen != generation ||
		strings.TrimSpace(s.sessionID) != identity.sessionID ||
		strings.TrimSpace(s.threadID) != identity.threadID {
		s.mu.Unlock()
		return
	}
	threadID := s.eventThreadIDLocked()
	sessionID := s.sessionID
	contextWindow := claudeContextWindow(s.sessionContextWindow, s.currentTransportModelLocked(), s.history)
	s.mu.Unlock()

	s.dispatch(dto.RawProviderEvent{
		EventType: "tokens:log_watcher",
		Data: map[string]any{
			"thread_id":      threadID,
			"session_id":     sessionID,
			"timestamp":      timestamp,
			"input_tokens":   usage.InputTokens,
			"output_tokens":  usage.OutputTokens,
			"total_tokens":   usage.TotalTokens,
			"context_window": contextWindow,
		},
	})
}

func (s *session) restartIfNeededLocked(ctx context.Context, req dto.TurnRequest) error {
	for {
		waited, err := s.awaitPendingRestartReadyLocked(ctx)
		if err != nil {
			return err
		}
		if waited {
			continue
		}
		next := s.stagedTurnSettingsLocked(req)
		if !s.needsRestartLocked(next) {
			s.consumeNoopPendingLocked(next)
			return s.awaitThreadReadyLocked(ctx)
		}
		return s.performRestartLocked(ctx, next)
	}
}

func (s *session) awaitPendingRestartReadyLocked(ctx context.Context) (bool, error) {
	if ready, _ := s.pendingThreadReadyLocked(); ready == nil || s.transport == nil {
		return false, nil
	}
	if err := s.awaitThreadReadyLocked(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *session) needsRestartLocked(next stagedSessionState) bool {
	return next.settingsChanged || !s.transport.readyForSend()
}

func (s *session) performRestartLocked(ctx context.Context, next stagedSessionState) error {
	resumeID := s.restartResumeIDLocked()
	restartReason := s.restartReasonLocked()
	s.logRestartLocked(restartReason, next, resumeID)
	prepared, err := s.prepareSessionRestartLocked(ctx, next, resumeID, restartReason)
	if err != nil {
		return err
	}
	return s.awaitSessionRestartLocked(prepared, next)
}

func (s *session) restartReasonLocked() string {
	if s.transport == nil || !s.transport.readyForSend() {
		return "transport_unavailable"
	}
	return "settings_changed"
}

func (s *session) logRestartLocked(reason string, next stagedSessionState, resumeID string) {
	if s.logger == nil {
		return
	}
	s.logger.Warn("claudecli: session restart triggered",
		"agent_id", s.agentID,
		"thread_id", s.threadID,
		"session_id", s.sessionID,
		"old_model", s.currentTransportModelLocked(),
		"new_model", next.displayModel,
		"resume_id", resumeID,
		"reason", reason,
	)
}

func (s *session) prepareSessionRestartLocked(ctx context.Context, next stagedSessionState, resumeID, reason string) (preparedSessionRestart, error) {
	baseInstructions := promptSnapshotBaseInstructions(next.config.PromptSnapshot, s.instructions)
	fn := s.launchCLI
	if fn == nil {
		fn = launchCLIWithManifest
	}
	tr, cleanup, err := fn(
		s.binaryPath,
		s.cwd,
		next.model,
		baseInstructions,
		next.config,
		next.manifest,
		resumeID,
	)
	if err != nil {
		return preparedSessionRestart{}, err
	}
	prepared := preparedSessionRestart{
		transport: tr,
		cleanup:   cleanup,
		snapshot:  s.restartSnapshotLocked(),
	}
	prepared.waitCtx, prepared.generation = s.beginRestartWaitLocked(ctx)
	prepared.patch = s.statusPatchRawEventLocked("syncing", "Claude 重启中…", restartStatusDetails(reason))
	registerTransportPID(s.pidRegistry, tr, s.agentID)
	s.swapRestartTransportLocked(tr, cleanup, next, resumeID)
	return prepared, nil
}

func (s *session) restartSnapshotLocked() restartSnapshot {
	ready := s.threadReady
	return restartSnapshot{
		transport:         s.transport,
		cleanup:           s.cleanup,
		watcher:           s.detachLogWatcherLocked(),
		ready:             ready,
		readyClosed:       readyChannelClosed(ready),
		transportModel:    s.transportModel,
		transportConfig:   s.transportConfig,
		transportManifest: s.transportManifest,
		contextWindow:     s.sessionContextWindow,
	}
}

func (s *session) swapRestartTransportLocked(tr *transport, cleanup func(), next stagedSessionState, resumeID string) {
	s.resetThreadReadyLocked()
	if identifier.IsClaudeCLISessionUUID(resumeID) {
		s.markThreadReadyLocked()
	}
	s.activeTurn = nil
	s.activeToolCalls = nil
	s.suppressedTurns = map[string]struct{}{}
	s.transport = tr
	s.cleanup = cleanup
	s.transportModel = next.displayModel
	s.transportConfig = next.config
	s.transportManifest = next.manifest
	s.sessionContextWindow = 0
	s.startReadLoop(tr)
}

func (s *session) awaitSessionRestartLocked(prepared preparedSessionRestart, next stagedSessionState) error {
	s.dispatchRestartPatch(prepared)
	if err := s.awaitThreadReadyLocked(prepared.waitCtx); err != nil {
		return s.rollbackSessionRestartLocked(prepared, err)
	}
	s.commitRestartSuccessLocked(next)
	s.finishRestartWaitLocked(prepared.generation)
	unregisterTransportPID(s.pidRegistry, prepared.snapshot.transport)
	if prepared.snapshot.transport != nil || prepared.snapshot.cleanup != nil {
		go releaseTransport(prepared.snapshot.transport, prepared.snapshot.cleanup)
	}
	return nil
}

// rollbackSessionRestartLocked 处理rollback会话restartlocked。
func (s *session) rollbackSessionRestartLocked(prepared preparedSessionRestart, err error) error {
	stagedCurrent := s.transport == prepared.transport
	var failurePatch dto.RawProviderEvent
	if stagedCurrent {
		s.restoreRestartSnapshotLocked(prepared.snapshot)
		status, header, details := restartFailureStatus(err)
		failurePatch = s.statusPatchRawEventLocked(status, header, details)
	}
	s.finishRestartWaitLocked(prepared.generation)
	unregisterTransportPID(s.pidRegistry, prepared.transport)
	go releaseTransport(prepared.transport, prepared.cleanup)
	if !stagedCurrent && (prepared.snapshot.transport != nil || prepared.snapshot.cleanup != nil) {
		unregisterTransportPID(s.pidRegistry, prepared.snapshot.transport)
		go releaseTransport(prepared.snapshot.transport, prepared.snapshot.cleanup)
	}
	if stagedCurrent {
		s.mu.Unlock()
		s.dispatch(failurePatch)
		s.mu.Lock()
	}
	return err
}

// stagedTurnSettingsLocked 处理stagedturnsettingslocked。
func (s *session) stagedTurnSettingsLocked(req dto.TurnRequest) stagedSessionState {
	currentModel := strings.TrimSpace(s.model)
	currentDisplayModel := claudeLaunchDisplayModel(currentModel, s.history)
	currentConfig := canonicalizeClaudeLaunchConfig(currentDisplayModel, s.config)
	currentManifest := s.manifest
	if s.transport != nil {
		currentModel = strings.TrimSpace(shared.FirstNonEmpty(s.transportModel, currentModel))
		currentDisplayModel = claudeLaunchDisplayModel(currentModel, s.history)
		transportConfig := s.transportConfig
		if reflect.DeepEqual(transportConfig, cliLaunchConfig{}) {
			transportConfig = s.config
		}
		currentConfig = canonicalizeClaudeLaunchConfig(currentDisplayModel, transportConfig)
		if !reflect.DeepEqual(s.transportManifest, dto.MCPManifest{}) {
			currentManifest = s.transportManifest
		}
	}
	next := stagedSessionState{
		model:        currentModel,
		config:       currentConfig,
		manifest:     currentManifest,
		displayModel: currentDisplayModel,
	}
	s.applyPendingStagedSettingsLocked(&next)
	if value := strings.TrimSpace(req.Overrides.Model); value != "" {
		next.model = value
	}
	if value := strings.TrimSpace(req.Overrides.Effort); value != "" {
		next.config.Effort = value
	}
	if manifestChanged(req.MCP, currentManifest) {
		next.manifest = req.MCP
		next.settingsChanged = true
	}
	next.displayModel = claudeLaunchDisplayModel(next.model, s.history)
	next.config = canonicalizeClaudeLaunchConfig(next.displayModel, next.config)
	if next.model != currentModel || next.config.Effort != currentConfig.Effort {
		next.settingsChanged = true
	}
	return next
}

func (s *session) applyPendingStagedSettingsLocked(next *stagedSessionState) {
	if s.pendingModel != nil {
		next.appliedPendingModel = true
		next.appliedPendingModelText = strings.TrimSpace(*s.pendingModel)
		next.model = next.appliedPendingModelText
	}
	if s.pendingEffort != nil {
		next.appliedPendingEffort = true
		next.appliedPendingEffortText = strings.TrimSpace(*s.pendingEffort)
		next.config.Effort = next.appliedPendingEffortText
	}
}

// consumeNoopPendingLocked 处理consumenoop待处理locked。
func (s *session) consumeNoopPendingLocked(next stagedSessionState) {
	if next.appliedPendingModel {
		s.overrideModel = next.appliedPendingModelText
		s.overrideModelSet = true
	}
	if next.appliedPendingEffort {
		s.overrideEffort = next.appliedPendingEffortText
		s.overrideEffortSet = true
	}
	if next.appliedPendingModel && s.pendingModel != nil && next.model == strings.TrimSpace(s.model) {
		s.pendingModel = nil
	}
	if next.appliedPendingEffort && s.pendingEffort != nil && next.config.Effort == canonicalizeClaudeLaunchConfig(s.currentTransportModelLocked(), s.config).Effort {
		s.pendingEffort = nil
	}
	s.configDirty = s.pendingModel != nil || s.pendingEffort != nil
}
