package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	autoDreamMinInterval   = 24 * time.Hour
	autoDreamMinSessions   = 5
	autoDreamScanThrottle  = 10 * time.Minute
	consolidationStampFile = ".consolidation.stamp.json"

	dreamTaskPhaseStarting = "starting"
	dreamTaskPhaseUpdating = "updating"
)

var ErrConsolidationExtractFuncRequired = errors.New("dream extract func is not configured")

type AutoDreamConsolidator struct {
	cfg       *Config
	extractor *MemoryExtractor
	extractFn ExtractFunc
}

type consolidationRunOptions struct {
	cfg      *Config
	now      func() time.Time
	onLocked func()
}

type consolidationStamp struct {
	LastScanAt    string `json:"last_scan_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
}

type autoDreamThreadLister interface {
	ListAll(ctx context.Context) ([]threadstore.Thread, error)
}

func (c *AutoDreamConsolidator) Consolidate(ctx context.Context, memoryRoot string, extractFn ExtractFunc) error {
	return c.consolidateWithOptions(ctx, memoryRoot, extractFn, consolidationRunOptions{cfg: c.cfg})
}

func (c *AutoDreamConsolidator) consolidateWithOptions(
	ctx context.Context,
	memoryRoot string,
	extractFn ExtractFunc,
	opts consolidationRunOptions,
) (err error) {
	if err := contextErr(ctx); err != nil {
		return err
	}
	root, err := normalizeStoreRoot(memoryRoot)
	if err != nil {
		return err
	}
	if opts.cfg == nil {
		opts.cfg = c.cfg
	}
	if opts.cfg != nil {
		if err := rejectConsolidationPath(opts.cfg, root); err != nil {
			return err
		}
	}
	extractFn = c.resolveExtractFunc(extractFn)
	if extractFn == nil {
		return ErrConsolidationExtractFuncRequired
	}
	now := opts.now
	if now == nil {
		now = time.Now
	}
	guard, err := acquireConsolidationLock(root, consolidationLockOptions{Now: now})
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if err != nil && !started {
			_ = guard.RollbackMtime()
		}
		_ = guard.Release()
	}()
	if opts.onLocked != nil {
		opts.onLocked()
	}
	input, err := loadConsolidationPromptInput(root, opts.cfg)
	if err != nil {
		return err
	}
	hasTopicContent := len(consolidationCandidates(input.TopicEntries)) > 0
	indexContent := strings.TrimSpace(input.Index.Content)
	hasIndexContent := indexContent != "" && indexContent != "(missing)" && indexContent != "(empty)"
	if !hasTopicContent && len(input.LogDocuments) == 0 && !hasIndexContent {
		return withDiskStoreLock(root, func() error {
			if _, updateErr := UpdateMemoryIndex(root); updateErr != nil {
				return updateErr
			}
			return recordConsolidation(root, now())
		})
	}
	input.Limit = c.limit()
	started = true
	raw, err := extractFn(ctx, buildConsolidationPrompt(input))
	if err != nil {
		return err
	}
	items, err := parseExtractedMemories(raw, input.Limit)
	if err != nil {
		return err
	}
	return withDiskStoreLock(root, func() error {
		if err := removeMemoryFiles(root, staleMemoryPaths(input.TopicEntries)); err != nil {
			return err
		}
		if err := writeConsolidatedMemories(root, items); err != nil {
			return err
		}
		if _, err := UpdateMemoryIndex(root); err != nil {
			return err
		}
		return recordConsolidation(root, now())
	})
}

func (c *AutoDreamConsolidator) resolveExtractFunc(extractFn ExtractFunc) ExtractFunc {
	if extractFn != nil {
		return extractFn
	}
	if c == nil {
		return nil
	}
	return c.extractFn
}

func (c *AutoDreamConsolidator) limit() int {
	if c == nil || c.extractor == nil {
		return defaultExtractMaxItems
	}
	return c.extractor.limit()
}

func registerAutoDreamSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	if p.Hooks == nil || !p.Hooks.enabled {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Stopped) {
		p.Hooks.onThreadStopped(context.Background(), ev)
	}, pkglogger.Get()))
}

func (h *MemoryLifecycleHooks) onThreadStopped(ctx context.Context, evt threaddto.Stopped) {
	if h == nil {
		return
	}
	threadID := strings.TrimSpace(evt.ThreadID)
	if threadID == "" {
		return
	}
	go func() {
		if _, err := h.maybeScheduleAutoDream(ctx, threadID); err != nil && h.logger != nil && !errors.Is(err, context.Canceled) {
			h.logger.Warn("memory auto-dream stop hook failed", "thread_id", threadID, "error", err)
		}
	}()
}

func (h *MemoryLifecycleHooks) maybeScheduleAutoDream(ctx context.Context, threadID string) (bool, error) {
	if h == nil || h.consolidator == nil {
		return false, nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, nil
	}
	meta := h.resolveThreadRuntimeMetadata(ctx, threadID)
	if !meta.isAutoMemoryRootThread() || meta.hasAgentMemoryScope() {
		return false, nil
	}
	if !h.isGateOpen(meta) {
		return false, nil
	}
	root, err := resolvedStoreRoot(h.rootDir, h.projectRoot, h.autoMemPathOverride)
	if err != nil {
		return false, err
	}
	if err := rejectConsolidationPath(h.cfg, root); err != nil {
		return false, err
	}
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return false, err
	}
	now := h.now()
	if !shouldAutoDreamScan(stamp, now) {
		return false, nil
	}
	if err := recordConsolidationScan(root, now); err != nil {
		return false, err
	}
	lastSuccess := stamp.lastSuccessTime()
	if !lastSuccess.IsZero() && now.Sub(lastSuccess) < autoDreamMinInterval {
		return false, nil
	}
	sessionCount, err := h.autoDreamSessionCount(ctx, threadID, lastSuccess)
	if err != nil {
		return false, err
	}
	if sessionCount < autoDreamMinSessions {
		return false, nil
	}
	if h.consolidator.resolveExtractFunc(h.extractFn) == nil {
		return false, ErrConsolidationExtractFuncRequired
	}
	taskCtx, started := h.startDreamTask(threadID)
	if !started {
		return false, nil
	}
	go func() {
		defer h.finishDreamTask()
		err := h.consolidator.consolidateWithOptions(taskCtx, root, h.extractFn, consolidationRunOptions{
			cfg: h.cfg,
			now: h.now,
			onLocked: func() {
				h.setDreamTaskPhase(dreamTaskPhaseUpdating)
			},
		})
		if err != nil && h.logger != nil && !errors.Is(err, context.Canceled) {
			h.logger.Warn("memory auto-dream execution failed", "thread_id", threadID, "error", err)
		}
	}()
	return true, nil
}

func (h *MemoryLifecycleHooks) isGateOpen(meta threadRuntimeMetadata) bool {
	if h == nil || !meta.isAutoMemoryRootThread() || meta.hasAgentMemoryScope() {
		return false
	}
	gate := ResolveMemoryGate(meta.buildCtx(), h.cfg)
	if gate.KairosActive {
		return false
	}
	return gate.AutoEnabled
}

func (h *MemoryLifecycleHooks) autoDreamSessionCount(ctx context.Context, currentThreadID string, since time.Time) (int, error) {
	if h == nil || h.threadStore == nil {
		return 0, nil
	}
	lister, ok := h.threadStore.(autoDreamThreadLister)
	if !ok || lister == nil {
		return 0, nil
	}
	threads, err := lister.ListAll(ctx)
	if err != nil {
		return 0, err
	}
	projectKey := h.autoDreamProjectKey()
	count := 0
	for idx := range threads {
		thread := threads[idx]
		if strings.TrimSpace(thread.ThreadID) == "" || strings.TrimSpace(thread.ThreadID) == currentThreadID {
			continue
		}
		meta := resolveThreadRuntimeMetadataFromThread(&thread)
		if !meta.isAutoMemoryRootThread() || meta.hasAgentMemoryScope() {
			continue
		}
		if projectKey != "" && !sameAutoDreamProject(projectKey, strings.TrimSpace(thread.Cwd)) {
			continue
		}
		observedAt := threadObservedAt(thread)
		if !since.IsZero() && !observedAt.After(since) {
			continue
		}
		count++
	}
	return count, nil
}

func (h *MemoryLifecycleHooks) autoDreamProjectKey() string {
	if h == nil || strings.TrimSpace(h.autoMemPathOverride) != "" {
		return ""
	}
	return autoDreamProjectKey(strings.TrimSpace(h.projectRoot))
}

func autoDreamProjectKey(projectRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return ""
	}
	if canonical, err := FindCanonicalGitRoot(context.Background(), projectRoot); err == nil && strings.TrimSpace(canonical) != "" {
		return filepath.Clean(canonical)
	}
	if cleaned, err := cleanAbsolutePath(projectRoot); err == nil {
		return cleaned
	}
	return projectRoot
}

func sameAutoDreamProject(currentKey, cwd string) bool {
	if strings.TrimSpace(currentKey) == "" {
		return true
	}
	if strings.TrimSpace(cwd) == "" {
		return false
	}
	return autoDreamProjectKey(cwd) == currentKey
}

func threadObservedAt(thread threadstore.Thread) time.Time {
	switch {
	case thread.FinishedAt != nil && *thread.FinishedAt > 0:
		return time.Unix(*thread.FinishedAt, 0)
	case thread.UpdatedAt > 0:
		return time.Unix(thread.UpdatedAt, 0)
	case thread.CreatedAt > 0:
		return time.Unix(thread.CreatedAt, 0)
	default:
		return time.Time{}
	}
}

func shouldAutoDreamScan(stamp consolidationStamp, now time.Time) bool {
	lastScan := stamp.lastScanTime()
	if lastScan.IsZero() {
		return true
	}
	if now.IsZero() {
		now = time.Now()
	}
	return now.Sub(lastScan) >= autoDreamScanThrottle
}

func loadConsolidationStamp(root string) (consolidationStamp, error) {
	path, err := consolidationStampPath(root)
	if err != nil {
		return consolidationStamp{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return consolidationStamp{}, nil
	}
	if err != nil {
		return consolidationStamp{}, err
	}
	if len(raw) == 0 {
		return consolidationStamp{}, nil
	}
	var stamp consolidationStamp
	if err := json.Unmarshal(raw, &stamp); err != nil {
		return consolidationStamp{}, err
	}
	return stamp, nil
}

func saveConsolidationStamp(root string, stamp consolidationStamp) error {
	path, err := consolidationStampPath(root)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(stamp, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(path, raw, 0o644)
}

func consolidationStampPath(root string) (string, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return "", err
	}
	return ValidateMemoryWritePath(normalizedRoot, filepath.Join(normalizedRoot, consolidationStampFile))
}

func recordConsolidation(root string, when time.Time) error {
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return err
	}
	stamp.LastSuccessAt = stampTimeString(when)
	return saveConsolidationStamp(root, stamp)
}

func recordConsolidationScan(root string, when time.Time) error {
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return err
	}
	stamp.LastScanAt = stampTimeString(when)
	return saveConsolidationStamp(root, stamp)
}

func stampTimeString(when time.Time) string {
	if when.IsZero() {
		when = time.Now()
	}
	return when.UTC().Format(time.RFC3339Nano)
}

func (s consolidationStamp) lastScanTime() time.Time {
	return parseStampTime(s.LastScanAt)
}

func (s consolidationStamp) lastSuccessTime() time.Time {
	return parseStampTime(s.LastSuccessAt)
}

func parseStampTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func consolidationCandidates(entries []MemoryEntry) []MemoryEntry {
	unique := uniqueEntriesByCanonicalName(entries)
	selected := make([]MemoryEntry, 0, len(unique))
	for _, entry := range unique {
		if hasMeaningfulMemoryContent(entry.Content) {
			selected = append(selected, entry)
		}
	}
	return selected
}

func staleMemoryPaths(entries []MemoryEntry) []string {
	selected := make(map[string]MemoryEntry, len(entries))
	stale := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !hasMeaningfulMemoryContent(entry.Content) {
			if entry.FilePath != "" {
				stale = append(stale, entry.FilePath)
			}
			continue
		}
		key := entry.CanonicalName
		if key == "" {
			key = CanonicalName(entry.Frontmatter.Name)
		}
		current, exists := selected[key]
		if !exists || preferMemoryEntry(entry, current) {
			if exists && current.FilePath != "" {
				stale = append(stale, current.FilePath)
			}
			selected[key] = entry
			continue
		}
		if entry.FilePath != "" {
			stale = append(stale, entry.FilePath)
		}
	}
	return uniqueNonEmptyStrings(stale)
}

func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func removeMemoryFiles(root string, paths []string) error {
	for _, path := range paths {
		validatedPath, err := ValidateMemoryWritePath(root, path)
		if err != nil {
			return err
		}
		if err := os.Remove(validatedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writeConsolidatedMemories(root string, items []ExtractedMemory) error {
	for _, item := range items {
		if _, err := WriteMemoryFile(root, buildConsolidatedMemoryEntry(item)); err != nil {
			return err
		}
	}
	return nil
}

func buildConsolidatedMemoryEntry(item ExtractedMemory) MemoryEntry {
	item = normalizeExtractedMemory(item)
	description := truncateRunes(firstNonEmptyLine(item.Content), memoryHookMaxRunes)
	if description == "" {
		description = truncateRunes(item.Content, memoryHookMaxRunes)
	}
	return MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        consolidationName(item, description),
			Description: description,
			Type:        cloneMemoryType(item.Type),
			SearchKeys:  normalizeStringSlice(item.Tags),
		},
		Content: item.Content,
	}
}

func consolidationName(item ExtractedMemory, description string) string {
	if description != "" {
		return description
	}
	if item.Type.IsKnown() {
		return fmt.Sprintf("%s dream note", item.Type)
	}
	return "Dream note"
}
