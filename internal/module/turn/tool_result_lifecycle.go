package turn

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// ToolResultCleanupResult 汇总一次工具结果生命周期清理的内存记录和落盘文件数量。
type ToolResultCleanupResult struct {
	Cleared      int
	Kept         int
	DeletedFiles int
}

// toolResultLifecycleEntry 绑定一条工具结果元数据和实际存储记录，供按 turn 维度裁剪。
type toolResultLifecycleEntry struct {
	meta   ToolResultMeta
	record ToolResultRecord
}

// toolResultLifecycleRegistry 记录每个线程最近的大工具结果，清理时只保留 FRC 允许的尾部记录。
type toolResultLifecycleRegistry struct {
	mu      sync.Mutex
	threads map[string][]toolResultLifecycleEntry
}

var defaultToolResultLifecycleRegistry = &toolResultLifecycleRegistry{
	threads: map[string][]toolResultLifecycleEntry{},
}

// registerToolResultLifecycle 把持久化过的大工具结果登记到默认生命周期表。
func registerToolResultLifecycle(meta ToolResultMeta, record ToolResultRecord) {
	defaultToolResultLifecycleRegistry.Register(meta, record)
}

// cleanupToolResultLifecycle 按模型级 FRC 配置裁剪线程内旧工具结果。
func cleanupToolResultLifecycle(threadID, model string, cfg *contract.FRCConfig) ToolResultCleanupResult {
	return defaultToolResultLifecycleRegistry.Cleanup(threadID, model, cfg)
}

// resetToolResultLifecycle 在线程清理时移除该线程所有已登记的大工具结果。
func resetToolResultLifecycle(threadID string) {
	defaultToolResultLifecycleRegistry.Reset(threadID)
}

// Register 记录一次可清理的工具结果；空线程或未落盘记录不会进入生命周期表。
func (r *toolResultLifecycleRegistry) Register(meta ToolResultMeta, record ToolResultRecord) {
	threadID := strings.TrimSpace(meta.ThreadID)
	if r == nil || threadID == "" || record.OriginalSize == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.threads[threadID] = append(r.threads[threadID], toolResultLifecycleEntry{meta: meta, record: record})
}

// Cleanup 保留指定线程最近的工具结果记录，并在释放锁后删除旧记录对应的落盘文件。
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

// Reset 删除线程的全部生命周期记录，并在锁外清理对应的落盘文件。
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

// deleteToolResultFile 删除单个工具结果文件；文件已不存在时视为清理成功。
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

// pruneToolResultDir 从结果文件目录向上删除空目录，但不会越过用户缓存或临时根目录。
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

// toolResultCleanupRoots 返回允许 pruneToolResultDir 停止上溯的安全根目录。
func toolResultCleanupRoots() []string {
	roots := []string{}
	if cacheDir, err := os.UserCacheDir(); err == nil && strings.TrimSpace(cacheDir) != "" {
		roots = append(roots, filepath.Join(cacheDir, "super-agent-v3"))
	}
	roots = append(roots, filepath.Join(os.TempDir(), "super-agent-v3"))
	return roots
}
