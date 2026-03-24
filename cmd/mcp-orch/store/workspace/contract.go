package workspace

import (
	"context"

	internalworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
)

type Store interface {
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	UpsertRun(ctx context.Context, run WorkspaceRun) (*WorkspaceRun, error)
	GetRun(ctx context.Context, runKey string) (*WorkspaceRun, error)
	ListRuns(ctx context.Context, filter ListRunsFilter) ([]WorkspaceRun, error)
	UpdateRunStatus(ctx context.Context, input UpdateRunStatusInput) (*WorkspaceRun, error)
	TransitionRunStatus(ctx context.Context, input TransitionRunStatusInput) (*WorkspaceRun, error)
	UpsertFile(ctx context.Context, file WorkspaceRunFile) (*WorkspaceRunFile, error)
	GetFile(ctx context.Context, runKey, relativePath string) (*WorkspaceRunFile, error)
	ListFiles(ctx context.Context, filter ListFilesFilter) ([]WorkspaceRunFile, error)
}

type ListRunsFilter = internalworkspace.ListRunsFilter
type UpdateRunStatusInput = internalworkspace.UpdateRunStatusInput
type TransitionRunStatusInput = internalworkspace.TransitionRunStatusInput
type ListFilesFilter = internalworkspace.ListFilesFilter
type WorkspaceRun = internalworkspace.WorkspaceRun
type WorkspaceRunFile = internalworkspace.WorkspaceRunFile

func AsInternalStore(store Store) internalworkspace.Store {
	if store == nil {
		return nil
	}
	return internalStoreAdapter{store: store}
}

type internalStoreAdapter struct {
	store Store
}

func (a internalStoreAdapter) WithTx(ctx context.Context, fn func(txStore internalworkspace.Store) error) error {
	return a.store.WithTx(ctx, func(tx Store) error {
		return fn(AsInternalStore(tx))
	})
}

func (a internalStoreAdapter) UpsertRun(ctx context.Context, run internalworkspace.WorkspaceRun) (*internalworkspace.WorkspaceRun, error) {
	return a.store.UpsertRun(ctx, run)
}

func (a internalStoreAdapter) GetRun(ctx context.Context, runKey string) (*internalworkspace.WorkspaceRun, error) {
	return a.store.GetRun(ctx, runKey)
}

func (a internalStoreAdapter) ListRuns(ctx context.Context, filter internalworkspace.ListRunsFilter) ([]internalworkspace.WorkspaceRun, error) {
	return a.store.ListRuns(ctx, filter)
}

func (a internalStoreAdapter) UpdateRunStatus(ctx context.Context, input internalworkspace.UpdateRunStatusInput) (*internalworkspace.WorkspaceRun, error) {
	return a.store.UpdateRunStatus(ctx, input)
}

func (a internalStoreAdapter) TransitionRunStatus(ctx context.Context, input internalworkspace.TransitionRunStatusInput) (*internalworkspace.WorkspaceRun, error) {
	return a.store.TransitionRunStatus(ctx, input)
}

func (a internalStoreAdapter) UpsertFile(ctx context.Context, file internalworkspace.WorkspaceRunFile) (*internalworkspace.WorkspaceRunFile, error) {
	return a.store.UpsertFile(ctx, file)
}

func (a internalStoreAdapter) GetFile(ctx context.Context, runKey, relativePath string) (*internalworkspace.WorkspaceRunFile, error) {
	return a.store.GetFile(ctx, runKey, relativePath)
}

func (a internalStoreAdapter) ListFiles(ctx context.Context, filter internalworkspace.ListFilesFilter) ([]internalworkspace.WorkspaceRunFile, error) {
	return a.store.ListFiles(ctx, filter)
}
