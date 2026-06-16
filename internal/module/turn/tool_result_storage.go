package turn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

type ToolResultMeta struct {
	ThreadID  string
	TurnID    string
	CallID    string
	ToolName  string
	Timestamp time.Time
}

type ToolResultRecord struct {
	Preview       string
	PersistedPath string
	Truncated     bool
	OriginalSize  int
}

// CaptureToolResult 生成capture工具结果。
func CaptureToolResult(meta ToolResultMeta, raw string) ToolResultRecord {
	originalSize := toolResultCharCount(raw)
	if originalSize == 0 {
		return ToolResultRecord{}
	}
	previewSource := raw
	if originalSize > toolResultPersistThresholdChars {
		previewSource = truncateToolResultChars(raw, toolResultPersistThresholdChars)
	}
	preview, budgetTruncated := takeToolResultPreview(meta.ThreadID, meta.TurnID, previewSource)
	if toolResultCharCount(preview) < originalSize {
		preview = repairTruncatedJSON(raw, preview)
	}
	record := ToolResultRecord{
		Preview:      preview,
		Truncated:    toolResultCharCount(preview) < originalSize,
		OriginalSize: originalSize,
	}
	if originalSize > toolResultPersistThresholdChars || budgetTruncated {
		record.PersistedPath = persistToolResult(meta, raw)
	}
	registerToolResultLifecycle(meta, record)
	return record
}

func persistToolResult(meta ToolResultMeta, raw string) string {
	dir, err := toolResultStorageDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, toolResultFileName(meta))
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		return ""
	}
	return path
}

func toolResultStorageDir() (string, error) {
	dir := kernel.ToolResultsCacheDir()
	if dir == "" {
		return "", errors.New("tool-results: cache base unavailable")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func toolResultFileName(meta ToolResultMeta) string {
	ts := meta.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	parts := []string{ts.UTC().Format("20060102-150405.000000000")}
	for _, raw := range []string{meta.TurnID, meta.CallID, meta.ToolName, meta.ThreadID} {
		if segment := sanitizeToolResultSegment(raw); segment != "" {
			parts = append(parts, segment)
		}
	}
	return strings.Join(parts, "_") + ".txt"
}

// sanitizeToolResultSegment 清理工具结果segment。
func sanitizeToolResultSegment(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}
