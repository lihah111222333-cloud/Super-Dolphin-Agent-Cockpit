package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	storeworkspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/workspace"
)

// executeMerge 执行真实 merge。
// 文件系统写入和 file 状态持久化分步完成，任何失败都会转入 failed 并回滚状态快照。
func (s *service) executeMerge(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	updatedBy string,
) (*MergeRunResult, error) {
	emptyResult, _ := s.planMerge(run, nil, nil, req.DryRun)
	files, err := s.store.ListFiles(ctx, storeworkspace.ListFilesFilter{
		RunKey: run.RunKey,
		Limit:  mergeListLimit,
	})
	if err != nil {
		return nil, s.failMergeRun(ctx, run, req, emptyResult, nil, updatedBy, err)
	}
	result, updates, err := s.buildMergePlan(run, files, req)
	if err != nil {
		return nil, s.failMergeRun(ctx, run, req, emptyResult, nil, updatedBy, err)
	}
	result.DryRun = req.DryRun
	updates = s.applyMergeFilesystem(run, result, updates)
	if err := s.applyFileUpdates(ctx, updates); err != nil {
		return nil, s.failMergeRun(ctx, run, req, result, files, updatedBy, err)
	}
	if result.Conflicts > 0 || result.Errors > 0 {
		failedRun, err := s.transitionMergeFailed(ctx, run, req, result, updatedBy, "")
		if err != nil {
			return nil, err
		}
		result.Status = failedRun.Status
		s.emitRunStatusChanged(run.Status, failedRun)
		s.emitRunMergeErrorEvent(failedRun, result, updatedBy, "")
		return result, nil
	}
	mergedRun, err := s.transitionMergeRun(ctx, run, statusMerging, statusMerged, req, updatedBy, result, "")
	if err != nil {
		return nil, s.failMergeRun(ctx, run, req, result, files, updatedBy, err)
	}
	result.Status = mergedRun.Status
	s.emitRunMergedEvent(mergedRun, result)
	return result, nil
}

// applyMergeFilesystem 根据文件状态执行写回或删除。
// 它会同步更新 result.Files，确保文件系统失败能反映到最终摘要。
func (s *service) applyMergeFilesystem(
	run *Run,
	result *MergeRunResult,
	files []storeworkspace.WorkspaceRunFile,
) []storeworkspace.WorkspaceRunFile {
	updated := append([]storeworkspace.WorkspaceRunFile(nil), files...)
	for i := range updated {
		updated[i], result.Files[i] = applyMergeFilesystemFile(run, updated[i], result.Files[i])
	}
	recountMergeResult(result)
	return updated
}

// applyMergeFilesystemFile 对单个文件执行 merge 文件系统动作。
func applyMergeFilesystemFile(
	run *Run,
	file storeworkspace.WorkspaceRunFile,
	item MergeFileResult,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	switch file.State {
	case fileStateMerged:
		return writeMergedSourceFile(run, file, item)
	case fileStateRemoved:
		return removeMergedSourceFile(run, file, item)
	default:
		return file, item
	}
}

// writeMergedSourceFile 将 workspace 文件原子写回 source root。
// 写入前会确认 workspace 文件真实路径没有通过 symlink 逃出 workspace。
func writeMergedSourceFile(
	run *Run,
	file storeworkspace.WorkspaceRunFile,
	item MergeFileResult,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	workspacePath := filepath.Join(run.WorkspacePath, file.RelativePath)
	if err := ensureWorkspaceMergeSource(run.WorkspacePath, workspacePath); err != nil {
		return mergeFileError(file, fmt.Errorf("workspace file %q: %w", file.RelativePath, err).Error())
	}
	info, err := os.Stat(workspacePath)
	if err != nil {
		return mergeFileError(file, fmt.Errorf("stat workspace file %q: %w", file.RelativePath, err).Error())
	}
	sourcePath := filepath.Join(run.SourceRoot, file.RelativePath)
	if err := copyFileAtomic(workspacePath, sourcePath, info.Mode().Perm()); err != nil {
		return mergeFileError(file, fmt.Errorf("write source file %q: %w", file.RelativePath, err).Error())
	}
	sourceAfter, err := hashFile(sourcePath)
	if err != nil {
		return mergeFileError(file, fmt.Errorf("hash source file %q: %w", file.RelativePath, err).Error())
	}
	file.SourceSHA256After = sourceAfter
	file.LastError = ""
	return file, item
}

// ensureWorkspaceMergeSource 确认待合并文件的真实路径仍在 workspace 内，避免链接把外部文件合并进源码。
func ensureWorkspaceMergeSource(workspaceRoot, workspacePath string) error {
	root := filepath.Clean(workspaceRoot)
	target := filepath.Clean(workspacePath)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes workspace root")
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	targetReal, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	expectedReal := filepath.Join(rootReal, rel)
	if !sameWorkspaceFilesystemPath(targetReal, expectedReal) {
		return fmt.Errorf("path escapes workspace root")
	}
	return nil
}

// sameWorkspaceFilesystemPath 比较文件系统路径。
// Windows 下大小写不敏感，其他平台按清理后的路径精确比较。
func sameWorkspaceFilesystemPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// removeMergedSourceFile 删除 source root 中对应文件。
// 删除不存在文件视为成功，方便重复清理已完成的删除动作。
func removeMergedSourceFile(
	run *Run,
	file storeworkspace.WorkspaceRunFile,
	item MergeFileResult,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	sourcePath := filepath.Join(run.SourceRoot, file.RelativePath)
	if err := os.Remove(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return mergeFileError(file, fmt.Errorf("remove source file %q: %w", file.RelativePath, err).Error())
	}
	file.SourceSHA256After = ""
	file.LastError = ""
	return file, item
}

// transitionMergeFailed 将 merging run 标记为 failed。
// 状态转换失败时仍先发 merge error 事件，避免故障完全静默。
func (s *service) transitionMergeFailed(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	result *MergeRunResult,
	updatedBy, message string,
) (*Run, error) {
	failedRun, err := s.transitionMergeRun(ctx, run, statusMerging, statusFailed, req, updatedBy, result, message)
	if err != nil {
		s.emitRunMergeErrorEvent(run, result, updatedBy, message)
		return nil, err
	}
	return failedRun, nil
}

// failMergeRun 统一处理 merge 失败收尾。
// 它会恢复原始 file 状态、转 failed、发布状态和错误事件。
func (s *service) failMergeRun(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	result *MergeRunResult,
	files []RunFile,
	updatedBy string,
	cause error,
) error {
	mergeErr := s.rollbackMergeState(ctx, files, cause)
	failedRun, err := s.transitionMergeFailed(ctx, run, req, result, updatedBy, mergeErr.Error())
	if err != nil {
		return errors.Join(mergeErr, err)
	}
	result.Status = failedRun.Status
	s.emitRunStatusChanged(run.Status, failedRun)
	s.emitRunMergeErrorEvent(failedRun, result, updatedBy, mergeErr.Error())
	return mergeErr
}
