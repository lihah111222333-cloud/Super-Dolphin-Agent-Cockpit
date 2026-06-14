package turn

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type ToolResultCleanupResult struct {
	Cleared      int
	Kept         int
	DeletedFiles int
}

type toolResultLifecycleEntry struct {
	meta   ToolResultMeta
	record ToolResultRecord
}

type toolResultLifecycleRegistry struct {
	mu      sync.Mutex
	threads map[string][]toolResultLifecycleEntry
}

var defaultToolResultLifecycleRegistry = &toolResultLifecycleRegistry{
	threads: map[string][]toolResultLifecycleEntry{},
}

func registerToolResultLifecycle(meta ToolResultMeta, record ToolResultRecord) {
	defaultToolResultLifecycleRegistry.Register(meta, record)
}

func cleanupToolResultLifecycle(threadID, model string, cfg *contract.FRCConfig) ToolResultCleanupResult {
	return defaultToolResultLifecycleRegistry.Cleanup(threadID, model, cfg)
}

func resetToolResultLifecycle(threadID string) {
	defaultToolResultLifecycleRegistry.Reset(threadID)
}

// Register 注册turn。
func (r *toolResultLifecycleRegistry) Register(meta ToolResultMeta, record ToolResultRecord) {
	threadID := strings.TrimSpace(meta.ThreadID)
	if r == nil || threadID == "" || record.OriginalSize == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.threads[threadID] = append(r.threads[threadID], toolResultLifecycleEntry{meta: meta, record: record})
}

// Cleanup 处理cleanup。
func (r *toolResultLifecycleRegistry) Cleanup(threadID, model string, cfg *contract.FRCConfig) ToolResultCleanupResult {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || cfg == nil || !cfg.EnabledForModel(model) {
		return ToolResultCleanupResult{}
	}
	keepRecent := cfg.KeepRecentCount()
	if keepRecent < 1 {
		keepRecent = 1
	}
	r.mu.Lock()
	entries := append([]toolResultLifecycleEntry(nil), r.threads[threadID]...)
	if len(entries) <= keepRecent {
		result := ToolResultCleanupResult{Kept: len(entries)}
		r.mu.Unlock()
		return result
	}
	cutoff := len(entries) - keepRecent
	stale := append([]toolResultLifecycleEntry(nil), entries[:cutoff]...)
	kept := append([]toolResultLifecycleEntry(nil), entries[cutoff:]...)
	if len(kept) == 0 {
		delete(r.threads, threadID)
	} else {
		r.threads[threadID] = kept
	}
	r.mu.Unlock()
	result := ToolResultCleanupResult{Cleared: len(stale), Kept: len(kept)}
	for _, entry := range stale {
		if deleteToolResultFile(entry.record.PersistedPath) {
			result.DeletedFiles++
		}
	}
	return result
}

// Reset 重置turn。
func (r *toolResultLifecycleRegistry) Reset(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return
	}
	r.mu.Lock()
	entries := append([]toolResultLifecycleEntry(nil), r.threads[threadID]...)
	delete(r.threads, threadID)
	r.mu.Unlock()
	for _, entry := range entries {
		deleteToolResultFile(entry.record.PersistedPath)
	}
}

func deleteToolResultFile(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false
	}
	pruneToolResultDir(filepath.Dir(path))
	return true
}

// pruneToolResultDir 裁剪工具结果目录。
func pruneToolResultDir(dir string) {
	for _, stop := range toolResultCleanupRoots() {
		for current := filepath.Clean(strings.TrimSpace(dir)); current != "." && current != string(filepath.Separator); current = filepath.Dir(current) {
			if current == stop {
				return
			}
			if err := os.Remove(current); err != nil {
				return
			}
		}
	}
}

func toolResultCleanupRoots() []string {
	roots := []string{}
	if cacheDir, err := os.UserCacheDir(); err == nil && strings.TrimSpace(cacheDir) != "" {
		roots = append(roots, filepath.Join(cacheDir, "super-agent-v3"))
	}
	roots = append(roots, filepath.Join(os.TempDir(), "super-agent-v3"))
	return roots
}
