package turn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/toolresults"
)

// ToolResultMeta 描述一次工具调用结果的归属，用于预算作用域、文件命名和生命周期登记。
type ToolResultMeta struct {
	ThreadID  string
	TurnID    string
	CallID    string
	ToolName  string
	Timestamp time.Time
}

// ToolResultRecord 是传回 provider 上下文的预览和可选落盘路径。
type ToolResultRecord struct {
	Preview       string
	PersistedPath string
	PersistFailed bool
	PersistError  string
	Truncated     bool
	OriginalSize  int
}

// ToolResultRuntime 持有单个应用实例的工具结果预算与生命周期状态。
type ToolResultRuntime struct {
	budget    *toolResultBudgetRegistry
	lifecycle *toolResultLifecycleRegistry
}

// NewToolResultRuntime 创建工具结果运行时 owner；每个应用 Fx 图必须只注入同一实例。
func NewToolResultRuntime() *ToolResultRuntime {
	return &ToolResultRuntime{
		budget:    &toolResultBudgetRegistry{budgets: map[string]*toolResultBudget{}},
		lifecycle: &toolResultLifecycleRegistry{threads: map[string][]toolResultLifecycleEntry{}},
	}
}

// Capture 生成可放入上下文的工具结果预览，必要时把完整结果落盘并登记清理。
func (r *ToolResultRuntime) Capture(meta ToolResultMeta, raw string) ToolResultRecord {
	r.require()
	originalSize := toolResultCharCount(raw)
	if originalSize == 0 {
		return ToolResultRecord{}
	}
	previewSource := raw
	if originalSize > toolResultPersistThresholdChars {
		previewSource = truncateToolResultChars(raw, toolResultPersistThresholdChars)
	}
	preview, budgetTruncated := r.budget.Take(toolResultScope(meta.ThreadID, meta.TurnID), previewSource)
	if toolResultCharCount(preview) < originalSize {
		preview = repairTruncatedJSON(raw, preview)
	}
	record := ToolResultRecord{
		Preview:      preview,
		Truncated:    toolResultCharCount(preview) < originalSize,
		OriginalSize: originalSize,
	}
	if originalSize > toolResultPersistThresholdChars || budgetTruncated {
		path, err := persistToolResult(meta, raw)
		if err != nil {
			record.PersistFailed = true
			record.PersistError = err.Error()
		} else {
			record.PersistedPath = path
		}
	}
	r.lifecycle.Register(meta, record)
	return record
}

// ResetScope 在 turn 结束时释放该 turn 的工具结果预览预算。
func (r *ToolResultRuntime) ResetScope(threadID, turnID string) {
	r.require()
	r.budget.Reset(toolResultScope(threadID, turnID))
}

// Cleanup 按模型级 FRC 配置裁剪线程内旧工具结果。
func (r *ToolResultRuntime) Cleanup(threadID, model string, cfg *contract.FRCConfig) ToolResultCleanupResult {
	r.require()
	return r.lifecycle.Cleanup(threadID, model, cfg)
}

// ResetThread 在线程清理时移除该线程所有已登记的大工具结果。
func (r *ToolResultRuntime) ResetThread(threadID string) {
	r.require()
	r.lifecycle.Reset(threadID)
}

// require 校验 owner 已完整构造，避免预算与生命周期在缺失依赖下分叉。
func (r *ToolResultRuntime) require() {
	if r == nil || r.budget == nil || r.lifecycle == nil {
		// archguard:ignore panic_count -- 缺失 owner 会让预算与生命周期跨应用共享，必须立即阻断。
		panic("tool result runtime is required")
	}
}

// persistToolResult 把完整工具结果写入私有缓存文件，并把失败原因返回给上层诊断。
func persistToolResult(meta ToolResultMeta, raw string) (string, error) {
	dir, err := toolResultStorageDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, toolResultFileName(meta))
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// toolResultStorageDir 确保工具结果缓存目录存在，并在找不到缓存根时返回错误。
func toolResultStorageDir() (string, error) {
	dir := toolresults.CacheDir()
	if dir == "" {
		return "", errors.New("tool-results: cache base unavailable")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// toolResultFileName 用时间和清洗后的归属字段生成稳定且不会穿越目录的文件名。
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

// sanitizeToolResultSegment 只保留小写字母和数字，其他字符压成连字符用于文件名片段。
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
