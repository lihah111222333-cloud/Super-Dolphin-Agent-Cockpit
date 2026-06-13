package memory

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"time"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const backgroundExtractTimeout = 5 * time.Second

func (h *MemoryLifecycleHooks) onTurnStarted(evt turndto.TurnStarted) {
	threadID := strings.TrimSpace(evt.ThreadID)
	turnID := strings.TrimSpace(evt.TurnID)
	if h == nil || threadID == "" || turnID == "" {
		return
	}
	h.stateMu.Lock()
	h.activeTurns[threadID] = turnID
	h.stateMu.Unlock()
}

// onTurnCompleted performs the full turn-end processing including disk
// I/O (intent detection, write, extraction). This method is invoked on
// the memoryHookWorker goroutine, NOT the bus dispatcher callback.
func (h *MemoryLifecycleHooks) onTurnCompleted(ctx context.Context, evt turndto.TurnCompleted) {
	if h == nil {
		return
	}
	h.onTurnEnd(ctx, evt)
	handled := h.consumeTurnTracking(evt.ThreadID, evt.TurnID)
	if handled {
		// A tool wrote into the auto-mem path during this turn; the
		// MEMORY.md entrypoint we cached at session start may no longer
		// match disk, so invalidate so the next AssembleStart re-renders.
		h.invalidateMemorySections()
	}
	if !h.shouldExtractThread(ctx, evt) {
		return
	}
	h.enqueueBackgroundExtraction(strings.TrimSpace(evt.ThreadID), handled)
}

// onTurnInputReceived handles explicit user memory intents. The
// rememberTurnInput call is in-memory only; handleExplicitUserMemoryIntent
// performs disk I/O. This method is invoked on the memoryHookWorker
// goroutine, NOT the bus dispatcher callback.
// onTurnInputReceived 处理onturninputreceived。
func (h *MemoryLifecycleHooks) onTurnInputReceived(ctx context.Context, ev turndto.TurnInputReceived) {
	if h == nil || !h.enabled {
		return
	}
	if err := contextErr(ctx); err != nil {
		return
	}
	key, text, ok := h.rememberTurnInput(ev)
	if !ok {
		return
	}
	handled, err := h.handleExplicitUserMemoryIntent(ctx, turnCompletedFromInput(ev), text)
	h.handleExplicitIntentError(ev.ThreadID, handled, err)
	if handled && err == nil {
		h.markHandledTurnInput(key)
	}
}

func (h *MemoryLifecycleHooks) rememberTurnInput(ev turndto.TurnInputReceived) (string, string, bool) {
	if !isExplicitMemoryUserInput(ev) {
		return "", "", false
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	turnID := strings.TrimSpace(ev.TurnID)
	if threadID == "" || turnID == "" {
		return "", "", false
	}
	text := strings.TrimSpace(ev.Text)
	key := turnTrackingKey(threadID, turnID)
	h.stateMu.Lock()
	if h.turnInputs == nil {
		h.turnInputs = map[string]string{}
	}
	h.turnInputs[key] = text
	h.stateMu.Unlock()
	return key, text, true
}

func (h *MemoryLifecycleHooks) consumeTurnInput(key string) (string, bool) {
	if h == nil || key == "" {
		return "", false
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	text, ok := h.turnInputs[key]
	if ok {
		delete(h.turnInputs, key)
	}
	return text, ok
}

func (h *MemoryLifecycleHooks) clearTurnInput(key string) {
	if h == nil || key == "" {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	delete(h.turnInputs, key)
}

func (h *MemoryLifecycleHooks) markHandledTurnInput(key string) {
	if h == nil || key == "" {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.handledTurnInputs == nil {
		h.handledTurnInputs = map[string]struct{}{}
	}
	h.handledTurnInputs[key] = struct{}{}
}

func (h *MemoryLifecycleHooks) consumeHandledTurnInput(key string) bool {
	if h == nil || key == "" {
		return false
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	_, ok := h.handledTurnInputs[key]
	if ok {
		delete(h.handledTurnInputs, key)
	}
	return ok
}

func turnCompletedFromInput(ev turndto.TurnInputReceived) turndto.TurnCompleted {
	return turndto.TurnCompleted{TurnHeader: ev.TurnHeader, Success: true}
}

func isExplicitMemoryUserInput(ev turndto.TurnInputReceived) bool {
	if strings.TrimSpace(ev.Text) == "" {
		return false
	}
	source := strings.TrimSpace(ev.Source)
	if source != "" && !strings.EqualFold(source, "user") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ev.InputType)) {
	case "", "message", "text":
		return true
	default:
		return false
	}
}

func normalizeIntentText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

func isGenericForgetTarget(text string) bool {
	switch CanonicalName(text) {
	case "it", "this", "that", "memory", "这", "这个", "这点", "这条":
		return true
	default:
		return false
	}
}

func (h *MemoryLifecycleHooks) onTurnTerminated(threadID, turnID string) {
	if h == nil {
		return
	}
	h.consumeTurnTracking(threadID, turnID)
}

func (h *MemoryLifecycleHooks) onToolCallBegin(ev tooldto.ToolCallBegin) {
	callID := strings.TrimSpace(ev.CallID)
	threadID := strings.TrimSpace(ev.ThreadID)
	turnID := strings.TrimSpace(ev.TurnID)
	if h == nil || callID == "" || threadID == "" || turnID == "" {
		return
	}
	h.stateMu.Lock()
	h.callTurns[callID] = toolCallScope{threadID: threadID, turnID: turnID}
	h.activeTurns[threadID] = turnID
	h.stateMu.Unlock()
}

// onToolDiffUpdated 处理on工具diffupdated。
func (h *MemoryLifecycleHooks) onToolDiffUpdated(ev tooldto.ToolDiffUpdated) {
	if h == nil {
		return
	}
	scope, ok := h.resolveToolScope(ev)
	if !ok {
		return
	}
	files := uniqueNonEmptyStrings(append([]string(nil), ev.Files...))
	if len(files) == 0 {
		files = extractDiffFiles(ev.DiffText)
	}
	if len(files) == 0 {
		return
	}
	key := turnTrackingKey(scope.threadID, scope.turnID)
	h.stateMu.Lock()
	if _, ok := h.turnWrites[key]; !ok {
		h.turnWrites[key] = map[string]struct{}{}
	}
	for _, file := range files {
		h.turnWrites[key][file] = struct{}{}
	}
	h.stateMu.Unlock()
}

// DrainPendingExtraction 处理drain待处理extraction。
func (h *MemoryLifecycleHooks) DrainPendingExtraction(ctx context.Context) error {
	if h == nil {
		return nil
	}
	// Set drainClosed under drainMu before Wait(): any concurrent
	// enqueueBackgroundExtraction holding drainMu will finish its Add(1)
	// before we proceed, and any new enqueue arriving after will read
	// drainClosed=true and skip Add. Without this guard, Add(1) racing
	// with Wait() on a counter that just hit zero panics.
	h.drainMu.Lock()
	h.drainClosed = true
	h.drainMu.Unlock()
	done := make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		h.extractWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExtractAndSave 提取save。
func (h *MemoryLifecycleHooks) ExtractAndSave(
	ctx context.Context,
	transcript []providerdto.Message,
	manifest []MemoryEntry,
) error {
	return h.extractAndSave(ctx, transcript, manifest, h.writeOptions(ctx, ""))
}

// extractAndSave 提取save。
func (h *MemoryLifecycleHooks) extractAndSave(
	ctx context.Context,
	transcript []providerdto.Message,
	manifest []MemoryEntry,
	options WriteOptions,
) error {
	if h == nil || !h.extractOnStop {
		return nil
	}
	items, err := h.extractorOrDefault().Extract(ctx, h.extractFn, ExtractParams{
		Transcript: transcript,
		Manifest:   manifest,
	})
	if err != nil || len(items) == 0 {
		return err
	}
	if err := h.saveExtractedMemories(items, options); err != nil {
		return err
	}
	h.invalidateMemorySections()
	return nil
}

// enqueueBackgroundExtraction 处理enqueue后台extraction。
func (h *MemoryLifecycleHooks) enqueueBackgroundExtraction(threadID string, handled bool) {
	if h == nil || threadID == "" {
		return
	}
	state := h.extractionState(threadID)
	start := state.markPending(handled)
	if !start {
		return
	}
	// Guard Add(1) against a concurrent Drain: if drainClosed is set,
	// the service is closing and Add() would race Wait(). Drop silently.
	// Important: state.markPending above already flipped state.inProgress
	// to true; if we drop the enqueue we MUST roll back the state flag,
	// otherwise ExtractionState.inProgress stays true forever and any
	// future enqueue for this thread is silently rejected by markPending.
	h.drainMu.Lock()
	if h.drainClosed {
		h.drainMu.Unlock()
		state.finish()
		return
	}
	h.extractWG.Add(1)
	h.drainMu.Unlock()
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logger := pkglogger.Get()
				if h.logger != nil {
					logger = h.logger
				}
				logger.Error("memory: recovered background extraction panic",
					"thread_id", threadID,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				state.fail(errors.New("memory: background extraction panicked"))
			}
		}()
		h.runBackgroundExtraction(threadID, state)
	}()
}

func (h *MemoryLifecycleHooks) runBackgroundExtraction(threadID string, state *ExtractionState) {
	defer h.extractWG.Done()
	for {
		cursor, handled, ok := state.beginCycle()
		if !ok {
			state.finish()
			return
		}
		nextCursor, err := h.executeBackgroundExtraction(threadID, cursor, handled)
		if err != nil {
			state.fail(err)
			return
		}
		if !state.commit(nextCursor) {
			return
		}
	}
}

// executeBackgroundExtraction 执行后台extraction。
func (h *MemoryLifecycleHooks) executeBackgroundExtraction(
	threadID string,
	cursor int64,
	handled bool,
) (int64, error) {
	ctx, cancel := ctxutil.WithTimeout(context.Background(), backgroundExtractTimeout)
	defer cancel()
	messages, err := h.readTranscript(ctx, threadID)
	if err != nil {
		return cursor, err
	}
	latest := latestTranscriptCursor(messages)
	if latest <= cursor {
		return cursor, nil
	}
	if handled {
		return latest, nil
	}
	window := transcriptWindow(messages, cursor)
	if len(window) == 0 {
		return latest, nil
	}
	manifest, err := h.buildManifest(ctx)
	if err != nil {
		return cursor, err
	}
	if err := h.extractAndSave(ctx, window, manifest, h.writeOptions(ctx, threadID)); err != nil {
		return cursor, err
	}
	return latest, nil
}

func (h *MemoryLifecycleHooks) readTranscript(ctx context.Context, threadID string) ([]providerdto.Message, error) {
	if h == nil || h.threads == nil {
		return nil, errors.New("memory transcript source is not configured")
	}
	messages, err := h.threads.ReadHistory(ctx, strings.TrimSpace(threadID), 0)
	if err != nil {
		return nil, err
	}
	return normalizeTranscriptMessages(messages), nil
}

func (h *MemoryLifecycleHooks) buildManifest(ctx context.Context) ([]MemoryEntry, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	store, err := h.diskStore()
	if err != nil {
		return nil, err
	}
	return h.manifestBuilderOrDefault().BuildManifest(store.Root())
}

// saveExtractedMemories 保存extractedmemories。
func (h *MemoryLifecycleHooks) saveExtractedMemories(items []ExtractedMemory, options WriteOptions) error {
	store, err := h.diskStore()
	if err != nil {
		return err
	}
	for _, item := range items {
		entry := buildConsolidatedMemoryEntry(item)
		if _, err := store.Create(entry, options); err == nil {
			continue
		} else if !errors.Is(err, ErrMemoryAlreadyExists) {
			return err
		}
		if _, err := store.Update(entry, options); err != nil {
			return err
		}
	}
	return nil
}

func (h *MemoryLifecycleHooks) invalidateMemorySections() {
	if h == nil {
		return
	}
	invalidateDurableMemorySections(h.sections)
}

func (h *MemoryLifecycleHooks) extractorOrDefault() *MemoryExtractor {
	if h != nil && h.extractor != nil {
		return h.extractor
	}
	return NewMemoryExtractor()
}

func (h *MemoryLifecycleHooks) manifestBuilderOrDefault() *ManifestBuilder {
	if h != nil && h.manifestBuilder != nil {
		return h.manifestBuilder
	}
	return NewManifestBuilder()
}

func (h *MemoryLifecycleHooks) extractionState(threadID string) *ExtractionState {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	state, ok := h.states[threadID]
	if ok {
		return state
	}
	state = &ExtractionState{}
	h.states[threadID] = state
	return state
}

// consumeTurnTracking 处理consumeturntracking。
func (h *MemoryLifecycleHooks) consumeTurnTracking(threadID, turnID string) bool {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if h == nil || threadID == "" || turnID == "" {
		return false
	}
	key := turnTrackingKey(threadID, turnID)
	cfg := &Config{RootDir: h.rootDir, ProjectRoot: h.projectRoot, AutoMemPathOverride: h.autoMemPathOverride}
	h.stateMu.Lock()
	delete(h.activeTurns, threadID)
	files := turnWriteFiles(h.turnWrites[key])
	delete(h.turnWrites, key)
	for callID, scope := range h.callTurns {
		if scope.threadID == threadID && scope.turnID == turnID {
			delete(h.callTurns, callID)
		}
	}
	h.stateMu.Unlock()
	return hasHandledAutoMemoryWrite(cfg, files)
}

func (h *MemoryLifecycleHooks) resolveToolScope(ev tooldto.ToolDiffUpdated) (toolCallScope, bool) {
	threadID := strings.TrimSpace(ev.ThreadID)
	callID := strings.TrimSpace(ev.CallID)
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if callID != "" {
		if scope, ok := h.callTurns[callID]; ok {
			return scope, true
		}
	}
	turnID := strings.TrimSpace(h.activeTurns[threadID])
	if threadID == "" || turnID == "" {
		return toolCallScope{}, false
	}
	return toolCallScope{threadID: threadID, turnID: turnID}, true
}

func hasHandledAutoMemoryWrite(cfg *Config, files []string) bool {
	for _, file := range files {
		if !isAutoMemPath(cfg, file) {
			continue
		}
		return true
	}
	return false
}
