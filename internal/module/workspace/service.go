package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
)

const (
	defaultListLimit   = 200
	mergeListLimit     = 5000
	fileStateSynced    = "synced"
	fileStateTracked   = "tracked"
	fileStateMerged    = "merged"
	fileStateConflict  = "conflict"
	fileStateError     = "error"
	fileStateUnchanged = "unchanged"
	statusAborted      = "aborted"
	statusActive       = "active"
	statusFailed       = "failed"
	statusMerging      = "merging"
	statusMerged       = "merged"
)

type service struct {
	store            storeworkspace.Store
	emitCreated      func(workspacedto.WorkspaceRunCreated)
	emitMerged       func(workspacedto.WorkspaceRunMerged)
	emitAborted      func(workspacedto.WorkspaceRunAborted)
	emitMergeError   func(workspacedto.WorkspaceRunMergeError)
	emitStatusChange func(workspacedto.WorkspaceRunStatusChanged)
}

func NewService(store storeworkspace.Store, emitters *bus.WorkspaceEmitters) Service {
	dispatcher := emitters.Dispatcher()
	return &service{
		store:            store,
		emitCreated:      bus.NewEmitter[workspacedto.WorkspaceRunCreated](dispatcher),
		emitMerged:       bus.NewEmitter[workspacedto.WorkspaceRunMerged](dispatcher),
		emitAborted:      bus.NewEmitter[workspacedto.WorkspaceRunAborted](dispatcher),
		emitMergeError:   bus.NewEmitter[workspacedto.WorkspaceRunMergeError](dispatcher),
		emitStatusChange: bus.NewEmitter[workspacedto.WorkspaceRunStatusChanged](dispatcher),
	}
}

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

func buildRun(req CreateRunRequest) (storeworkspace.WorkspaceRun, error) {
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
	if strings.Contains(runKey, "..") || strings.ContainsAny(runKey, `/\`) {
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

func (s *service) GetRun(ctx context.Context, runKey string) (*Run, error) {
	return s.store.GetRun(ctx, strings.TrimSpace(runKey))
}

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

func (s *service) UpdateRunStatus(ctx context.Context, runKey, status string) (*Run, error) {
	current, err := s.store.GetRun(ctx, strings.TrimSpace(runKey))
	if err != nil {
		return nil, err
	}
	run, err := s.store.UpdateRunStatus(ctx, storeworkspace.UpdateRunStatusInput{
		RunKey: strings.TrimSpace(runKey),
		Status: strings.TrimSpace(status),
	})
	if err != nil {
		return nil, err
	}
	s.emitRunStatusChanged(current.Status, run)
	return run, nil
}

func (s *service) MergeRun(ctx context.Context, req MergeRunRequest) (*MergeRunResult, error) {
	run, err := s.requireRun(ctx, req.RunKey, statusActive)
	if err != nil {
		return nil, err
	}
	if req.DryRun {
		return s.dryRunMerge(ctx, run, req)
	}
	updatedBy := strings.TrimSpace(req.UpdatedBy)
	mergingRun, err := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     run.RunKey,
		FromStatus: statusActive,
		Status:     statusMerging,
		UpdatedBy:  updatedBy,
		Metadata:   mergeMetadata(nil, req, ""),
	})
	if err != nil {
		return nil, err
	}
	s.emitRunStatusChanged(run.Status, mergingRun)
	return s.executeMerge(ctx, mergingRun, req, updatedBy)
}

func (s *service) AbortRun(ctx context.Context, runKey, updatedBy, reason string) error {
	run, err := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     strings.TrimSpace(runKey),
		FromStatus: statusActive,
		Status:     statusAborted,
		UpdatedBy:  strings.TrimSpace(updatedBy),
		Metadata:   marshalMetadata(map[string]any{"reason": reason}),
	})
	if err != nil {
		return err
	}
	s.emitRunStatusChanged(statusActive, run)
	s.emitRunAbortedEvent(run, reason)
	return nil
}

func (s *service) ListRunFiles(ctx context.Context, runKey, state string) ([]RunFile, error) {
	return s.store.ListFiles(ctx, storeworkspace.ListFilesFilter{
		RunKey: strings.TrimSpace(runKey),
		State:  strings.TrimSpace(state),
		Limit:  defaultListLimit,
	})
}

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

func (s *service) dryRunMerge(ctx context.Context, run *Run, req MergeRunRequest) (*MergeRunResult, error) {
	files, err := s.store.ListFiles(ctx, storeworkspace.ListFilesFilter{
		RunKey: run.RunKey,
		Limit:  mergeListLimit,
	})
	if err != nil {
		return nil, err
	}
	result, _ := s.planMerge(run, files)
	result.DryRun = req.DryRun
	if req.DeleteRemoved {
		// TODO(p2-r2): restore full V2 deleteRemoved semantics once merge walks the workspace tree again.
	}
	result.Status = run.Status
	return result, nil
}

func mergeMetadata(result *MergeRunResult, req MergeRunRequest, message string) json.RawMessage {
	metadata := map[string]any{
		"dryRun":        req.DryRun,
		"deleteRemoved": req.DeleteRemoved,
	}
	if result != nil {
		metadata["merged"] = result.Merged
		metadata["conflicts"] = result.Conflicts
		metadata["unchanged"] = result.Unchanged
		metadata["errors"] = result.Errors
	}
	if text := strings.TrimSpace(message); text != "" {
		metadata["message"] = text
	}
	return marshalMetadata(metadata)
}
