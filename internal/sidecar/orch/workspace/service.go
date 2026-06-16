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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/workspace"

	"github.com/kelindar/event"
)

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

var runKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type service struct {
	store            storeworkspace.Store
	emitCreated      func(WorkspaceRunCreated)
	emitMerged       func(WorkspaceRunMerged)
	emitAborted      func(WorkspaceRunAborted)
	emitMergeError   func(WorkspaceRunMergeError)
	emitStatusChange func(WorkspaceRunStatusChanged)
}

// NewService creates a workspace service that uses the given store and event
// dispatcher. Events are published directly via kelindar/event generics.
// NewService 创建服务。
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

// CreateRun 创建运行记录。
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

// buildRun 构建运行记录。
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
	sourceRoot, err := resolveSourceRoot(req.SourceRoot, req.CWD)
	if err != nil {
		return storeworkspace.WorkspaceRun{}, err
	}
	workspacePath, err := resolveWorkspacePath(req, runKey, sourceRoot)
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

func validateRunKey(runKey string) error {
	if runKey == "" {
		return errors.New("workspace: invalid run key")
	}
	if len(runKey) > maxRunKeyLength || !runKeyPattern.MatchString(runKey) {
		return errors.New("workspace: invalid run key")
	}
	return nil
}

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

// resolveWorkspacePath 解析工作区路径。
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

// GetRun 读取运行记录。
func (s *service) GetRun(ctx context.Context, runKey string) (*Run, error) {
	return s.store.GetRun(ctx, strings.TrimSpace(runKey))
}

// ListRuns 列出运行记录。
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

// UpdateRunStatus 更新运行记录状态。
func (s *service) UpdateRunStatus(ctx context.Context, input storeworkspace.UpdateRunStatusInput) (*Run, error) {
	return s.updateRunStatusAndEmit(ctx, input, passthroughRunStatusError)
}

// MergeRun 合并运行记录。
func (s *service) MergeRun(ctx context.Context, req MergeRunRequest) (*MergeRunResult, error) {
	run, err := s.requireRun(ctx, req.RunKey, statusActive)
	if err != nil {
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

// AbortRun 处理abort运行记录。
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

// ListRunFiles 列出运行记录文件。
func (s *service) ListRunFiles(ctx context.Context, runKey, state string) ([]RunFile, error) {
	return s.store.ListFiles(ctx, storeworkspace.ListFilesFilter{
		RunKey: strings.TrimSpace(runKey),
		State:  strings.TrimSpace(state),
		Limit:  defaultListLimit,
	})
}

// GetRunFile 读取运行记录文件。
func (s *service) GetRunFile(ctx context.Context, runKey, path string) (*RunFile, error) {
	return s.store.GetFile(ctx, strings.TrimSpace(runKey), strings.TrimSpace(path))
}

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

// transitionRunStatus 处理transition运行记录状态。
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
