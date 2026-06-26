package wails

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultClipboardRetention 是启动时清理临时剪贴板 PNG 的年龄阈值。
// 历史渲染仍可从会话元数据中的 data URL 恢复图片，因此删除临时文件不会破坏旧消息。
const defaultClipboardRetention = 7 * 24 * time.Hour

// cleanupStaleClipboardImages 清理超过 retention 的临时剪贴板 PNG。
// 单文件错误只记录并跳过，启动清理不能阻断桌面应用。
func cleanupStaleClipboardImages(logger *slog.Logger, dir string, retention time.Duration) (removed, kept int) {
	if retention <= 0 || strings.TrimSpace(dir) == "" {
		return 0, 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		logCleanupWarn(logger, "read tmp dir failed", "dir", dir, "err", err)
		return 0, 0
	}
	cutoff := time.Now().Add(-retention)
	for _, entry := range entries {
		switch handleClipboardEntry(logger, dir, entry, cutoff) {
		case cleanupResultRemoved:
			removed++
		case cleanupResultKept:
			kept++
		}
	}
	logCleanupSummary(logger, dir, retention, removed, kept)
	return removed, kept
}

// cleanupResult 表示单个剪贴板临时文件的清理结果。
type cleanupResult int

const (
	// 剪贴板临时文件清理结果。
	cleanupResultSkipped cleanupResult = iota
	cleanupResultRemoved
	cleanupResultKept
)

// handleClipboardEntry 判断并清理单个临时文件，复杂分支从主流程拆出以便维护。
func handleClipboardEntry(logger *slog.Logger, dir string, entry fs.DirEntry, cutoff time.Time) cleanupResult {
	if !isClipboardCleanupCandidate(entry) {
		return cleanupResultSkipped
	}
	info, err := entry.Info()
	if err != nil {
		return cleanupResultSkipped
	}
	if info.ModTime().After(cutoff) {
		return cleanupResultKept
	}
	full := filepath.Join(dir, entry.Name())
	if err := os.Remove(full); err != nil {
		logCleanupWarn(logger, "remove failed", "path", full, "err", err)
		return cleanupResultSkipped
	}
	return cleanupResultRemoved
}

// logCleanupSummary 在有实际清理活动时输出汇总日志。
func logCleanupSummary(logger *slog.Logger, dir string, retention time.Duration, removed, kept int) {
	if logger == nil || (removed == 0 && kept == 0) {
		return
	}
	logger.Info("clipboard cleanup",
		"dir", dir,
		"removed", removed,
		"kept", kept,
		"retention", retention.String(),
	)
}

// isClipboardCleanupCandidate 判断目录项是否是本应用创建的剪贴板 PNG。
func isClipboardCleanupCandidate(entry fs.DirEntry) bool {
	if entry == nil || entry.IsDir() {
		return false
	}
	name := entry.Name()
	if !strings.HasPrefix(name, "clipboard-") {
		return false
	}
	return strings.EqualFold(filepath.Ext(name), ".png")
}

// logCleanupWarn 在 logger 可用时记录剪贴板清理警告。
func logCleanupWarn(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Warn("clipboard cleanup: "+msg, args...)
}
