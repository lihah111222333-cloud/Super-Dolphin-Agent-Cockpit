package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	storeworkspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/workspace"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"

	"github.com/kelindar/event"
)

// workspace run 状态、文件状态和列表限制。
const (
	defaultListLimit   = 200
	mergeListLimit     = 5000
	maxRunKeyLength    = 128
	fileStateSynced    = "synced"
	fileStateTracked   = "tracked"
	fileStateMerged    = "merged"
	fileStateRemoved   = "removed"
	fileStateConflict  = "conflict"
	fileStateError     = "error"
	fileStateUnchanged = "unchanged"
	statusAborted      = "aborted"
	statusActive       = "active"
	statusFailed       = "failed"
	statusMerging      = "merging"
	statusMerged       = "merged"
)

// runKeyPattern 限制 run key 只能包含跨平台安全字符。
var runKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// service 实现 workspace.Service。
// 事件 emitter 在状态变化后同步发送，store 负责持久化一致性。
type service struct {
	store            storeworkspace.Store
	emitCreated      func(WorkspaceRunCreated)
	emitMerged       func(WorkspaceRunMerged)
	emitAborted      func(WorkspaceRunAborted)
	emitMergeError   func(WorkspaceRunMergeError)
	emitStatusChange func(WorkspaceRunStatusChanged)
}

// NewService 创建 workspace 服务并接线事件 emitter。
// dispatcher 可以为 nil，但事件发送函数仍由 contract.NewEmitter 统一封装。
func NewService(store storeworkspace.Store, dispatcher *event.Dispatcher) Service {
	return &service{
		store:            store,
		emitCreated:      contract.NewEmitter[WorkspaceRunCreated](dispatcher),
		emitMerged:       contract.NewEmitter[WorkspaceRunMerged](dispatcher),
		emitAborted:      contract.NewEmitter[WorkspaceRunAborted](dispatcher),
		emitMergeError:   contract.NewEmitter[WorkspaceRunMergeError](dispatcher),
		emitStatusChange: contract.NewEmitter[WorkspaceRunStatusChanged](dispatcher),
	}
}

// CreateRun 创建 workspace run 并复制初始文件。
// 文件 bootstrap 或持久化任一步失败都会返回错误，不留下“成功但缺文件”的 run。
func (s *service) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error) {
	run, err := buildRun(req)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(run.WorkspacePath, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace dir: %w", err)
	}
	files, err := bootstrapFiles(run, req.Files)
	if err != nil {
		return nil, err
	}
	saved, err := s.persistRun(ctx, run, files)
	if err != nil {
		return nil, err
	}
	s.emitRunCreated(saved)
	return saved, nil
}

// buildRun 规范化创建请求并生成 store 层 run。
// runKey 为空时生成时间戳 key，source/workspace 路径必须解析为不同绝对路径。
func buildRun(req CreateRunRequest) (storeworkspace.WorkspaceRun, error) {
	if len(req.Metadata) == 0 {
		req.Metadata = json.RawMessage("{}")
	}
	runKey := strings.TrimSpace(req.RunKey)
	if runKey == "" {
		runKey = "run-" + strconv.FormatInt(time.Now().UnixMilli(), 10)
	}
	if err := validateRunKey(runKey); err != nil {
		return storeworkspace.WorkspaceRun{}, err
	}
	sourceRoot, workspacePath, err := resolveRunRoots(req, runKey)
	if err != nil {
		return storeworkspace.WorkspaceRun{}, err
	}
	createdBy := strings.TrimSpace(req.CreatedBy)
	updatedBy := strings.TrimSpace(req.UpdatedBy)
	if updatedBy == "" {
		updatedBy = createdBy
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = statusActive
	}
	return storeworkspace.WorkspaceRun{
		RunKey:        runKey,
		DagKey:        strings.TrimSpace(req.DagKey),
		SourceRoot:    sourceRoot,
		WorkspacePath: workspacePath,
		Status:        status,
		CreatedBy:     createdBy,
		UpdatedBy:     updatedBy,
		Metadata:      append([]byte(nil), req.Metadata...),
		FinishedAt:    req.FinishedAt,
	}, nil
}

// resolveRunRoots 解析 create 请求里的 source/workspace 路径，并按可信 roots 做包含校验。
func resolveRunRoots(req CreateRunRequest, runKey string) (string, string, error) {
	allowedRoots, err := requestAllowedWorkspaceRoots(req.CWD, req.AllowedSourceRoots)
	if err != nil {
		return "", "", err
	}
	sourceRoot, err := resolveSourceRoot(req.SourceRoot, req.CWD)
	if err != nil {
		return "", "", err
	}
	if err := ensurePathWithinAllowedWorkspaceRoots("sourceRoot", sourceRoot, allowedRoots); err != nil {
		return "", "", err
	}
	workspacePath, err := resolveWorkspacePath(req, runKey, sourceRoot)
	if err != nil {
		return "", "", err
	}
	if err := ensurePathWithinAllowedWorkspaceRoots("workspacePath", workspacePath, allowedRoots); err != nil {
		return "", "", err
	}
	return sourceRoot, workspacePath, nil
}

// validateRunKey 校验 run key 长度和字符集。
func validateRunKey(runKey string) error {
	if runKey == "" {
		return errors.New("workspace: invalid run key")
	}
	if len(runKey) > maxRunKeyLength || !runKeyPattern.MatchString(runKey) {
		return errors.New("workspace: invalid run key")
	}
	return nil
}

// resolveSourceRoot 解析并验证 source root 必须是目录。
func resolveSourceRoot(raw, cwd string) (string, error) {
	sourceRoot, err := resolveAbsolutePath(raw, cwd)
	if err != nil {
		return "", fmt.Errorf("resolve sourceRoot: %w", err)
	}
	if sourceRoot == "" {
		return "", errors.New("sourceRoot is required")
	}
	info, err := os.Stat(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("stat sourceRoot: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("sourceRoot must be directory")
	}
	return sourceRoot, nil
}

// resolveWorkspacePath 解析 workspace 路径。
// 未显式传入时放在 cwd 或 sourceRoot 的 .workspace/runKey 下，且不能等于 sourceRoot。
func resolveWorkspacePath(req CreateRunRequest, runKey, sourceRoot string) (string, error) {
	workspacePath, err := resolveAbsolutePath(req.WorkspacePath, req.CWD)
	if err != nil {
		return "", fmt.Errorf("resolve workspacePath: %w", err)
	}
	if workspacePath == "" {
		base := strings.TrimSpace(req.CWD)
		if base == "" {
			base = sourceRoot
		}
		workspacePath = filepath.Join(base, ".workspace", runKey)
	}
	workspacePath, err = filepath.Abs(workspacePath)
	if err != nil {
		return "", fmt.Errorf("resolve workspacePath: %w", err)
	}
	if workspacePath == sourceRoot {
		return "", errors.New("workspacePath must be distinct from sourceRoot")
	}
	return workspacePath, nil
}

// resolveAbsolutePath 将可选路径解析为绝对路径。
func resolveAbsolutePath(raw, base string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) && strings.TrimSpace(base) != "" {
		path = filepath.Join(strings.TrimSpace(base), path)
	}
	return filepath.Abs(path)
}

// bootstrapFiles 去重并复制初始文件到 workspace。
func bootstrapFiles(run storeworkspace.WorkspaceRun, files []string) ([]string, error) {
	relativeFiles, err := dedupeRelativePaths(files)
	if err != nil {
		return nil, err
	}
	for _, rel := range relativeFiles {
		if err := copyRunFile(run, rel); err != nil {
			return nil, err
		}
	}
	return relativeFiles, nil
}

// GetRun 读取 workspace run。
func (s *service) GetRun(ctx context.Context, runKey string) (*Run, error) {
	return s.store.GetRun(ctx, strings.TrimSpace(runKey))
}

// ListRuns 按状态和 DAG 过滤 workspace run。
func (s *service) ListRuns(ctx context.Context, status, dagKey string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	return s.store.ListRuns(ctx, storeworkspace.ListRunsFilter{
		Status: strings.TrimSpace(status),
		DagKey: strings.TrimSpace(dagKey),
		Limit:  int32(limit),
	})
}

// UpdateRunStatus 更新 run 状态并发出状态变化事件。
func (s *service) UpdateRunStatus(ctx context.Context, input storeworkspace.UpdateRunStatusInput) (*Run, error) {
	return s.updateRunStatusAndEmit(ctx, input, passthroughRunStatusError)
}

// MergeRun 将 active workspace run 合并回 source root。
// dry-run 会临时进入 merging 后恢复 active，真实合并才会写 source 文件。
func (s *service) MergeRun(ctx context.Context, req MergeRunRequest) (*MergeRunResult, error) {
	run, err := s.requireRun(ctx, req.RunKey, statusActive)
	if err != nil {
		return nil, err
	}
	allowedRoots, err := requestAllowedWorkspaceRoots("", req.AllowedSourceRoots)
	if err != nil {
		return nil, err
	}
	if err := ensurePathWithinAllowedWorkspaceRoots("sourceRoot", run.SourceRoot, allowedRoots); err != nil {
		return nil, err
	}
	if err := ensurePathWithinAllowedWorkspaceRoots("workspacePath", run.WorkspacePath, allowedRoots); err != nil {
		return nil, err
	}
	updatedBy := strings.TrimSpace(req.UpdatedBy)
	if req.DryRun {
		return s.dryRunMerge(ctx, run, req, updatedBy)
	}
	mergingRun, err := s.transitionMergeRun(ctx, run, statusActive, statusMerging, req, updatedBy, nil, "")
	if err != nil {
		return nil, err
	}
	return s.executeMerge(ctx, mergingRun, req, updatedBy)
}

// AbortRun 将 run 标记为 aborted 并发布 abort 事件。
func (s *service) AbortRun(ctx context.Context, runKey, updatedBy, reason string) error {
	run, err := s.updateRunStatusAndEmit(ctx, storeworkspace.UpdateRunStatusInput{
		RunKey:    runKey,
		Status:    statusAborted,
		UpdatedBy: updatedBy,
		Metadata:  marshalMetadata(map[string]any{"reason": strings.TrimSpace(reason)}),
	}, publicRunStatusError)
	if err != nil {
		return err
	}
	s.emitRunAbortedEvent(run, reason)
	return nil
}

// ListRunFiles 列出某个 run 的文件状态。
func (s *service) ListRunFiles(ctx context.Context, runKey, state string) ([]RunFile, error) {
	return s.store.ListFiles(ctx, storeworkspace.ListFilesFilter{
		RunKey: strings.TrimSpace(runKey),
		State:  strings.TrimSpace(state),
		Limit:  defaultListLimit,
	})
}

// GetRunFile 读取某个 run 的单个文件状态。
func (s *service) GetRunFile(ctx context.Context, runKey, path string) (*RunFile, error) {
	return s.store.GetFile(ctx, strings.TrimSpace(runKey), strings.TrimSpace(path))
}

// requireRun 读取 run 并校验当前状态。
// merge/abort 这类写操作依赖它阻止错误状态下继续写文件或改状态。
func (s *service) requireRun(ctx context.Context, runKey, expectedStatus string) (*Run, error) {
	key := strings.TrimSpace(runKey)
	if key == "" {
		return nil, errors.New("runKey is required")
	}
	run, err := s.store.GetRun(ctx, key)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, fmt.Errorf("run %q not found", key)
		}
		return nil, err
	}
	if run.Status != expectedStatus {
		return nil, fmt.Errorf("run %q status is %s, expected %s", key, run.Status, expectedStatus)
	}
	return run, nil
}

// transitionRunStatus 执行带 fromStatus 的状态转换。
// store 未命中时回读当前状态，返回更明确的状态冲突错误。
func (s *service) transitionRunStatus(ctx context.Context, input storeworkspace.TransitionRunStatusInput) (*Run, error) {
	input.RunKey = strings.TrimSpace(input.RunKey)
	input.FromStatus = strings.TrimSpace(input.FromStatus)
	input.Status = strings.TrimSpace(input.Status)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.RunKey == "" {
		return nil, errors.New("runKey is required")
	}
	run, err := s.store.TransitionRunStatus(ctx, input)
	if err == nil {
		return run, nil
	}
	if !platformdb.IsNotFound(err) {
		return nil, err
	}
	current, getErr := s.store.GetRun(ctx, input.RunKey)
	if getErr != nil {
		if platformdb.IsNotFound(getErr) {
			return nil, fmt.Errorf("run %q not found", input.RunKey)
		}
		return nil, getErr
	}
	return nil, fmt.Errorf("run %q status is %s, expected %s", input.RunKey, current.Status, input.FromStatus)
}

// marshalMetadata 将状态变化 metadata 编码为 JSON。
// 该函数只处理内部 map，编码失败返回 nil 以避免 panic 打断状态转换。
func marshalMetadata(metadata map[string]any) json.RawMessage {
	if len(metadata) == 0 {
		return nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	return data
}

// mergeMetadata 记录 merge 请求和结果摘要。
func mergeMetadata(result *MergeRunResult, req MergeRunRequest, message string) json.RawMessage {
	metadata := map[string]any{
		"dryRun":        req.DryRun,
		"deleteRemoved": req.DeleteRemoved,
	}
	if result != nil {
		metadata["merged"] = result.Merged
		metadata["removed"] = result.Removed
		metadata["conflicts"] = result.Conflicts
		metadata["unchanged"] = result.Unchanged
		metadata["errors"] = result.Errors
	}
	if text := strings.TrimSpace(message); text != "" {
		metadata["message"] = text
	}
	return marshalMetadata(metadata)
}
