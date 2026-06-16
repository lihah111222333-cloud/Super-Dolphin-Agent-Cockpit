package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/workspace"
)

type copyOptions struct {
	Atomic bool
	Mode   os.FileMode
}

type runFilePaths struct {
	SourcePath    string
	WorkspacePath string
}

type inspectedRunFile struct {
	RelativePath    string
	Paths           runFilePaths
	SourceSHA256    string
	WorkspaceSHA256 string
	SourceExists    bool
	WorkspaceExists bool
}

type mergeEvaluationKind string

const (
	mergeEvaluationTracked mergeEvaluationKind = "tracked"
	mergeEvaluationRemoved mergeEvaluationKind = "removed"
)

type mergeFileSnapshot struct {
	File               storeworkspace.WorkspaceRunFile
	RelativePath       string
	SourceSHA256Before string
	WorkspaceSHA256    string
	DryRun             bool
}

func copyRunFile(run storeworkspace.WorkspaceRun, rel string) error {
	paths := runPaths(&run, rel)
	if err := copyPreserveMode(paths.SourcePath, paths.WorkspacePath, copyOptions{}); err != nil {
		return fmt.Errorf("copy workspace file %q: %w", rel, err)
	}
	return nil
}

func copyFileAtomic(source, target string, perm os.FileMode) error {
	return copyPreserveMode(source, target, copyOptions{
		Atomic: true,
		Mode:   perm,
	})
}

func copyPreserveMode(sourcePath, targetPath string, opts copyOptions) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = sourceFile.Close() }()

	info, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	perm := info.Mode().Perm()
	if opts.Mode != 0 {
		perm = opts.Mode
	}

	if opts.Atomic {
		return copyPreserveModeAtomic(sourceFile, targetPath, perm)
	}
	return copyPreserveModeDirect(sourceFile, targetPath, perm)
}

// copyPreserveModeAtomic 复制preserve模式atomic。
func copyPreserveModeAtomic(source *os.File, targetPath string, perm os.FileMode) error {
	if err := ensureCopyTarget(targetPath, true); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(targetPath), ".workspace-merge-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	defer cleanup()
	if _, err := io.Copy(tmp, source); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return err
	}
	return nil
}

func copyPreserveModeDirect(source *os.File, targetPath string, perm os.FileMode) error {
	if err := ensureCopyTarget(targetPath, false); err != nil {
		return err
	}
	targetFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(targetFile, source); err != nil {
		_ = targetFile.Close()
		return err
	}
	return targetFile.Close()
}

func ensureCopyTarget(target string, rejectSymlink bool) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if !rejectSymlink {
		return nil
	}
	if stat, err := os.Lstat(target); err == nil && stat.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("target is symlink: %s", target)
	}
	return nil
}

func runPaths(run *Run, rel string) runFilePaths {
	return runFilePaths{
		SourcePath:    filepath.Join(run.SourceRoot, rel),
		WorkspacePath: filepath.Join(run.WorkspacePath, rel),
	}
}

func inspectRunFile(run *Run, rel string) (inspectedRunFile, error) {
	normalized, err := validateRelativePath(rel)
	if err != nil {
		return inspectedRunFile{}, err
	}
	paths := runPaths(run, normalized)
	sourceHash, err := hashFileIfExists(paths.SourcePath)
	if err != nil {
		return inspectedRunFile{}, fmt.Errorf("hash source file %q: %w", normalized, err)
	}
	workspaceHash, err := hashFileIfExists(paths.WorkspacePath)
	if err != nil {
		return inspectedRunFile{}, fmt.Errorf("hash workspace file %q: %w", normalized, err)
	}
	return inspectedRunFile{
		RelativePath:    normalized,
		Paths:           paths,
		SourceSHA256:    sourceHash,
		WorkspaceSHA256: workspaceHash,
		SourceExists:    sourceHash != "",
		WorkspaceExists: workspaceHash != "",
	}, nil
}

func evaluateTrackedMergeFile(
	run *Run,
	file RunFile,
	removed map[string]removedWorkspaceFile,
	dryRun bool,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	if candidate, ok := removed[file.RelativePath]; ok {
		return evaluateMergeFileState(mergeFileSnapshot{
			File:               storeworkspace.WorkspaceRunFile(file),
			RelativePath:       candidate.RelativePath,
			SourceSHA256Before: candidate.SourceSHA256Before,
			DryRun:             dryRun,
		}, mergeEvaluationRemoved)
	}
	inspected, err := inspectRunFile(run, file.RelativePath)
	if err != nil {
		return mergeFileError(storeworkspace.WorkspaceRunFile(file), err.Error())
	}
	return evaluateMergeFileState(mergeFileSnapshot{
		File:               storeworkspace.WorkspaceRunFile(file),
		RelativePath:       inspected.RelativePath,
		SourceSHA256Before: inspected.SourceSHA256,
		WorkspaceSHA256:    inspected.WorkspaceSHA256,
	}, mergeEvaluationTracked)
}

func evaluateMergeFileState(
	snapshot mergeFileSnapshot,
	kind mergeEvaluationKind,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	file := snapshot.File
	file.LastError = ""
	file.SourceSHA256Before = snapshot.SourceSHA256Before

	switch kind {
	case mergeEvaluationRemoved:
		return evaluateRemovedMergeFileState(file, snapshot)
	default:
		return evaluateTrackedMergeFileState(file, snapshot)
	}
}

func evaluateRemovedMergeFileState(
	file storeworkspace.WorkspaceRunFile,
	snapshot mergeFileSnapshot,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	file.WorkspaceSHA256 = ""
	file.SourceSHA256After = ""
	if file.BaselineSHA256 != "" && snapshot.SourceSHA256Before != "" && snapshot.SourceSHA256Before != file.BaselineSHA256 {
		reason := "delete conflict: source changed since baseline"
		file.State = fileStateConflict
		file.LastError = reason
		return file, MergeFileResult{Path: snapshot.RelativePath, Action: "conflict", Reason: reason}
	}
	file.State = fileStateRemoved
	action := "removed"
	if snapshot.DryRun {
		action = "would_remove"
	}
	return file, MergeFileResult{Path: snapshot.RelativePath, Action: action}
}

// evaluateTrackedMergeFileState 处理evaluatetrackedmerge文件状态。
func evaluateTrackedMergeFileState(
	file storeworkspace.WorkspaceRunFile,
	snapshot mergeFileSnapshot,
) (storeworkspace.WorkspaceRunFile, MergeFileResult) {
	file.WorkspaceSHA256 = snapshot.WorkspaceSHA256
	if snapshot.WorkspaceSHA256 == "" {
		return mergeFileError(file, "workspace file is missing")
	}
	if file.BaselineSHA256 != "" && snapshot.WorkspaceSHA256 == file.BaselineSHA256 {
		file.State = fileStateUnchanged
		file.SourceSHA256After = snapshot.SourceSHA256Before
		return file, MergeFileResult{Path: snapshot.RelativePath, Action: "unchanged"}
	}
	if file.BaselineSHA256 != "" && snapshot.SourceSHA256Before != "" && snapshot.SourceSHA256Before != file.BaselineSHA256 {
		reason := "source changed since baseline"
		file.State = fileStateConflict
		file.SourceSHA256After = ""
		file.LastError = reason
		return file, MergeFileResult{Path: snapshot.RelativePath, Action: "conflict", Reason: reason}
	}
	file.State = fileStateMerged
	file.SourceSHA256After = snapshot.WorkspaceSHA256
	return file, MergeFileResult{Path: snapshot.RelativePath, Action: "merged"}
}

func prepareRunFiles(run *Run, files []string) ([]storeworkspace.WorkspaceRunFile, error) {
	prepared := make([]storeworkspace.WorkspaceRunFile, 0, len(files))
	for _, rel := range files {
		file, err := buildRunFile(run, rel)
		if err != nil {
			return nil, fmt.Errorf("prepare run file %q: %w", rel, err)
		}
		prepared = append(prepared, file)
	}
	return prepared, nil
}

func upsertRunFiles(ctx context.Context, runStore storeworkspace.Store, files []storeworkspace.WorkspaceRunFile) error {
	for _, file := range files {
		if _, err := runStore.UpsertFile(ctx, file); err != nil {
			return fmt.Errorf("upsert run file %q: %w", file.RelativePath, err)
		}
	}
	return nil
}

func buildRunFile(run *Run, rel string) (storeworkspace.WorkspaceRunFile, error) {
	inspected, err := inspectRunFile(run, rel)
	if err != nil {
		return storeworkspace.WorkspaceRunFile{}, err
	}
	if !inspected.SourceExists {
		return storeworkspace.WorkspaceRunFile{}, fmt.Errorf("hash source file: %w", os.ErrNotExist)
	}
	state := fileStateTracked
	if inspected.WorkspaceExists && inspected.WorkspaceSHA256 == inspected.SourceSHA256 {
		state = fileStateSynced
	}
	return storeworkspace.WorkspaceRunFile{
		RunKey:             run.RunKey,
		RelativePath:       inspected.RelativePath,
		BaselineSHA256:     inspected.SourceSHA256,
		WorkspaceSHA256:    inspected.WorkspaceSHA256,
		SourceSHA256Before: inspected.SourceSHA256,
		SourceSHA256After:  inspected.SourceSHA256,
		State:              state,
	}, nil
}

type runStatusErrorMapper func(runKey string, err error) error

func passthroughRunStatusError(_ string, err error) error {
	return err
}

func publicRunStatusError(runKey string, err error) error {
	if platformdb.IsNotFound(err) {
		return fmt.Errorf("run %q not found", runKey)
	}
	return err
}

// updateRunStatusAndEmit 更新运行记录状态emit。
func (s *service) updateRunStatusAndEmit(
	ctx context.Context,
	input storeworkspace.UpdateRunStatusInput,
	mapError runStatusErrorMapper,
) (*Run, error) {
	input.RunKey = strings.TrimSpace(input.RunKey)
	input.Status = strings.TrimSpace(input.Status)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.RunKey == "" {
		return nil, jrpc2.Errorf(jrpc2.Code(contract.CodeInvalidParams), "runKey is required")
	}
	if mapError == nil {
		mapError = passthroughRunStatusError
	}

	before, err := s.store.GetRun(ctx, input.RunKey)
	if err != nil {
		return nil, mapError(input.RunKey, err)
	}
	updated, err := s.store.UpdateRunStatus(ctx, input)
	if err != nil {
		return nil, mapError(input.RunKey, err)
	}
	if before != nil && before.Status != updated.Status {
		s.emitRunStatusChanged(before.Status, updated)
	}
	return updated, nil
}

func (s *service) transitionMergeRun(
	ctx context.Context,
	run *Run,
	fromStatus, toStatus string,
	req MergeRunRequest,
	updatedBy string,
	result *MergeRunResult,
	message string,
) (*Run, error) {
	transitioned, err := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     run.RunKey,
		FromStatus: fromStatus,
		Status:     toStatus,
		UpdatedBy:  updatedBy,
		Metadata:   mergeMetadata(result, req, message),
	})
	if err != nil {
		return nil, err
	}
	s.emitRunStatusChanged(run.Status, transitioned)
	return transitioned, nil
}

func typedRPCAdapter[P any, R any](
	call func(context.Context, P) (R, error),
	validators ...func(P) error,
) func(context.Context, P) (R, error) {
	return func(ctx context.Context, params P) (R, error) {
		var zero R
		for _, validate := range validators {
			if err := validate(params); err != nil {
				return zero, err
			}
		}
		return call(ctx, params)
	}
}

func validateCreateRunParams(p createRunParams) error {
	return required(p.SourceRoot, "source_root")
}

func validateRunKeyParams(p runKeyParams) error {
	return required(p.RunKey, "run_key")
}

func validateMergeRunParams(p mergeRunParams) error {
	return required(p.RunKey, "run_key")
}

func validateAbortRunParams(p abortRunParams) error {
	return required(p.RunKey, "run_key")
}

func validateListRunFilesParams(p listRunFilesParams) error {
	return required(p.RunKey, "run_key")
}

func validateRunFileParams(p runFileParams) error {
	return required2(p.RunKey, "run_key", p.Path, "path")
}

func decodeLegacyRunParams[L any](
	data []byte,
	decodeCurrent func() error,
	legacy *L,
	merge func(L),
) error {
	if err := decodeCurrent(); err != nil {
		return err
	}
	if err := json.Unmarshal(data, legacy); err != nil {
		return err
	}
	merge(*legacy)
	return nil
}
