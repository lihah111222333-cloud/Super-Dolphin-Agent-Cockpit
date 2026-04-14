package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type RootManager struct {
	svc Service
}

type AutoDreamConsolidator struct {
	extractor *MemoryExtractor
}

type memoryHookParams struct {
	fx.In

	Lifecycle       fx.Lifecycle
	Dispatcher      *event.Dispatcher      `optional:"true"`
	Hooks           *MemoryLifecycleHooks  `optional:"true"`
	ContextProvider *MemoryContextProvider `optional:"true"`
}

var Module = fx.Module("memory",
	fx.Provide(
		NewConfig,
		NewService,
		NewAgentMemoryManager,
		NewTeamMemoryManager,
		NewMemoryRuleEngine,
		NewRulesProvider,
		NewAgentMemoryPromptProvider,
		NewContextProvider,
		NewAutoDreamConsolidator,
		NewMemoryLifecycleHooks,
		NewMemoryExtractor,
	),
	fx.Invoke(registerLifecycle, registerPromptProviders, registerMemoryHooks),
)

const extractOnStopTimeout = 5 * time.Second

func NewRootManager(svc Service) *RootManager {
	return &RootManager{svc: svc}
}

func (m *RootManager) RootDir() string {
	if m == nil || m.svc == nil {
		return ""
	}
	return m.svc.RootDir()
}

func (m *RootManager) EnsureRoot(ctx context.Context) error {
	if m == nil || m.svc == nil {
		return errors.New("memory service is nil")
	}
	return m.svc.EnsureRoot(ctx)
}

func NewAutoDreamConsolidator(extractor *MemoryExtractor) *AutoDreamConsolidator {
	if extractor == nil {
		extractor = NewMemoryExtractor()
	}
	return &AutoDreamConsolidator{extractor: extractor}
}

func registerLifecycle(lc fx.Lifecycle, svc Service) {
	if svc == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return svc.EnsureRoot(ctx)
		},
	})
}

func registerMemoryHooks(p memoryHookParams) {
	if p.Dispatcher == nil {
		return
	}
	var cancels []context.CancelFunc
	appendCancel := func(cancel context.CancelFunc) {
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			registerLifecycleSubscriptions(p, appendCancel)
			return nil
		},
		OnStop: func(context.Context) error {
			cancelSubscriptions(cancels)
			cancels = nil
			return nil
		},
	})
}

func registerLifecycleSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	registerThreadHookSubscriptions(p, appendCancel)
	registerExtractOnStopSubscription(p, appendCancel)
	registerContextProviderSubscriptions(p, appendCancel)
}

func registerThreadHookSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	if p.Hooks == nil || !p.Hooks.enabled {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
		p.Hooks.onThreadStart(context.Background(), ev)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) {
		p.Hooks.onTurnEnd(context.Background(), ev)
	}, pkglogger.Get()))
}

func registerExtractOnStopSubscription(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	if p.Hooks == nil || !p.Hooks.extractOnStop {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Stopped) {
		dispatchExtractOnStop(p.Hooks, ev)
	}, pkglogger.Get()))
}

func registerContextProviderSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	if p.ContextProvider == nil || !p.ContextProvider.enabled {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStarted) {
		p.ContextProvider.onTurnStarted(context.Background(), ev)
	}, pkglogger.Get()))
	registerTurnTerminationSubscriptions(p, appendCancel)
}

func registerTurnTerminationSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	terminate := func(threadID, turnID string) {
		p.ContextProvider.onTurnTerminated(threadID, turnID)
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) {
		terminate(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInterrupted) {
		terminate(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStalled) {
		terminate(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
}

func cancelSubscriptions(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func dispatchExtractOnStop(hooks *MemoryLifecycleHooks, ev threaddto.Stopped) {
	if hooks == nil || !hooks.extractOnStop {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), extractOnStopTimeout)
		defer cancel()
		hooks.onThreadStopped(ctx, ev)
	}()
}

func mockAutoDreamExtractFunc(context.Context, string) (string, error) {
	return "", nil
}

func (c *AutoDreamConsolidator) Consolidate(ctx context.Context, memoryRoot string, extractFn ExtractFunc) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	root, err := normalizeStoreRoot(memoryRoot)
	if err != nil {
		return err
	}
	if extractFn == nil {
		extractFn = mockAutoDreamExtractFunc
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return withDiskStoreLock(root, func() error {
		return c.consolidateLocked(ctx, root, extractFn)
	})
}

func (c *AutoDreamConsolidator) consolidateLocked(ctx context.Context, memoryRoot string, extractFn ExtractFunc) error {
	entries, err := scanMemoryEntries(memoryRoot)
	if err != nil {
		return err
	}
	if err := removeMemoryFiles(memoryRoot, staleMemoryPaths(entries)); err != nil {
		return err
	}
	selected := consolidationCandidates(entries)
	if len(selected) == 0 {
		_, err = UpdateMemoryIndex(memoryRoot)
		return err
	}
	limit := extractLimit(len(selected), c.limit())
	raw, err := extractFn(ctx, buildConsolidationPrompt(memoryRoot, selected, limit))
	if err != nil {
		return err
	}
	items, err := parseExtractedMemories(raw, limit)
	if err != nil {
		return err
	}
	if err := writeConsolidatedMemories(memoryRoot, items); err != nil {
		return err
	}
	_, err = UpdateMemoryIndex(memoryRoot)
	return err
}

func (c *AutoDreamConsolidator) limit() int {
	if c == nil || c.extractor == nil {
		return defaultExtractMaxItems
	}
	return c.extractor.limit()
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

func buildConsolidationPrompt(memoryRoot string, entries []MemoryEntry, limit int) string {
	parts := []string{
		"Review the current durable memory files and merge duplicate or outdated notes into a cleaner long-term set.",
		"Return JSON in the form {\"memories\": [{\"content\":\"...\",\"type\":\"user|feedback|project|reference\",\"tags\":[\"...\"]}] }.",
		fmt.Sprintf("Return at most %d consolidated memories.", limit),
		"Existing memory files:",
		formatConsolidationEntries(memoryRoot, entries),
	}
	return strings.Join(parts, "\n\n")
}

func formatConsolidationEntries(memoryRoot string, entries []MemoryEntry) string {
	var builder strings.Builder
	for i, entry := range entries {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		_, _ = fmt.Fprintf(&builder, "### Memory %d\n", i+1)
		builder.WriteString("Path: ")
		builder.WriteString(relativeMemoryPath(memoryRoot, entry.FilePath))
		builder.WriteString("\nName: ")
		builder.WriteString(strings.TrimSpace(entry.Frontmatter.Name))
		builder.WriteString("\nType: ")
		builder.WriteString(string(entry.Type()))
		builder.WriteString("\nContent:\n")
		builder.WriteString(strings.TrimSpace(entry.Content))
	}
	return builder.String()
}

func relativeMemoryPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
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
