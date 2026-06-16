package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type editRecoveryLogEntry struct {
	CreatedAt string `json:"created_at"`
	Event     string `json:"event"`
	FilePath  string `json:"file_path"`
	Confirmed string `json:"confirmed_by"`
	DiffBytes int    `json:"diff_bytes"`
	SyncError string `json:"sync_error,omitempty"`
}

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
	pkglogger.Warn("mcp-lsp edit disk write confirmed before LSP sync returned",
		"file_path", path,
		"diff_bytes", diffBytes,
		"sync_error", syncError,
	)
	if err := appendEditRecoveryLog(entry); err != nil {
		pkglogger.Warn("mcp-lsp edit recovery log write failed", "error", err)
	}
}

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
