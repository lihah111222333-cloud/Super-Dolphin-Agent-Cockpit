package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// dedupeRelativePaths 校验并去重相对文件路径。
// 这里不保留重复项，避免同一文件在 run file 表里出现多份状态。
func dedupeRelativePaths(files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, raw := range files {
		rel, err := validateRelativePath(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	return out, nil
}

// planMerge 对所有跟踪文件生成 merge 结果和待持久化 file 状态。
func (s *service) planMerge(
	run *Run,
	files []RunFile,
	removed map[string]removedWorkspaceFile,
	dryRun bool,
) (*MergeRunResult, []storeworkspace.WorkspaceRunFile) {
	result := &MergeRunResult{
		RunKey:        run.RunKey,
		Status:        run.Status,
		SourceRoot:    run.SourceRoot,
		WorkspacePath: run.WorkspacePath,
		Files:         make([]MergeFileResult, 0, len(files)),
	}
	updates := make([]storeworkspace.WorkspaceRunFile, 0, len(files))
	for _, file := range files {
		updated, item := evaluateTrackedMergeFile(run, file, removed, dryRun)
		updates = append(updates, updated)
		recordMergeItem(result, item)
	}
	return result, updates
}

// recordMergeItem 追加单文件结果并更新汇总计数。
func recordMergeItem(result *MergeRunResult, item MergeFileResult) {
	result.Files = append(result.Files, item)
	countMergeItem(result, item)
}

// countMergeItem 根据单文件 action 更新 merge 汇总计数。
func countMergeItem(result *MergeRunResult, item MergeFileResult) {
	switch item.Action {
	case "merged":
		result.Merged++
	case "removed", "would_remove":
		result.Removed++
	case "conflict":
		result.Conflicts++
	case "unchanged":
		result.Unchanged++
	case "error":
		result.Errors++
	}
}

// recountMergeResult 重新计算 merge 汇总计数。
// 文件系统写入后 action 可能从 merged 变成 error，因此需要重算。
func recountMergeResult(result *MergeRunResult) {
	result.Merged = 0
	result.Removed = 0
	result.Conflicts = 0
	result.Unchanged = 0
	result.Errors = 0
	for _, item := range result.Files {
		countMergeItem(result, item)
	}
}

// mergeFileError 标记单文件 merge 错误并保留原因。
func mergeFileError(file storeworkspace.WorkspaceRunFile, reason string) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	file.State = fileStateError
	file.SourceSHA256After = ""
	file.LastError = reason
	return file, MergeFileResult{Path: file.RelativePath, Action: "error", Reason: reason}
}

// applyFileUpdates 持久化 merge 后的文件状态。
func (s *service) applyFileUpdates(ctx context.Context, files []storeworkspace.WorkspaceRunFile) error {
	return upsertRunFiles(ctx, s.store, files)
}

// rollbackMergeState 在 merge 失败时恢复原始文件状态记录。
func (s *service) rollbackMergeState(ctx context.Context, original []RunFile, cause error) error {
	if restoreErr := s.restoreRunFiles(ctx, original); restoreErr != nil {
		return errors.Join(cause, restoreErr)
	}
	return cause
}

// restoreRunFiles 将 run file 记录恢复为传入快照。
func (s *service) restoreRunFiles(ctx context.Context, files []RunFile) error {
	restored := make([]storeworkspace.WorkspaceRunFile, 0, len(files))
	for _, file := range files {
		restored = append(restored, storeworkspace.WorkspaceRunFile(file))
	}
	return upsertRunFiles(ctx, s.store, restored)
}

// persistRun 在同一事务中写入 run 和初始 file 记录。
func (s *service) persistRun(ctx context.Context, run storeworkspace.WorkspaceRun, files []string) (*Run, error) {
	var saved *Run
	err := s.store.WithTx(ctx, func(txStore storeworkspace.Store) error {
		created, err := txStore.UpsertRun(ctx, run)
		if err != nil {
			return err
		}
		prepared, err := prepareRunFiles(created, files)
		if err != nil {
			return err
		}
		if err := upsertRunFiles(ctx, txStore, prepared); err != nil {
			return err
		}
		saved = created
		return nil
	})
	return saved, err
}

// emitRunCreated 发布 workspace run 创建事件。
func (s *service) emitRunCreated(run *Run) {
	s.emitCreated(WorkspaceRunCreated{
		WorkspaceRunHeader: workspaceRunHeader(run),
		ID:                 run.ID,
		SourceRoot:         run.SourceRoot,
		WorkspacePath:      run.WorkspacePath,
		Status:             run.Status,
		CreatedBy:          run.CreatedBy,
		UpdatedBy:          run.UpdatedBy,
		Metadata:           append(json.RawMessage(nil), run.Metadata...),
		CreatedAt:          run.CreatedAt,
		UpdatedAt:          run.UpdatedAt,
		FinishedAt:         cloneTimePtr(run.FinishedAt),
	})
}

// emitRunStatusChanged 发布 workspace run 状态变化事件。
func (s *service) emitRunStatusChanged(oldStatus string, run *Run) {
	s.emitStatusChange(WorkspaceRunStatusChanged{
		WorkspaceRunHeader: workspaceRunHeader(run),
		OldStatus:          oldStatus,
		NewStatus:          run.Status,
		UpdatedBy:          run.UpdatedBy,
	})
}

// emitRunMergedEvent 发布 merge 成功事件。
func (s *service) emitRunMergedEvent(run *Run, result *MergeRunResult) {
	s.emitMerged(WorkspaceRunMerged{
		WorkspaceRunHeader: workspaceRunHeader(run),
		SourceRoot:         run.SourceRoot,
		WorkspacePath:      run.WorkspacePath,
		Status:             run.Status,
		DryRun:             result.DryRun,
		MergedFileCount:    result.Merged,
		Removed:            result.Removed,
		Conflicts:          result.Conflicts,
		Unchanged:          result.Unchanged,
		Errors:             result.Errors,
		UpdatedBy:          run.UpdatedBy,
	})
}

// emitRunMergeErrorEvent 发布 merge 冲突或失败事件。
func (s *service) emitRunMergeErrorEvent(run *Run, result *MergeRunResult, updatedBy, message string) {
	s.emitMergeError(WorkspaceRunMergeError{
		WorkspaceRunHeader: workspaceRunHeader(run),
		SourceRoot:         run.SourceRoot,
		WorkspacePath:      run.WorkspacePath,
		Conflicts:          result.Conflicts,
		Errors:             result.Errors,
		Message:            mergeIssueMessage(result, message),
		UpdatedBy:          updatedBy,
	})
}

// mergeIssueMessage 选择最适合展示的 merge 问题摘要。
func mergeIssueMessage(result *MergeRunResult, fallback string) string {
	if text := strings.TrimSpace(fallback); text != "" {
		return text
	}
	for _, item := range result.Files {
		if reason := strings.TrimSpace(item.Reason); reason != "" {
			return reason
		}
	}
	if result.Errors > 0 {
		return "merge failed"
	}
	if result.Conflicts > 0 {
		return "merge conflict"
	}
	return ""
}

// emitRunAbortedEvent 发布 workspace run abort 事件。
func (s *service) emitRunAbortedEvent(run *Run, reason string) {
	s.emitAborted(WorkspaceRunAborted{
		WorkspaceRunHeader: workspaceRunHeader(run),
		SourceRoot:         run.SourceRoot,
		WorkspacePath:      run.WorkspacePath,
		Status:             run.Status,
		Reason:             strings.TrimSpace(reason),
		UpdatedBy:          run.UpdatedBy,
	})
}

// workspaceRunHeader 构造所有 workspace 事件共享的头部。
func workspaceRunHeader(run *Run) WorkspaceRunHeader {
	return WorkspaceRunHeader{
		EventHeader: EventHeader{Timestamp: time.Now()},
		DagKey:      run.DagKey,
		RunKey:      run.RunKey,
	}
}

// cloneTimePtr 复制可选时间指针，避免事件持有可变共享地址。
func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// validateRelativePath 规范化并拒绝绝对路径或向上逃逸路径。
func validateRelativePath(raw string) (string, error) {
	path := platformshared.NormalizeRelativePath(raw)
	if path == "." {
		return "", errors.New("file path is required")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("file path %q must be relative", raw)
	}
	if path == ".." || strings.HasPrefix(path, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("file path %q escapes sourceRoot", raw)
	}
	return path, nil
}

// hashFile 计算文件 SHA-256。
func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// hashFileIfExists 在文件存在时计算 SHA-256。
// 不存在返回空字符串，其他 stat 错误直接返回。
func hashFileIfExists(path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return hashFile(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return "", nil
}
