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
	cfg            *Config
	now            func() time.Time
	onLocked       func()
	runtimeContext string
}

type consolidationStamp struct {
	LastScanAt    string `json:"last_scan_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
}

type preparedConsolidation struct {
	root           string
	cfg            *Config
	now            func() time.Time
	extractFn      ExtractFunc
	guard          *consolidationLockGuard
	runtimeContext string
}

type autoDreamExecutionPlan struct {
	root         string
	lastSuccess  time.Time
	sessionCount int
	extractFn    ExtractFunc
}
type autoDreamThreadLister interface {
	ListAll(ctx context.Context) ([]threadstore.Thread, error)
}

func (c *AutoDreamConsolidator) Consolidate(ctx context.Context, memoryRoot string, extractFn ExtractFunc) error {
	cfg, err := c.consolidationConfig(memoryRoot, nil)
	if err != nil {
		return err
	}
	return c.consolidateWithOptions(ctx, memoryRoot, extractFn, consolidationRunOptions{cfg: cfg})
}

func (c *AutoDreamConsolidator) consolidationConfig(path string, cfg *Config) (*Config, error) {
	if cfg != nil {
		return cfg, nil
	}
	if c != nil && c.cfg != nil {
		return c.cfg, nil
	}
	return nil, rejectConsolidationPath(nil, path)
}

func (c *AutoDreamConsolidator) consolidateWithOptions(
	ctx context.Context,
	memoryRoot string,
	extractFn ExtractFunc,
	opts consolidationRunOptions,
) (err error) {
	run, err := c.prepareConsolidation(ctx, memoryRoot, extractFn, opts)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if cleanupErr := run.guard.Complete(committed); err == nil && cleanupErr != nil {
			err = cleanupErr
		}
	}()
	input, err := loadConsolidationPromptInput(run.root, run.cfg)
	if err != nil {
		return err
	}
	input.Limit = c.limit()
	if !shouldRunConsolidationExtract(input) {
		err = refreshConsolidationWithoutExtract(run.root, run.now)
		committed = err == nil
		return err
	}
	err = c.runConsolidationExtract(ctx, run, input)
	committed = err == nil
	return err
}

func shouldRunConsolidationExtract(input consolidationPromptInput) bool {
	if len(consolidationCandidates(input.TopicEntries)) > 0 {
		return true
	}
	if len(input.LogDocuments) > 0 {
		return true
	}
	indexContent := strings.TrimSpace(input.Index.Content)
	return indexContent != "" && indexContent != "(missing)" && indexContent != "(empty)"
}

func refreshConsolidationWithoutExtract(root string, now func() time.Time) error {
	return withDiskStoreLock(root, func() error {
		if _, err := UpdateMemoryIndex(root); err != nil {
			return err
		}
		return recordConsolidation(root, now())
	})
}

func (c *AutoDreamConsolidator) prepareConsolidation(
	ctx context.Context,
	memoryRoot string,
	extractFn ExtractFunc,
	opts consolidationRunOptions,
) (preparedConsolidation, error) {
	if err := contextErr(ctx); err != nil {
		return preparedConsolidation{}, err
	}
	root, err := normalizeStoreRoot(memoryRoot)
	if err != nil {
		return preparedConsolidation{}, err
	}
	if opts.cfg, err = c.consolidationConfig(root, opts.cfg); err != nil {
		return preparedConsolidation{}, err
	}
	if err := rejectConsolidationPath(opts.cfg, root); err != nil {
		return preparedConsolidation{}, err
	}
	extractFn = c.resolveExtractFunc(extractFn)
	if extractFn == nil {
		return preparedConsolidation{}, ErrConsolidationExtractFuncRequired
	}
	now := opts.now
	if now == nil {
		now = time.Now
	}
	guard, err := acquireConsolidationLock(root, consolidationLockOptions{Now: now})
	if err != nil {
		return preparedConsolidation{}, err
	}
	if opts.onLocked != nil {
		opts.onLocked()
	}
	return preparedConsolidation{root: root, cfg: opts.cfg, now: now, extractFn: extractFn, guard: guard, runtimeContext: opts.runtimeContext}, nil
}

func (c *AutoDreamConsolidator) runConsolidationExtract(
	ctx context.Context,
	run preparedConsolidation,
	input consolidationPromptInput,
) error {
	promptText := appendConsolidationRuntimeContext(buildConsolidationPrompt(input), run.runtimeContext)
	raw, err := run.extractFn(ctx, promptText)
	if err != nil {
		return err
	}
	items, err := parseExtractedMemories(raw, input.Limit)
	if err != nil {
		return err
	}
	return withDiskStoreLock(run.root, func() error {
		if err := removeMemoryFiles(run.root, staleMemoryPaths(input.TopicEntries)); err != nil {
			return err
		}
		if err := writeConsolidatedMemories(run.root, items); err != nil {
			return err
		}
		if _, err := UpdateMemoryIndex(run.root); err != nil {
			return err
		}
		return recordConsolidation(run.root, run.now())
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
	threadID, ok := h.autoDreamThreadEligible(ctx, threadID)
	if !ok {
		return false, nil
	}
	plan, ok, err := h.prepareAutoDreamExecution(ctx, threadID)
	if err != nil || !ok {
		return false, err
	}
	taskCtx, started := h.startDreamTask(threadID)
	if !started {
		return false, nil
	}
	h.launchAutoDreamTask(taskCtx, threadID, plan)
	return true, nil
}

func (h *MemoryLifecycleHooks) autoDreamThreadEligible(ctx context.Context, threadID string) (string, bool) {
	if h == nil || h.consolidator == nil {
		return "", false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false
	}
	meta := h.resolveThreadRuntimeMetadata(ctx, threadID)
	if !h.autoDreamAllowed(meta) {
		return "", false
	}
	return threadID, true
}

func (h *MemoryLifecycleHooks) autoDreamAllowed(meta threadRuntimeMetadata) bool {
	return meta.isAutoMemoryRootThread() && !meta.hasAgentMemoryScope() && h.isGateOpen(meta)
}

func (h *MemoryLifecycleHooks) prepareAutoDreamExecution(ctx context.Context, threadID string) (autoDreamExecutionPlan, bool, error) {
	root, err := h.autoDreamRoot()
	if err != nil {
		return autoDreamExecutionPlan{}, false, err
	}
	lastSuccess, ok, err := h.prepareAutoDreamWindow(root)
	if err != nil || !ok {
		return autoDreamExecutionPlan{}, false, err
	}
	sessionCount, err := h.autoDreamSessionCount(ctx, threadID, lastSuccess)
	if err != nil {
		return autoDreamExecutionPlan{}, false, err
	}
	if sessionCount < autoDreamMinSessions {
		return autoDreamExecutionPlan{}, false, nil
	}
	extractFn, err := h.resolveDreamExtractFunc()
	if err != nil {
		return autoDreamExecutionPlan{}, false, err
	}
	return autoDreamExecutionPlan{
		root:         root,
		lastSuccess:  lastSuccess,
		sessionCount: sessionCount,
		extractFn:    extractFn,
	}, true, nil
}

func (h *MemoryLifecycleHooks) autoDreamRoot() (string, error) {
	root, err := resolvedStoreRoot(h.rootDir, h.projectRoot, h.autoMemPathOverride)
	if err != nil {
		return "", err
	}
	if err := rejectConsolidationPath(h.cfg, root); err != nil {
		return "", err
	}
	return root, nil
}

func (h *MemoryLifecycleHooks) prepareAutoDreamWindow(root string) (time.Time, bool, error) {
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return time.Time{}, false, err
	}
	now := h.now()
	if !shouldAutoDreamScan(stamp, now) {
		return time.Time{}, false, nil
	}
	if err := recordConsolidationScan(root, now); err != nil {
		return time.Time{}, false, err
	}
	lastSuccess := stamp.lastSuccessTime()
	if !lastSuccess.IsZero() && now.Sub(lastSuccess) < autoDreamMinInterval {
		return time.Time{}, false, nil
	}
	return lastSuccess, true, nil
}

func (h *MemoryLifecycleHooks) resolveDreamExtractFunc() (ExtractFunc, error) {
	extractFn := h.extractFn
	if extractFn == nil {
		extractFn = h.consolidator.resolveExtractFunc(nil)
	}
	if extractFn == nil {
		return nil, ErrConsolidationExtractFuncRequired
	}
	return extractFn, nil
}

func (h *MemoryLifecycleHooks) launchAutoDreamTask(taskCtx context.Context, threadID string, plan autoDreamExecutionPlan) {
	go func() {
		defer h.finishDreamTask()
		err := h.consolidator.consolidateWithOptions(taskCtx, plan.root, plan.extractFn, consolidationRunOptions{
			cfg:            h.cfg,
			now:            h.now,
			runtimeContext: buildConsolidationRuntimeContext("background auto-dream stop hook", plan.sessionCount, plan.lastSuccess, threadID),
			onLocked: func() {
				h.setDreamTaskPhase(dreamTaskPhaseUpdating)
			},
		})
		if err != nil && h.logger != nil && !errors.Is(err, context.Canceled) {
			h.logger.Warn("memory auto-dream execution failed", "thread_id", threadID, "error", err)
		}
	}()
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
		if shouldCountAutoDreamThread(threads[idx], currentThreadID, projectKey, since) {
			count++
		}
	}
	return count, nil
}

func shouldCountAutoDreamThread(thread threadstore.Thread, currentThreadID, projectKey string, since time.Time) bool {
	threadID := strings.TrimSpace(thread.ThreadID)
	if threadID == "" || threadID == currentThreadID {
		return false
	}
	meta := resolveThreadRuntimeMetadataFromThread(&thread)
	if !meta.isAutoMemoryRootThread() || meta.hasAgentMemoryScope() {
		return false
	}
	if projectKey != "" && !sameAutoDreamProject(projectKey, strings.TrimSpace(thread.Cwd)) {
		return false
	}
	observedAt := threadObservedAt(thread)
	return since.IsZero() || observedAt.After(since)
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
