package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
)

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
	mergedRun, err := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     run.RunKey,
		FromStatus: statusMerging,
		Status:     statusMerged,
		UpdatedBy:  updatedBy,
		Metadata:   mergeMetadata(result, req, ""),
	})
	if err != nil {
		return nil, s.failMergeRun(ctx, run, req, result, files, updatedBy, err)
	}
	result.Status = mergedRun.Status
	s.emitRunStatusChanged(run.Status, mergedRun)
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

func copyFileAtomic(source, target string, perm os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp, tmpPath, err := prepareAtomicTarget(target)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	defer func() {
		cleanup()
	}()
	if err := writeAtomicTempFile(tmp, in); err != nil {
		return err
	}
	if err := finishAtomicCopy(tmp, tmpPath, target, perm); err != nil {
		return err
	}
	cleanup = func() {}
	return nil
}

func prepareAtomicTarget(target string) (*os.File, string, error) {
	if err := ensureAtomicTarget(target); err != nil {
		return nil, "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".workspace-merge-*")
	if err != nil {
		return nil, "", err
	}
	return tmp, tmp.Name(), nil
}

func ensureAtomicTarget(target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if stat, err := os.Lstat(target); err == nil && stat.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("target is symlink: %s", target)
	}
	return nil
}

func writeAtomicTempFile(tmp *os.File, in io.Reader) error {
	if _, err := io.Copy(tmp, in); err != nil {
		return err
	}
	return tmp.Sync()
}

func finishAtomicCopy(tmp *os.File, tmpPath, target string, perm os.FileMode) error {
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, target)
}

func (s *service) transitionMergeFailed(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	result *MergeRunResult,
	updatedBy, message string,
) (*Run, error) {
	failedRun, err := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     run.RunKey,
		FromStatus: statusMerging,
		Status:     statusFailed,
		UpdatedBy:  updatedBy,
		Metadata:   mergeMetadata(result, req, message),
	})
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
