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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
	// Display text for a pending card; left empty when the caller did not
	// supply a Name or Prompt.  The real name gets rewritten during
	// SpawnIfNeeded once we have the user's first-turn input.
	displayName := strings.TrimSpace(shared.FirstNonEmpty(req.Name, req.Prompt))
	// Stash the launch-time provider/effort/personality/approvals choices into
	// config_override so SpawnIfNeeded can restore them on the first turn.
	// Without this the pending row only retains Model+Cwd and normalizeStart
	// in the spawn path defaults Provider back to "codex", overriding the
	// user's UI selection (this was the '创建的对话都是codex' regression).
	pendingStored := storedThreadConfig{
		Model:         strings.TrimSpace(req.Model),
		Effort:        strings.TrimSpace(req.Effort),
		Approvals:     strings.TrimSpace(req.ApprovalPolicy),
		Personality:   strings.TrimSpace(req.Personality),
		Provider:      strings.TrimSpace(req.Provider),
		PromptKey:     strings.TrimSpace(req.PromptKey),
		UseClassifier: req.UseClassifier,
		Runtime:       shared.CloneRuntimeConfigMap(req.Config),
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
		AgentKey:         "",
		PromptVersionID:  nil,
		PendingLaunch:    true,
	})); err != nil {
		return StartResult{}, fmt.Errorf("thread: upsert pending_launch row: %w", err)
	}
	if meta := taskHandoffMetaFromRuntimeConfig(req.Config); meta.TaskID != "" {
		s.logIgnoredTaskHandoffError("ensure task handoff shell for pending thread", state.PublicThreadID, s.ensureTaskHandoffShell(ctx, meta, state.OwnerThreadID))
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
		TaskID:        firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake),
		HandoffFile:   firstConfigString(req.Config, taskConfigKeyHandoffFile, taskConfigKeyHandoffFileSnake),
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
// Returns (launched=true, routing, nil) when this call performed the spawn;
// routing captures the router decision so turn/start can forward it to the UI.
// Returns (false, zero, nil) when the thread is already running (no-op).
// Returns (false, zero, err) when the thread exists but spawn failed; caller
// should leave pending_launch=true so a later retry can proceed.
func (s *service) SpawnIfNeeded(ctx context.Context, threadID, userInputForRouter string) (bool, SpawnRouting, error) {
	ctx = shared.NonNilContext(ctx)
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, SpawnRouting{}, errors.New("thread: SpawnIfNeeded requires thread_id")
	}
	if s == nil || s.threadStore == nil {
		return false, SpawnRouting{}, errors.New("thread store is not configured")
	}

	mu := s.acquirePendingLaunchLock(threadID)
	mu.Lock()
	defer mu.Unlock()

	row, needSpawn, err := s.loadPendingLaunchRow(ctx, threadID)
	if err != nil || !needSpawn {
		return false, SpawnRouting{}, err
	}

	// Agent-id convention for threadStateStartKind: publicThreadID == agentID.
	// The pending row was written with that assumption, so we reuse threadID.
	agentID := threadID
	req, err := buildPendingSpawnRequest(row, agentID, userInputForRouter)
	if err != nil {
		return false, SpawnRouting{}, err
	}
	if err := s.prepareTaskHandoffStart(ctx, &req); err != nil {
		return false, SpawnRouting{}, err
	}
	if err := s.runPendingSpawn(ctx, &req, row, agentID, threadID); err != nil {
		return false, SpawnRouting{}, err
	}
	// req.* are populated by resolveRoutedPrompt inside runPendingSpawn.
	return true, SpawnRouting{
		AgentKey:        req.AgentKey,
		AgentTitle:      req.AgentTitle,
		PromptKey:       req.PromptKey,
		PromptVersionID: req.PromptVersionID,
	}, nil
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
		PromptKey:        storedCfg.PromptKey,
		UseClassifier:    storedCfg.UseClassifier,
		Config:           shared.CloneRuntimeConfigMap(storedCfg.Runtime),
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
	if req.PromptAssemblyRef == nil {
		req.PromptAssemblyRef = s.promptAssembly
	}
	// Overlap the classifier (dominated by a 5-15s `claude -p` subprocess when
	// the Plan B opt-in is on) with buildStartAssemblyInput (local filesystem
	// scratchpad prep + buildCtx). The assembly step only depends on classifier
	// output for two fields — BaseInstructions and DeveloperInstructions —
	// which we patch in after both goroutines settle. Every other input
	// (cwd, git root, MCP snapshot, session flags, scratchpad dir, ...) is
	// already present on the pre-classifier snapshot, so splitting the work
	// here is safe and shaves whatever the scratchpad/mcp-config I/O path
	// costs (∼200–500ms in typical setups) off the user-visible first-turn
	// latency.
	snapshot := *req
	parallelStart := time.Now()

	g, gCtx := errgroup.WithContext(ctx)
	var assemblyInput contract.StartInput
	var cleanupScratchpad func()

	g.Go(func() error {
		s.resolveRoutedPrompt(gCtx, req)
		return nil
	})
	g.Go(func() error {
		var aerr error
		assemblyInput, cleanupScratchpad, aerr = s.buildStartAssemblyInput(snapshot, agentID)
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
	// When the classifier picked a persona that isn't the anonymous default,
	// prefix the thread name with a human-readable agent label. This is the
	// one visible place to surface "you got routed to X" without adding a
	// separate UI component: sidebar, chat header, and dashboard all show
	// display_name. The prefix is a stable bracketed slug so the UI can parse
	// it into a blue pill (see stores/thread-view.model.js:parseAgentBadge).
	displayName := strings.TrimSpace(assembly.DisplayName)
	displayName = prependAgentBadge(displayName, req.AgentTitle, req.AgentKey)
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

// foldRouterOutputIntoAssemblyInput copies router-produced fields from the
// post-router StartRequest into the assemblyInput that was built from the
// pre-router snapshot. snapshot was cloned before resolveRoutedPrompt ran, so
// without this fold-back every field the router stamps onto *req would be
// silently dropped by the time AssembleStart executes. Historically only two
// fields needed folding (BaseInstructions / DeveloperInstructions); when
// match_when + section-backed templates landed, BaseInstructionBlocks joined
// the list. Keep this helper as the single place that enforces "whatever
// resolveRoutedPrompt writes must reach the assembler" so future router
// additions have one predictable update site.
func foldRouterOutputIntoAssemblyInput(assemblyInput *contract.StartInput, req *StartRequest) {
	if assemblyInput == nil || req == nil {
		return
	}
	assemblyInput.BaseInstructions = req.BaseInstructions
	assemblyInput.DeveloperInstructions = req.DeveloperInstructions
	assemblyInput.BaseInstructionBlocks = append(
		[]contract.BaseInstructionBlock(nil),
		req.BaseInstructionBlocks...,
	)
}

// prependAgentBadge renders `[label] displayName` when the router picked a
// non-default persona. Prefers the prompt_template's human-readable Title
// ("SQL 与数据建模专家") because that's what users recognize; falls back to
// the agent_key slug ("sql-expert") if Title is empty so the badge always
// says *something* meaningful.
//
// The badge is skipped for the anonymous default identity (agent_key=="main")
// — otherwise every un-classified thread would end up with a redundant
// [通用助手] / [main] prefix and the badge stops carrying information.
// Idempotent: applying the same label twice leaves the name unchanged so
// retry spawns don't stack prefixes.
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
