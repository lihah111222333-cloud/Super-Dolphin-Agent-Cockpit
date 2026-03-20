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
	"strconv"
	"strings"
	"time"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
	"github.com/jackc/pgx/v5"
)

const (
	defaultListLimit = 200
	fileStateSynced  = "synced"
	fileStateTracked = "tracked"
	statusAborted    = "aborted"
	statusActive     = "active"
	statusMerged     = "merged"
)

type service struct{ store storeworkspace.Store }

func NewService(store storeworkspace.Store) Service { return &service{store: store} }

func (s *service) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error) {
	run := buildRun(req)
	if run.SourceRoot == "" {
		return nil, errors.New("sourceRoot is required")
	}
	saved, err := s.store.UpsertRun(ctx, run)
	if err != nil {
		return nil, err
	}
	if err := s.upsertRunFiles(ctx, saved, req.Files); err != nil {
		return nil, err
	}
	return saved, nil
}

func buildRun(req CreateRunRequest) storeworkspace.WorkspaceRun {
	runKey := strings.TrimSpace(req.RunKey)
	if runKey == "" {
		runKey = "run-" + strconv.FormatInt(time.Now().UnixMilli(), 10)
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
	workspacePath := strings.TrimSpace(req.WorkspacePath)
	sourceRoot := strings.TrimSpace(req.SourceRoot)
	if workspacePath == "" {
		workspacePath = sourceRoot
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
	}
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
	return s.store.UpdateRunStatus(ctx, storeworkspace.UpdateRunStatusInput{
		RunKey: strings.TrimSpace(runKey),
		Status: strings.TrimSpace(status),
	})
}

// TODO: persist merge file state when full merge semantics is implemented.
func (s *service) MergeRun(ctx context.Context, runKey string) (*Run, error) {
	return s.transitionRunStatus(ctx, runKey, statusActive, statusMerged)
}

func (s *service) AbortRun(ctx context.Context, runKey string) error {
	_, err := s.transitionRunStatus(ctx, runKey, statusActive, statusAborted)
	return err
}

func (s *service) ListRunFiles(ctx context.Context, runKey string) ([]RunFile, error) {
	return s.store.ListFiles(ctx, storeworkspace.ListFilesFilter{
		RunKey: strings.TrimSpace(runKey),
		Limit:  defaultListLimit,
	})
}

func (s *service) GetRunFile(ctx context.Context, runKey, path string) (*RunFile, error) {
	return s.store.GetFile(ctx, strings.TrimSpace(runKey), strings.TrimSpace(path))
}

func (s *service) transitionRunStatus(ctx context.Context, runKey, fromStatus, toStatus string) (*Run, error) {
	key := strings.TrimSpace(runKey)
	if key == "" {
		return nil, errors.New("runKey is required")
	}
	run, err := s.store.TransitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     key,
		FromStatus: fromStatus,
		Status:     toStatus,
	})
	if err == nil {
		return run, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	current, getErr := s.store.GetRun(ctx, key)
	if getErr != nil {
		if errors.Is(getErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("run %q not found", key)
		}
		return nil, getErr
	}
	return nil, fmt.Errorf("run %q status is %s, expected %s", key, current.Status, fromStatus)
}

func (s *service) upsertRunFiles(ctx context.Context, run *Run, files []string) error {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	for _, raw := range files {
		rel, err := normalizeRelativePath(raw)
		if err != nil {
			return err
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		file, err := buildRunFile(run, rel)
		if err != nil {
			return fmt.Errorf("prepare run file %q: %w", rel, err)
		}
		if _, err := s.store.UpsertFile(ctx, file); err != nil {
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
