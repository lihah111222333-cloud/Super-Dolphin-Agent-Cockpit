package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
	"github.com/jackc/pgx/v5"
)

func TestBuildRunRejectsInvalidRunKey(t *testing.T) {
	sourceRoot := t.TempDir()
	cases := []string{
		"../escape",
		"nested/escape",
		`nested\escape`,
		"run..key",
		"run.key",
		"run key",
		strings.Repeat("a", 129),
	}
	for _, runKey := range cases {
		t.Run(runKey, func(t *testing.T) {
			_, err := buildRun(CreateRunRequest{RunKey: runKey, SourceRoot: sourceRoot})
			if err == nil {
				t.Fatalf("buildRun() error = nil, want invalid run key")
			}
			if got := err.Error(); got != "workspace: invalid run key" {
				t.Fatalf("buildRun() error = %q, want %q", got, "workspace: invalid run key")
			}
		})
	}
}

func TestMergeRunTransitionsThroughMerging(t *testing.T) {
	store := &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        "run-1",
			SourceRoot:    t.TempDir(),
			WorkspacePath: t.TempDir(),
			Status:        statusActive,
		},
	}
	svc := newTestService(store)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:    "run-1",
		UpdatedBy: "worker-1",
	})
	if err != nil {
		t.Fatalf("MergeRun() error = %v, want nil", err)
	}
	if result.Status != statusMerged {
		t.Fatalf("MergeRun() status = %q, want %q", result.Status, statusMerged)
	}
	if got := transitionPairs(store.transitions); !equalStrings(got, []string{"active->merging", "merging->merged"}) {
		t.Fatalf("MergeRun() transitions = %#v", got)
	}
}

func TestMergeRunMarksConflictsFailed(t *testing.T) {
	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	filePath := "conflict.txt"
	if err := os.WriteFile(sourceRoot+"/"+filePath, []byte("source"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.WriteFile(workspacePath+"/"+filePath, []byte("workspace"), 0o644); err != nil {
		t.Fatalf("WriteFile(workspace) error = %v", err)
	}
	store := &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        "run-2",
			SourceRoot:    sourceRoot,
			WorkspacePath: workspacePath,
			Status:        statusActive,
		},
		files: []storeworkspace.WorkspaceRunFile{{
			RunKey:         "run-2",
			RelativePath:   filePath,
			BaselineSHA256: sha256Hex("baseline"),
			State:          fileStateTracked,
		}},
	}
	svc := newTestService(store)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:    "run-2",
		UpdatedBy: "worker-2",
	})
	if err != nil {
		t.Fatalf("MergeRun() error = %v, want nil", err)
	}
	if result.Status != statusFailed {
		t.Fatalf("MergeRun() status = %q, want %q", result.Status, statusFailed)
	}
	if result.Conflicts != 1 {
		t.Fatalf("MergeRun() conflicts = %d, want 1", result.Conflicts)
	}
	if got := transitionPairs(store.transitions); !equalStrings(got, []string{"active->merging", "merging->failed"}) {
		t.Fatalf("MergeRun() transitions = %#v", got)
	}
	if store.files[0].State != fileStateConflict {
		t.Fatalf("MergeRun() file state = %q, want %q", store.files[0].State, fileStateConflict)
	}
}

func TestMergeRunRejectsConcurrentMerge(t *testing.T) {
	store := &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        "run-3",
			SourceRoot:    t.TempDir(),
			WorkspacePath: t.TempDir(),
			Status:        statusActive,
		},
		rejectActiveTransition: true,
	}
	svc := newTestService(store)

	_, err := svc.MergeRun(context.Background(), MergeRunRequest{RunKey: "run-3"})
	if err == nil {
		t.Fatalf("MergeRun() error = nil, want CAS failure")
	}
	if !strings.Contains(err.Error(), `run "run-3" status is merging, expected active`) {
		t.Fatalf("MergeRun() error = %q", err)
	}
	if got := transitionPairs(store.transitions); !equalStrings(got, []string{"active->merging"}) {
		t.Fatalf("MergeRun() transitions = %#v", got)
	}
}

func TestMergeRunMarksDeleteRemovedFiles(t *testing.T) {
	store, filePath := newRemovedFileStore(t, "run-removed")
	svc := newTestService(store)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:        "run-removed",
		UpdatedBy:     "worker-removed",
		DeleteRemoved: true,
	})
	if err != nil {
		t.Fatalf("MergeRun() error = %v, want nil", err)
	}
	if result.Status != statusMerged {
		t.Fatalf("MergeRun() status = %q, want %q", result.Status, statusMerged)
	}
	if result.Removed != 1 {
		t.Fatalf("MergeRun() removed = %d, want 1", result.Removed)
	}
	if result.Merged != 0 {
		t.Fatalf("MergeRun() merged = %d, want 0", result.Merged)
	}
	if got := result.Files[0].Action; got != "removed" {
		t.Fatalf("MergeRun() action = %q, want %q", got, "removed")
	}
	if got := store.files[0].State; got != fileStateRemoved {
		t.Fatalf("MergeRun() file state = %q, want %q", got, fileStateRemoved)
	}
	if got := store.files[0].RelativePath; got != filePath {
		t.Fatalf("MergeRun() relative path = %q, want %q", got, filePath)
	}
	if _, err := os.Stat(store.run.SourceRoot + "/" + filePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source file after remove err = %v, want not exist", err)
	}
}

func TestDryRunMergeReportsDeleteRemovedFiles(t *testing.T) {
	store, filePath := newRemovedFileStore(t, "run-dry-remove")
	svc := newTestService(store)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:        "run-dry-remove",
		DryRun:        true,
		DeleteRemoved: true,
	})
	if err != nil {
		t.Fatalf("MergeRun() error = %v, want nil", err)
	}
	if result.Status != statusActive {
		t.Fatalf("MergeRun() status = %q, want %q", result.Status, statusActive)
	}
	if result.Removed != 1 {
		t.Fatalf("MergeRun() removed = %d, want 1", result.Removed)
	}
	if got := result.Files[0].Action; got != "would_remove" {
		t.Fatalf("MergeRun() action = %q, want %q", got, "would_remove")
	}
	if got := store.files[0].State; got != fileStateTracked {
		t.Fatalf("MergeRun() file state = %q, want %q", got, fileStateTracked)
	}
	if _, err := os.Stat(store.run.SourceRoot + "/" + filePath); err != nil {
		t.Fatalf("source file after dry-run err = %v, want nil", err)
	}
}

func TestMergeRunWritesWorkspaceContentToSourceRoot(t *testing.T) {
	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	filePath := "merge.txt"
	if err := os.WriteFile(sourceRoot+"/"+filePath, []byte("source"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.WriteFile(workspacePath+"/"+filePath, []byte("workspace"), 0o644); err != nil {
		t.Fatalf("WriteFile(workspace) error = %v", err)
	}
	store := &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        "run-write",
			SourceRoot:    sourceRoot,
			WorkspacePath: workspacePath,
			Status:        statusActive,
		},
		files: []storeworkspace.WorkspaceRunFile{{
			RunKey:             "run-write",
			RelativePath:       filePath,
			BaselineSHA256:     sha256Hex("source"),
			WorkspaceSHA256:    sha256Hex("workspace"),
			SourceSHA256Before: sha256Hex("source"),
			State:              fileStateTracked,
		}},
	}
	svc := newTestService(store)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:    "run-write",
		UpdatedBy: "worker-write",
	})
	if err != nil {
		t.Fatalf("MergeRun() error = %v, want nil", err)
	}
	if result.Merged != 1 || result.Status != statusMerged {
		t.Fatalf("MergeRun() = %#v, want merged result", result)
	}
	data, err := os.ReadFile(sourceRoot + "/" + filePath)
	if err != nil {
		t.Fatalf("ReadFile(source) error = %v", err)
	}
	if string(data) != "workspace" {
		t.Fatalf("source content = %q, want %q", string(data), "workspace")
	}
	info, err := os.Stat(sourceRoot + "/" + filePath)
	if err != nil {
		t.Fatalf("Stat(source) error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("source mode = %o, want %o", got, 0o644)
	}
	if got := store.files[0].SourceSHA256After; got != sha256Hex("workspace") {
		t.Fatalf("SourceSHA256After = %q, want %q", got, sha256Hex("workspace"))
	}
}

func TestAbortRunCompletedStatusesRewriteToAborted(t *testing.T) {
	statuses := []string{statusMerged, statusFailed, statusAborted}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			store := &testWorkspaceStore{
				run: storeworkspace.WorkspaceRun{
					RunKey:        "run-" + status,
					SourceRoot:    t.TempDir(),
					WorkspacePath: t.TempDir(),
					Status:        status,
				},
			}
			svc := newTestService(store)
			if err := svc.AbortRun(context.Background(), store.run.RunKey, "worker", "noop"); err != nil {
				t.Fatalf("AbortRun() error = %v, want nil", err)
			}
			if got := len(store.transitions); got != 0 {
				t.Fatalf("AbortRun() transitions = %d, want 0", got)
			}
			if got := len(store.updates); got != 1 {
				t.Fatalf("AbortRun() updates = %d, want 1", got)
			}
			if got := store.run.Status; got != statusAborted {
				t.Fatalf("AbortRun() status = %q, want %q", got, statusAborted)
			}
		})
	}
}

func TestAbortRunTransitionsActiveRun(t *testing.T) {
	store := &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        "run-abort",
			SourceRoot:    t.TempDir(),
			WorkspacePath: t.TempDir(),
			Status:        statusActive,
		},
	}
	svc := newTestService(store)

	if err := svc.AbortRun(context.Background(), "run-abort", "worker", "stop"); err != nil {
		t.Fatalf("AbortRun() error = %v, want nil", err)
	}
	if got := transitionPairs(store.transitions); len(got) != 0 {
		t.Fatalf("AbortRun() transitions = %#v, want none", got)
	}
	if got := len(store.updates); got != 1 {
		t.Fatalf("AbortRun() updates = %d, want 1", got)
	}
	if got := store.run.Status; got != statusAborted {
		t.Fatalf("AbortRun() status = %q, want %q", got, statusAborted)
	}
}

type testWorkspaceStore struct {
	run                    storeworkspace.WorkspaceRun
	files                  []storeworkspace.WorkspaceRunFile
	updates                []storeworkspace.UpdateRunStatusInput
	transitions            []storeworkspace.TransitionRunStatusInput
	rejectActiveTransition bool
}

func newTestService(store storeworkspace.Store) *service {
	return &service{
		store:            store,
		emitCreated:      func(workspacedto.WorkspaceRunCreated) {},
		emitMerged:       func(workspacedto.WorkspaceRunMerged) {},
		emitAborted:      func(workspacedto.WorkspaceRunAborted) {},
		emitMergeError:   func(workspacedto.WorkspaceRunMergeError) {},
		emitStatusChange: func(workspacedto.WorkspaceRunStatusChanged) {},
	}
}

func (s *testWorkspaceStore) WithTx(ctx context.Context, fn func(txStore storeworkspace.Store) error) error {
	return fn(s)
}

func (s *testWorkspaceStore) UpsertRun(ctx context.Context, run storeworkspace.WorkspaceRun) (*storeworkspace.WorkspaceRun, error) {
	s.run = cloneRun(run)
	return cloneRunPtr(s.run), nil
}

func (s *testWorkspaceStore) GetRun(ctx context.Context, runKey string) (*storeworkspace.WorkspaceRun, error) {
	if runKey != s.run.RunKey {
		return nil, pgx.ErrNoRows
	}
	return cloneRunPtr(s.run), nil
}

func (s *testWorkspaceStore) ListRuns(ctx context.Context, filter storeworkspace.ListRunsFilter) ([]storeworkspace.WorkspaceRun, error) {
	return []storeworkspace.WorkspaceRun{cloneRun(s.run)}, nil
}

func (s *testWorkspaceStore) UpdateRunStatus(ctx context.Context, input storeworkspace.UpdateRunStatusInput) (*storeworkspace.WorkspaceRun, error) {
	s.updates = append(s.updates, input)
	if input.RunKey != s.run.RunKey {
		return nil, pgx.ErrNoRows
	}
	s.run.Status = input.Status
	s.run.UpdatedBy = input.UpdatedBy
	s.run.Metadata = append([]byte(nil), input.Metadata...)
	return cloneRunPtr(s.run), nil
}

func (s *testWorkspaceStore) TransitionRunStatus(ctx context.Context, input storeworkspace.TransitionRunStatusInput) (*storeworkspace.WorkspaceRun, error) {
	s.transitions = append(s.transitions, input)
	if input.RunKey != s.run.RunKey {
		return nil, pgx.ErrNoRows
	}
	if s.rejectActiveTransition && input.FromStatus == statusActive && input.Status == statusMerging {
		s.run.Status = statusMerging
		return nil, pgx.ErrNoRows
	}
	if s.run.Status != input.FromStatus {
		return nil, pgx.ErrNoRows
	}
	s.run.Status = input.Status
	s.run.UpdatedBy = input.UpdatedBy
	s.run.Metadata = append([]byte(nil), input.Metadata...)
	return cloneRunPtr(s.run), nil
}

func (s *testWorkspaceStore) UpsertFile(ctx context.Context, file storeworkspace.WorkspaceRunFile) (*storeworkspace.WorkspaceRunFile, error) {
	for i := range s.files {
		if s.files[i].RunKey == file.RunKey && s.files[i].RelativePath == file.RelativePath {
			s.files[i] = cloneFile(file)
			return cloneFilePtr(s.files[i]), nil
		}
	}
	s.files = append(s.files, cloneFile(file))
	return cloneFilePtr(file), nil
}

func (s *testWorkspaceStore) GetFile(ctx context.Context, runKey, relativePath string) (*storeworkspace.WorkspaceRunFile, error) {
	for _, file := range s.files {
		if file.RunKey == runKey && file.RelativePath == relativePath {
			return cloneFilePtr(file), nil
		}
	}
	return nil, pgx.ErrNoRows
}

func (s *testWorkspaceStore) ListFiles(ctx context.Context, filter storeworkspace.ListFilesFilter) ([]storeworkspace.WorkspaceRunFile, error) {
	out := make([]storeworkspace.WorkspaceRunFile, 0, len(s.files))
	for _, file := range s.files {
		if filter.RunKey != "" && file.RunKey != filter.RunKey {
			continue
		}
		if filter.State != "" && file.State != filter.State {
			continue
		}
		out = append(out, cloneFile(file))
	}
	return out, nil
}

func cloneRun(run storeworkspace.WorkspaceRun) storeworkspace.WorkspaceRun {
	run.Metadata = append([]byte(nil), run.Metadata...)
	return run
}

func cloneRunPtr(run storeworkspace.WorkspaceRun) *storeworkspace.WorkspaceRun {
	cloned := cloneRun(run)
	return &cloned
}

func cloneFile(file storeworkspace.WorkspaceRunFile) storeworkspace.WorkspaceRunFile {
	return file
}

func cloneFilePtr(file storeworkspace.WorkspaceRunFile) *storeworkspace.WorkspaceRunFile {
	cloned := cloneFile(file)
	return &cloned
}

func newRemovedFileStore(t *testing.T, runKey string) (*testWorkspaceStore, string) {
	t.Helper()
	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	filePath := "removed.txt"
	if err := os.WriteFile(sourceRoot+"/"+filePath, []byte("source"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	store := &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        runKey,
			SourceRoot:    sourceRoot,
			WorkspacePath: workspacePath,
			Status:        statusActive,
		},
		files: []storeworkspace.WorkspaceRunFile{{
			RunKey:             runKey,
			RelativePath:       filePath,
			BaselineSHA256:     sha256Hex("source"),
			SourceSHA256Before: sha256Hex("source"),
			State:              fileStateTracked,
		}},
	}
	return store, filePath
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func transitionPairs(items []storeworkspace.TransitionRunStatusInput) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.FromStatus+"->"+item.Status)
	}
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
