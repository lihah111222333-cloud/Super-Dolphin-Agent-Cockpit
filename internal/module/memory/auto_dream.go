package memory

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrConsolidationExtractFuncRequired = errors.New("dream extract func is not configured")

type AutoDreamConsolidator struct {
	cfg       *Config
	extractor *MemoryExtractor
	extractFn ExtractFunc
	locks     *diskLockCoordinator
}

type consolidationRunOptions struct {
	cfg            *Config
	now            func() time.Time
	onLocked       func()
	runtimeContext string
}

type preparedConsolidation struct {
	root           string
	cfg            *Config
	now            func() time.Time
	extractFn      ExtractFunc
	guard          *consolidationLockGuard
	runtimeContext string
	locks          *diskLockCoordinator
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
		err = refreshConsolidationWithoutExtract(run.root, run.now, run.locks)
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

func refreshConsolidationWithoutExtract(root string, now func() time.Time, locks *diskLockCoordinator) error {
	if locks == nil {
		locks = newDiskLockCoordinator()
	}
	return locks.withDiskStoreLock(root, func() error {
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
	return preparedConsolidation{root: root, cfg: opts.cfg, now: now, extractFn: extractFn, guard: guard, runtimeContext: opts.runtimeContext, locks: c.locks}, nil
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
	return run.locks.withDiskStoreLock(run.root, func() error {
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
