package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
)

// executeMerge 执行merge。
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

func sameWorkspaceFilesystemPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

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
