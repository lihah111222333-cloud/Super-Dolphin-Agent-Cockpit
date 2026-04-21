package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// isPendingLaunchIntent reports whether a StartRequest should be treated as a
// "create an empty thread card, defer provider-CLI spawn to the first turn"
// request. We require both:
//   - req.DeferSpawn=true (explicit opt-in by the caller)
//   - no pre-baked prompt content (Prompt/Name/BaseInstructions/AgentKey all
//     empty) so the router still has a blank canvas to classify on turn/start.
//
// If any prompt-shaped field is set we stay on the eager path; it means the
// caller already knows enough to spawn immediately (router sees real input
// during Start, identity lock works out of the gate).
func isPendingLaunchIntent(req StartRequest) bool {
	if !req.DeferSpawn {
		return false
	}
	if strings.TrimSpace(req.Prompt) != "" {
		return false
	}
	if strings.TrimSpace(req.Name) != "" {
		return false
	}
	if strings.TrimSpace(req.BaseInstructions) != "" {
		return false
	}
	if strings.TrimSpace(req.AgentKey) != "" {
		return false
	}
	return true
}

// startPendingThread writes a placeholder agent_threads row with
// pending_launch=true and returns a StartResult the UI can render as a pending
// card. The provider CLI is NOT forked; that happens lazily in SpawnIfNeeded on
// the first turn/start for this thread.
func (s *service) startPendingThread(ctx context.Context, req StartRequest, agentID string) (StartResult, error) {
	if s == nil || s.threadStore == nil {
		return StartResult{}, errors.New("thread store is not configured")
	}
	createdAt := time.Now().Unix()
	// Default display text for a pending card; the real name gets rewritten
	// during SpawnIfNeeded once we have the user's first-turn input.
	displayName := strings.TrimSpace(shared.FirstNonEmpty(req.Name, req.Prompt))
	if displayName == "" {
		displayName = "新对话"
	}
	// Stash the launch-time provider/effort/personality/approvals choices into
	// config_override so SpawnIfNeeded can restore them on the first turn.
	// Without this the pending row only retains Model+Cwd and normalizeStart
	// in the spawn path defaults Provider back to "codex", overriding the
	// user's UI selection (this was the '创建的对话都是codex' regression).
	pendingStored := storedThreadConfig{
		Model:       strings.TrimSpace(req.Model),
		Effort:      strings.TrimSpace(req.Effort),
		Approvals:   strings.TrimSpace(req.ApprovalPolicy),
		Personality: strings.TrimSpace(req.Personality),
		Provider:    strings.TrimSpace(req.Provider),
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
		OwnerThreadID:    req.OwnerThreadID,
		PendingLaunch:    true,
	})
	if err := s.threadStore.Upsert(ctx, newThreadUpsertParams(threadstore.Thread{
		ThreadID:         state.PublicThreadID,
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
		AgentKey:         "",
		PromptVersionID:  nil,
		PendingLaunch:    true,
	})); err != nil {
		return StartResult{}, fmt.Errorf("thread: upsert pending_launch row: %w", err)
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
	}, nil
}

// isThreadPendingLaunch reports whether the agent_threads row for threadID
// exists with pending_launch=true. Used by entry points (Archive, Delete,
// ReadMessages, Compact) to short-circuit operations that assume a binding
// / session exists, which is never the case for a pending thread.
func (s *service) isThreadPendingLaunch(ctx context.Context, threadID string) bool {
	if s == nil || s.threadStore == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	row, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil || row == nil {
		return false
	}
	return row.PendingLaunch
}

// acquirePendingLaunchLock returns the per-thread mutex used to serialize
// SpawnIfNeeded calls for a given thread_id. sync.Map.LoadOrStore guarantees
// only one *sync.Mutex value exists per key under concurrent access.
func (s *service) acquirePendingLaunchLock(threadID string) *sync.Mutex {
	m, _ := s.pendingLaunchMu.LoadOrStore(threadID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// SpawnIfNeeded lazily forks the provider CLI for a thread that was previously
// created with pending_launch=true. It runs the router classifier with the
// first-turn user input, materializes a prompt_versions row, launches the
// agent, establishes the session, saves the prompt snapshot, and clears
// pending_launch on the agent_threads row. Safe to call concurrently; only the
// first caller per thread actually spawns (guarded by acquirePendingLaunchLock).
//
// Returns (launched=true, nil) when this call performed the spawn.
// Returns (false, nil) when the thread is already running (no-op).
// Returns (false, err) when the thread exists but spawn failed; caller should
// leave pending_launch=true so a later retry can proceed.
func (s *service) SpawnIfNeeded(ctx context.Context, threadID, userInputForRouter string) (bool, error) {
	ctx = shared.NonNilContext(ctx)
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, errors.New("thread: SpawnIfNeeded requires thread_id")
	}
	if s == nil || s.threadStore == nil {
		return false, errors.New("thread store is not configured")
	}

	mu := s.acquirePendingLaunchLock(threadID)
	mu.Lock()
	defer mu.Unlock()

	row, needSpawn, err := s.loadPendingLaunchRow(ctx, threadID)
	if err != nil || !needSpawn {
		return false, err
	}

	// Agent-id convention for threadStateStartKind: publicThreadID == agentID.
	// The pending row was written with that assumption, so we reuse threadID.
	agentID := threadID
	req, err := buildPendingSpawnRequest(row, agentID, userInputForRouter)
	if err != nil {
		return false, err
	}
	if err := s.runPendingSpawn(ctx, &req, row, agentID, threadID); err != nil {
		return false, err
	}
	return true, nil
}

// loadPendingLaunchRow returns (row, true, nil) only when the agent_threads
// row exists and still has pending_launch=true. A missing row is reported as
// an error (caller cannot spawn something that does not exist); any other
// read error is wrapped. A running thread yields (nil, false, nil).
func (s *service) loadPendingLaunchRow(ctx context.Context, threadID string) (*threadstore.Thread, bool, error) {
	row, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, false, fmt.Errorf("thread: %w", err)
		}
		return nil, false, fmt.Errorf("thread: load pending row: %w", err)
	}
	if row == nil || !row.PendingLaunch {
		return nil, false, nil
	}
	// Guard against the Stop/Archive race: if the row was already marked
	// stopped or archived but pending_launch was not cleared (updateThreadStatus
	// only touches the status column), bail out instead of forking a ghost CLI.
	if row.Status != statusCreated && row.Status != "" {
		return nil, false, nil
	}
	return row, true, nil
}

// buildPendingSpawnRequest reconstructs the StartRequest that the UI originally
// submitted when it called Start with DeferSpawn=true. The launch-time
// provider/effort/personality/approvals choices were stashed into
// config_override by startPendingThread and are restored here so
// normalizeStartRequest does not default them back (this was the
// "created threads are always codex" regression).
func buildPendingSpawnRequest(row *threadstore.Thread, agentID, userInputForRouter string) (StartRequest, error) {
	storedCfg := decodeStoredThreadConfig(row.ConfigOverride)
	req := StartRequest{
		AgentID:          agentID,
		ParentAgentID:    row.ParentAgentID,
		AgentType:        row.AgentType,
		AgentMemoryScope: row.AgentMemoryScope,
		CWD:              row.Cwd,
		Model:            shared.FirstNonEmpty(storedCfg.Model, row.Model),
		Name:             row.Prompt,
		Prompt:           strings.TrimSpace(userInputForRouter),
		OwnerThreadID:    row.OwnerThreadID,
		Provider:         storedCfg.Provider,
		Effort:           storedCfg.Effort,
		Personality:      storedCfg.Personality,
		ApprovalPolicy:   storedCfg.Approvals,
	}
	// normalizeStartRequest fills in provider default ("codex"), resolves CWD,
	// sanitizes sandbox, picks approval policy defaults, etc. AgentID stays
	// intact because normalizeStartRequest only generates a new one when the
	// field is empty.
	normalized, normalizedAgentID, err := normalizeStartRequest(req)
	if err != nil {
		return StartRequest{}, fmt.Errorf("thread: normalize pending spawn: %w", err)
	}
	if normalizedAgentID != agentID {
		return StartRequest{}, fmt.Errorf("thread: normalize rewrote agent_id (%s -> %s); pending row is tied to the original id", agentID, normalizedAgentID)
	}
	return normalized, nil
}

// runPendingSpawn executes the Router -> assembly -> launch -> session ->
// persist pipeline for a deferred spawn. On failure it rolls back the
// orchestration agent (if already launched) and releases the scratchpad
// snapshot so a later retry sees a clean slate.
func (s *service) runPendingSpawn(
	ctx context.Context,
	req *StartRequest,
	row *threadstore.Thread,
	agentID, threadID string,
) error {
	// Router + prompt_versions materialization.
	s.resolveRoutedPrompt(ctx, req)
	if req.PromptAssemblyRef == nil {
		req.PromptAssemblyRef = s.promptAssembly
	}
	assemblyInput, cleanupScratchpad, err := s.buildStartAssemblyInput(*req, agentID)
	if err != nil {
		return fmt.Errorf("thread: assembly input: %w", err)
	}
	agentLaunched := false
	cleanupOnFailure := true
	defer cleanupPendingSpawn(ctx, s, &cleanupOnFailure, cleanupScratchpad, &agentLaunched, agentID)
	assembly, err := resolveStartPromptAssembly(ctx, *req, assemblyInput)
	if err != nil {
		return fmt.Errorf("thread: prompt assembly: %w", err)
	}
	displayName := strings.TrimSpace(shared.FirstNonEmpty(assembly.DisplayName, row.Prompt))
	if err := s.launchAgent(ctx, agentID, req.CWD, displayName,
		req.ParentAgentID, req.AgentType, req.AgentMemoryScope,
		req.Provider, req.Model); err != nil {
		return fmt.Errorf("thread: launch agent: %w", err)
	}
	agentLaunched = true
	session, err := s.establishStartedSession(ctx, *req, assemblyInput, assembly, agentID)
	if err != nil {
		return fmt.Errorf("thread: establish session: %w", err)
	}
	// persistStartedSession is the eager path's final step: it builds the
	// thread state, upserts the agent_threads row (clearing pending_launch),
	// writes the binding row, saves the prompt snapshot, and publishes
	// thread.started. Reusing it keeps pending spawns DB-shape identical to
	// eager starts.
	if _, err := s.persistStartedSession(ctx, *req, assemblyInput, assembly, agentID, displayName, session); err != nil {
		return fmt.Errorf("thread: persist launched session: %w", err)
	}
	cleanupOnFailure = false
	s.pendingLaunchMu.Delete(threadID)
	publishPendingSpawnLaunched(s, req, row, session, agentID, threadID, displayName)
	return nil
}

// cleanupPendingSpawn is the shared `defer` target for runPendingSpawn. It
// leaves state untouched on success (active=false) and otherwise releases
// the scratchpad and stops the orchestration agent if launchAgent already
// committed. active / agentLaunched are pointers so the caller can mutate
// them after this defer is registered.
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

// publishPendingSpawnLaunched emits thread.launched for observability once the
// pending spawn has fully committed. persistStartedSession already wrote
// agent_key + prompt_version_id (resolveRoutedPrompt filled them before
// assembly); any future router-provenance writes belong in a shared helper.
func publishPendingSpawnLaunched(
	s *service,
	req *StartRequest,
	row *threadstore.Thread,
	session contract.Session,
	agentID, threadID, displayName string,
) {
	effectiveModel, effectiveCWD, _ := enrichFromSessionConfig(session, req.Model, req.CWD)
	spawnedState := newThreadState(threadStateStartKind, threadStateFields{
		PublicThreadID:   threadID,
		AgentID:          agentID,
		ParentAgentID:    req.ParentAgentID,
		AgentType:        req.AgentType,
		AgentMemoryScope: req.AgentMemoryScope,
		ProviderThreadID: session.ThreadID(),
		Provider:         req.Provider,
		CWD:              effectiveCWD,
		Model:            effectiveModel,
		Name:             displayName,
		Prompt:           displayName,
		RolloutPath:      session.RolloutPath(),
		SessionUUID:      session.ThreadID(),
		CreatedAt:        row.CreatedAt,
		AgentKey:         req.AgentKey,
		PromptVersionID:  req.PromptVersionID,
		OwnerThreadID:    req.OwnerThreadID,
	})
	s.publishThreadLaunched(spawnedState)
}
