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

	storeworkspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/workspace"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

// copyOptions 控制工作区文件复制方式。
type copyOptions struct {
	Atomic bool
	Mode   os.FileMode
}

// runFilePaths 保存同一相对文件在源目录和工作区中的绝对路径。
type runFilePaths struct {
	SourcePath    string
	WorkspacePath string
}

// inspectedRunFile 是一次文件检查的快照。
// hash 为空表示对应文件不存在，不把缺失和空文件混淆。
type inspectedRunFile struct {
	RelativePath    string
	Paths           runFilePaths
	SourceSHA256    string
	WorkspaceSHA256 string
	SourceExists    bool
	WorkspaceExists bool
}

// mergeEvaluationKind 区分普通文件更新和 workspace 删除文件两类判定。
type mergeEvaluationKind string

// merge 文件判定类型。
const (
	mergeEvaluationTracked mergeEvaluationKind = "tracked"
	mergeEvaluationRemoved mergeEvaluationKind = "removed"
)

// mergeFileSnapshot 固化 merge 判定所需的哈希和 dry-run 状态。
type mergeFileSnapshot struct {
	File               storeworkspace.WorkspaceRunFile
	RelativePath       string
	SourceSHA256Before string
	WorkspaceSHA256    string
	DryRun             bool
}

// copyRunFile 将源文件复制到对应 workspace 路径。
func copyRunFile(run storeworkspace.WorkspaceRun, rel string) error {
	paths := runPaths(&run, rel)
	if err := copyPreserveMode(paths.SourcePath, paths.WorkspacePath, copyOptions{}); err != nil {
		return fmt.Errorf("copy workspace file %q: %w", rel, err)
	}
	return nil
}

// copyFileAtomic 原子复制文件到目标路径并设置权限。
func copyFileAtomic(source, target string, perm os.FileMode) error {
	return copyPreserveMode(source, target, copyOptions{
		Atomic: true,
		Mode:   perm,
	})
}

// copyPreserveMode 按源文件权限复制内容。
// Atomic=true 时用于写回 source root，先写临时文件再 rename。
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

// copyPreserveModeAtomic 通过临时文件和 rename 完成原子替换。
// 写回源文件前拒绝目标 symlink，避免 workspace merge 跟随链接写出根目录。
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

// copyPreserveModeDirect 直接覆盖目标文件。
// 该路径只用于初始化 workspace 文件，不承担 source root 写回安全边界。
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

// ensureCopyTarget 创建目标目录，并按需拒绝 symlink 目标。
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

// runPaths 根据 run 和相对路径生成源/工作区绝对路径。
func runPaths(run *Run, rel string) runFilePaths {
	return runFilePaths{
		SourcePath:    filepath.Join(run.SourceRoot, rel),
		WorkspacePath: filepath.Join(run.WorkspacePath, rel),
	}
}

// inspectRunFile 校验相对路径并计算源/工作区哈希。
// 读取失败会携带相对路径上下文，便于 merge 报告定位。
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

// evaluateTrackedMergeFile 判定单个跟踪文件的 merge 动作。
// DeleteRemoved 命中时走删除判定，否则用源/工作区哈希做冲突检测。
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

// evaluateMergeFileState 按判定类型分发到更新或删除逻辑。
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

// evaluateRemovedMergeFileState 判定 workspace 已删除文件能否同步删除 source。
// source 相对 baseline 有漂移时标记 conflict，不执行删除。
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

// evaluateTrackedMergeFileState 判定普通文件的 merge 状态。
// workspace 未改则 unchanged，source 漂移则 conflict，其余可写回 source。
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

// prepareRunFiles 为创建 run 的文件列表生成持久化 file 记录。
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

// upsertRunFiles 批量写入 run file 记录。
func upsertRunFiles(ctx context.Context, runStore storeworkspace.Store, files []storeworkspace.WorkspaceRunFile) error {
	for _, file := range files {
		if _, err := runStore.UpsertFile(ctx, file); err != nil {
			return fmt.Errorf("upsert run file %q: %w", file.RelativePath, err)
		}
	}
	return nil
}

// buildRunFile 根据源文件快照构造 run file 记录。
// 源文件不存在直接报错，避免创建出无法 merge 的跟踪项。
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

// runStatusErrorMapper 将 store 错误转换为公开或内部错误。
type runStatusErrorMapper func(runKey string, err error) error

// passthroughRunStatusError 原样返回状态更新错误。
func passthroughRunStatusError(_ string, err error) error {
	return err
}

// publicRunStatusError 将 not found 转成包含 runKey 的公开错误。
func publicRunStatusError(runKey string, err error) error {
	if platformdb.IsNotFound(err) {
		return fmt.Errorf("run %q not found", runKey)
	}
	return err
}

// updateRunStatusAndEmit 更新 run 状态并在状态变化时发事件。
// 先读取旧状态再写入，确保事件里能带上准确的 oldStatus。
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

// transitionMergeRun 执行带 fromStatus 栅栏的 merge 状态转换。
// metadata 记录本次 merge 摘要，供事件和后续诊断复盘。
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

// typedRPCAdapter 为 jrpc2 handler 包一层参数校验。
// 校验失败时不进入服务层，保持 RPC 边界 fail-fast。
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

// validateCreateRunParams 校验创建 run 的必填参数。
func validateCreateRunParams(p createRunParams) error {
	return required(p.SourceRoot, "source_root")
}

// validateRunKeyParams 校验只需要 run_key 的请求。
func validateRunKeyParams(p runKeyParams) error {
	return required(p.RunKey, "run_key")
}

// validateMergeRunParams 校验 merge 请求。
func validateMergeRunParams(p mergeRunParams) error {
	return required(p.RunKey, "run_key")
}

// validateAbortRunParams 校验 abort 请求。
func validateAbortRunParams(p abortRunParams) error {
	return required(p.RunKey, "run_key")
}

// validateListRunFilesParams 校验列出 run 文件的请求。
func validateListRunFilesParams(p listRunFilesParams) error {
	return required(p.RunKey, "run_key")
}

// validateRunFileParams 校验读取单个 run 文件的请求。
func validateRunFileParams(p runFileParams) error {
	return required2(p.RunKey, "run_key", p.Path, "path")
}

// decodeLegacyRunParams 先解当前字段，再读取旧 camelCase 字段补齐。
// 这样新字段总是优先，兼容逻辑不会覆盖显式的新协议值。
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
