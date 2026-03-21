package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
)

func copyRunFile(run storeworkspace.WorkspaceRun, rel string) error {
	sourcePath := filepath.Join(run.SourceRoot, rel)
	workspacePath := filepath.Join(run.WorkspacePath, rel)
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return fmt.Errorf("create workspace file dir %q: %w", rel, err)
	}
	if err := copyFile(sourcePath, workspacePath); err != nil {
		return fmt.Errorf("copy workspace file %q: %w", rel, err)
	}
	return nil
}

func copyFile(sourcePath, targetPath string) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = sourceFile.Close() }()
	info, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	targetFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		_ = targetFile.Close()
		return err
	}
	return targetFile.Close()
}

func dedupeRelativePaths(files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, raw := range files {
		rel, err := normalizeRelativePath(raw)
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

func evaluateTrackedMergeFile(
	run *Run,
	file RunFile,
	removed map[string]removedWorkspaceFile,
	dryRun bool,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	if candidate, ok := removed[file.RelativePath]; ok {
		return evaluateRemovedMergeFile(storeworkspace.WorkspaceRunFile(file), candidate, dryRun)
	}
	return evaluateMergeFile(run, file)
}

func evaluateRemovedMergeFile(
	file storeworkspace.WorkspaceRunFile,
	candidate removedWorkspaceFile,
	dryRun bool,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	file.WorkspaceSHA256 = candidate.WorkspaceSHA256
	file.SourceSHA256Before = ""
	file.SourceSHA256After = ""
	file.State = fileStateRemoved
	file.LastError = ""
	action := "removed"
	if dryRun {
		action = "would_remove"
	}
	return file, MergeFileResult{Path: candidate.RelativePath, Action: action}
}

func evaluateMergeFile(run *Run, file RunFile) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	updated := storeworkspace.WorkspaceRunFile(file)
	updated.LastError = ""
	sourceBefore, err := hashFileIfExists(filepath.Join(run.SourceRoot, file.RelativePath))
	updated.SourceSHA256Before = sourceBefore
	if err != nil {
		return mergeFileError(updated, err.Error())
	}
	workspaceHash, err := hashFileIfExists(filepath.Join(run.WorkspacePath, file.RelativePath))
	updated.WorkspaceSHA256 = workspaceHash
	if err != nil {
		return mergeFileError(updated, err.Error())
	}
	if workspaceHash == "" {
		return mergeFileError(updated, "workspace file is missing")
	}
	if file.BaselineSHA256 != "" && workspaceHash == file.BaselineSHA256 {
		updated.State = fileStateUnchanged
		updated.SourceSHA256After = sourceBefore
		return updated, MergeFileResult{Path: file.RelativePath, Action: "unchanged"}
	}
	if file.BaselineSHA256 != "" && sourceBefore != "" && sourceBefore != file.BaselineSHA256 {
		reason := "source changed since baseline"
		updated.State = fileStateConflict
		updated.SourceSHA256After = ""
		updated.LastError = reason
		return updated, MergeFileResult{Path: file.RelativePath, Action: "conflict", Reason: reason}
	}
	// TODO: copy workspace content into sourceRoot once full V2 merge I/O is restored.
	updated.State = fileStateMerged
	updated.SourceSHA256After = workspaceHash
	return updated, MergeFileResult{Path: file.RelativePath, Action: "merged"}
}

func recordMergeItem(result *MergeRunResult, item MergeFileResult) {
	result.Files = append(result.Files, item)
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

func mergeFileError(file storeworkspace.WorkspaceRunFile, reason string) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	file.State = fileStateError
	file.SourceSHA256After = ""
	file.LastError = reason
	return file, MergeFileResult{Path: file.RelativePath, Action: "error", Reason: reason}
}

func (s *service) applyFileUpdates(ctx context.Context, files []storeworkspace.WorkspaceRunFile) error {
	for _, file := range files {
		if _, err := s.store.UpsertFile(ctx, file); err != nil {
			return fmt.Errorf("upsert run file %q: %w", file.RelativePath, err)
		}
	}
	return nil
}

func (s *service) rollbackMergeState(ctx context.Context, original []RunFile, cause error) error {
	if restoreErr := s.restoreRunFiles(ctx, original); restoreErr != nil {
		return errors.Join(cause, restoreErr)
	}
	return cause
}

func (s *service) restoreRunFiles(ctx context.Context, files []RunFile) error {
	for _, file := range files {
		if _, err := s.store.UpsertFile(ctx, storeworkspace.WorkspaceRunFile(file)); err != nil {
			return fmt.Errorf("restore run file %q: %w", file.RelativePath, err)
		}
	}
	return nil
}

func (s *service) persistRun(ctx context.Context, run storeworkspace.WorkspaceRun, files []string) (*Run, error) {
	var saved *Run
	err := s.store.WithTx(ctx, func(txStore storeworkspace.Store) error {
		created, err := txStore.UpsertRun(ctx, run)
		if err != nil {
			return err
		}
		if err := upsertRunFilesWithStore(ctx, txStore, created, files); err != nil {
			return err
		}
		saved = created
		return nil
	})
	return saved, err
}

func upsertRunFilesWithStore(ctx context.Context, txStore storeworkspace.Store, run *Run, files []string) error {
	for _, rel := range files {
		file, err := buildRunFile(run, rel)
		if err != nil {
			return fmt.Errorf("prepare run file %q: %w", rel, err)
		}
		if _, err := txStore.UpsertFile(ctx, file); err != nil {
			return fmt.Errorf("upsert run file %q: %w", rel, err)
		}
	}
	return nil
}

func buildRunFile(run *Run, rel string) (storeworkspace.WorkspaceRunFile, error) {
	sourcePath := filepath.Join(run.SourceRoot, rel)
	sourceHash, err := hashFile(sourcePath)
	if err != nil {
		return storeworkspace.WorkspaceRunFile{}, fmt.Errorf("hash source file: %w", err)
	}
	workspaceHash, err := hashFileIfExists(filepath.Join(run.WorkspacePath, rel))
	if err != nil {
		return storeworkspace.WorkspaceRunFile{}, fmt.Errorf("hash workspace file: %w", err)
	}
	state := fileStateTracked
	if workspaceHash != "" && workspaceHash == sourceHash {
		state = fileStateSynced
	}
	return storeworkspace.WorkspaceRunFile{
		RunKey:             run.RunKey,
		RelativePath:       rel,
		BaselineSHA256:     sourceHash,
		WorkspaceSHA256:    workspaceHash,
		SourceSHA256Before: sourceHash,
		SourceSHA256After:  sourceHash,
		State:              state,
	}, nil
}

func (s *service) emitRunCreated(run *Run) {
	s.emitCreated(workspacedto.WorkspaceRunCreated{
		WorkspaceRunHeader: workspaceRunHeader(run),
		SourceRoot:         run.SourceRoot,
		WorkspacePath:      run.WorkspacePath,
		Status:             run.Status,
		CreatedBy:          run.CreatedBy,
		UpdatedBy:          run.UpdatedBy,
	})
}

func (s *service) emitRunStatusChanged(oldStatus string, run *Run) {
	s.emitStatusChange(workspacedto.WorkspaceRunStatusChanged{
		WorkspaceRunHeader: workspaceRunHeader(run),
		OldStatus:          oldStatus,
		NewStatus:          run.Status,
		UpdatedBy:          run.UpdatedBy,
	})
}

func (s *service) emitRunMergedEvent(run *Run, result *MergeRunResult) {
	s.emitMerged(workspacedto.WorkspaceRunMerged{
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

func (s *service) emitRunMergeErrorEvent(run *Run, result *MergeRunResult, updatedBy, message string) {
	s.emitMergeError(workspacedto.WorkspaceRunMergeError{
		WorkspaceRunHeader: workspaceRunHeader(run),
		SourceRoot:         run.SourceRoot,
		WorkspacePath:      run.WorkspacePath,
		Conflicts:          result.Conflicts,
		Errors:             result.Errors,
		Message:            mergeIssueMessage(result, message),
		UpdatedBy:          updatedBy,
	})
}

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

func (s *service) emitRunAbortedEvent(run *Run, reason string) {
	s.emitAborted(workspacedto.WorkspaceRunAborted{
		WorkspaceRunHeader: workspaceRunHeader(run),
		SourceRoot:         run.SourceRoot,
		WorkspacePath:      run.WorkspacePath,
		Status:             run.Status,
		Reason:             strings.TrimSpace(reason),
		UpdatedBy:          run.UpdatedBy,
	})
}

func workspaceRunHeader(run *Run) sharedto.WorkspaceRunHeader {
	return sharedto.WorkspaceRunHeader{
		DAGHeader: sharedto.DAGHeader{
			EventHeader: sharedto.EventHeader{Timestamp: time.Now()},
			DagKey:      run.DagKey,
		},
		RunKey: run.RunKey,
	}
}

func normalizeRelativePath(raw string) (string, error) {
	path := filepath.Clean(strings.TrimSpace(raw))
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

func hashFileIfExists(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return hashFile(path)
}
