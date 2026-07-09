package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// editRecoveryLogEntry 记录“磁盘已写入但 LSP 同步未完成”的恢复线索。
// 这些事件用于排查工具调用中断后文件实际已经修改的情况。
type editRecoveryLogEntry struct {
	CreatedAt string `json:"created_at"`
	Event     string `json:"event"`
	FilePath  string `json:"file_path"`
	Confirmed string `json:"confirmed_by"`
	DiffBytes int    `json:"diff_bytes"`
	SyncError string `json:"sync_error,omitempty"`
}

// logEditDiskConfirmation 在 git diff 已确认改动落盘时写恢复日志。
// 即使 LSP sync 随后失败，调用方也能从该日志知道需要以磁盘状态为准继续处理。
func logEditDiskConfirmation(path string, diffBytes int, syncErr error) {
	var syncError string
	if syncErr != nil {
		syncError = syncErr.Error()
	}
	entry := editRecoveryLogEntry{
		CreatedAt: time.Now().Format(time.RFC3339Nano),
		Event:     "git_diff_confirmed",
		FilePath:  path,
		Confirmed: "git diff",
		DiffBytes: diffBytes,
		SyncError: syncError,
	}
	pkglogger.Warn("mcp-lsp patch_edit disk write confirmed before LSP sync returned",
		"file_path", path,
		"diff_bytes", diffBytes,
		"sync_error", syncError,
	)
	if err := appendEditRecoveryLog(entry); err != nil {
		pkglogger.Warn("mcp-lsp patch_edit recovery log write failed", "error", err)
	}
}

// appendEditRecoveryLog 以 JSONL 追加恢复事件；找不到日志目录时视为禁用。
func appendEditRecoveryLog(entry editRecoveryLogEntry) error {
	dir, ok := editRecoveryLogDir()
	if !ok {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	file, err := os.OpenFile(filepath.Join(dir, editRecoveryLogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(line)
	return err
}

// editRecoveryLogDir 优先使用显式 fallback 目录，其次复用当前 logger 文件目录。
// 两者都不可用时返回 false，让编辑主流程不因辅助日志失败而中断。
func editRecoveryLogDir() (string, bool) {
	if dir := strings.TrimSpace(os.Getenv("GO_AGENT_LOG_FALLBACK_DIR")); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", false
		}
		return abs, true
	}
	if logPath := strings.TrimSpace(pkglogger.CurrentLogFilePath()); logPath != "" {
		return filepath.Dir(logPath), true
	}
	return "", false
}
