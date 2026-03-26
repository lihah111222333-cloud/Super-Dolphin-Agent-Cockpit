package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
)

func TestDryRunMergeTransitionsThroughMergingAndBack(t *testing.T) {
	store, _ := newRemovedFileStore(t, "run-dry-transition")
	recorder := &mergeEventRecorder{}
	svc := newTestServiceWithRecorder(store, recorder)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:        "run-dry-transition",
		UpdatedBy:     "worker-dry",
		DryRun:        true,
		DeleteRemoved: true,
	})
	if err != nil {
		t.Fatalf("MergeRun() error = %v, want nil", err)
	}
	if result.Status != statusActive {
		t.Fatalf("MergeRun() status = %q, want %q", result.Status, statusActive)
	}
	if got := transitionPairs(store.transitions); !equalStrings(got, []string{"active->merging", "merging->active"}) {
		t.Fatalf("MergeRun() transitions = %#v", got)
	}
	if got := store.run.Status; got != statusActive {
		t.Fatalf("run status after dry-run = %q, want %q", got, statusActive)
	}
	assertStatusChangePairs(t, recorder, []string{"active->merging", "merging->active"})
	if got := len(recorder.merged); got != 0 {
		t.Fatalf("merged events = %d, want 0", got)
	}
	if got := len(recorder.mergeErrors); got != 0 {
		t.Fatalf("merge error events = %d, want 0", got)
	}
}

func TestMergeRunDeleteRemovedDetectsSourceConflict(t *testing.T) {
	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	filePath := "removed-conflict.txt"
	if err := os.WriteFile(filepath.Join(sourceRoot, filePath), []byte("source-changed"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	store := &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        "run-delete-conflict",
			SourceRoot:    sourceRoot,
			WorkspacePath: workspacePath,
			Status:        statusActive,
		},
		files: []storeworkspace.WorkspaceRunFile{{
			RunKey:         "run-delete-conflict",
			RelativePath:   filePath,
			BaselineSHA256: sha256Hex("baseline"),
			State:          fileStateTracked,
		}},
	}
	svc := newTestService(store)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:        "run-delete-conflict",
		UpdatedBy:     "worker-delete",
		DeleteRemoved: true,
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
	if got := result.Files[0].Action; got != "conflict" {
		t.Fatalf("MergeRun() action = %q, want %q", got, "conflict")
	}
	if got := result.Files[0].Reason; got != "delete conflict: source changed since baseline" {
		t.Fatalf("MergeRun() reason = %q", got)
	}
	if got := store.files[0].State; got != fileStateConflict {
		t.Fatalf("MergeRun() file state = %q, want %q", got, fileStateConflict)
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, filePath)); err != nil {
		t.Fatalf("source file after conflict err = %v, want nil", err)
	}
}

func TestMergeRunDeleteRemovedTreatsMissingSourceAsRemoved(t *testing.T) {
	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	filePath := "already-missing.txt"
	store := &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        "run-delete-missing",
			SourceRoot:    sourceRoot,
			WorkspacePath: workspacePath,
			Status:        statusActive,
		},
		files: []storeworkspace.WorkspaceRunFile{{
			RunKey:         "run-delete-missing",
			RelativePath:   filePath,
			BaselineSHA256: sha256Hex("baseline"),
			State:          fileStateTracked,
		}},
	}
	svc := newTestService(store)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:        "run-delete-missing",
		UpdatedBy:     "worker-delete",
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
	if got := result.Files[0].Action; got != "removed" {
		t.Fatalf("MergeRun() action = %q, want %q", got, "removed")
	}
	if got := store.files[0].State; got != fileStateRemoved {
		t.Fatalf("MergeRun() file state = %q, want %q", got, fileStateRemoved)
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, filePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source file after remove err = %v, want not exist", err)
	}
}

func TestMergeRunCompensatesPersistFailureAndEmitsConsistentEvents(t *testing.T) {
	store := newMergeFixtureStore(t, "run-persist-fail", []mergeFixtureFile{
		{RelativePath: "ok.txt", SourceBody: "source-ok", WorkspaceBody: "workspace-ok"},
		{RelativePath: "later.txt", SourceBody: "source-later", WorkspaceBody: "workspace-later"},
	})
	recorder := &mergeEventRecorder{}
	svc := newTestServiceWithRecorder(&failOnceUpsertStore{
		testWorkspaceStore: store,
		failRelativePath:   "later.txt",
		failErr:            errors.New("persist failed"),
	}, recorder)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:    "run-persist-fail",
		UpdatedBy: "worker-comp",
	})
	if err == nil {
		t.Fatalf("MergeRun() error = nil, want persist failure")
	}
	if result != nil {
		t.Fatalf("MergeRun() result = %#v, want nil", result)
	}
	if got := err.Error(); got != `upsert run file "later.txt": persist failed` {
		t.Fatalf("MergeRun() error = %q", got)
	}
	if got := store.run.Status; got != statusFailed {
		t.Fatalf("run status after compensation = %q, want %q", got, statusFailed)
	}
	if got := transitionPairs(store.transitions); !equalStrings(got, []string{"active->merging", "merging->failed"}) {
		t.Fatalf("MergeRun() transitions = %#v", got)
	}
	assertStatusChangePairs(t, recorder, []string{"active->merging", "merging->failed"})
	if got := len(recorder.merged); got != 0 {
		t.Fatalf("merged events = %d, want 0", got)
	}
	if got := len(recorder.mergeErrors); got != 1 {
		t.Fatalf("merge error events = %d, want 1", got)
	}
	if got := recorder.mergeErrors[0].Message; got != err.Error() {
		t.Fatalf("merge error message = %q, want %q", got, err.Error())
	}
	for i, file := range store.files {
		if got := file.State; got != fileStateTracked {
			t.Fatalf("restored file[%d] state = %q, want %q", i, got, fileStateTracked)
		}
	}
	assertFileContent(t, filepath.Join(store.run.SourceRoot, "ok.txt"), "workspace-ok")
	assertFileContent(t, filepath.Join(store.run.SourceRoot, "later.txt"), "workspace-later")
}

func TestMergeRunFilesystemErrorLeavesSuccessfulWritesInPlace(t *testing.T) {
	store := newMergeFixtureStore(t, "run-fs-fail", []mergeFixtureFile{
		{RelativePath: "ok.txt", SourceBody: "source-ok", WorkspaceBody: "workspace-ok"},
		{RelativePath: "bad.txt", SourceBody: "source-bad", WorkspaceBody: "workspace-bad"},
	})
	badTarget := filepath.Join(store.run.SourceRoot, "bad-target.txt")
	if err := os.WriteFile(badTarget, []byte("source-bad"), 0o644); err != nil {
		t.Fatalf("WriteFile(bad target) error = %v", err)
	}
	badPath := filepath.Join(store.run.SourceRoot, "bad.txt")
	if err := os.Remove(badPath); err != nil {
		t.Fatalf("Remove(bad source) error = %v", err)
	}
	if err := os.Symlink(badTarget, badPath); err != nil {
		t.Fatalf("Symlink(bad source) error = %v", err)
	}
	recorder := &mergeEventRecorder{}
	svc := newTestServiceWithRecorder(store, recorder)

	result, err := svc.MergeRun(context.Background(), MergeRunRequest{
		RunKey:    "run-fs-fail",
		UpdatedBy: "worker-fs",
	})
	if err != nil {
		t.Fatalf("MergeRun() error = %v, want nil", err)
	}
	if result.Status != statusFailed {
		t.Fatalf("MergeRun() status = %q, want %q", result.Status, statusFailed)
	}
	if result.Merged != 1 || result.Errors != 1 {
		t.Fatalf("MergeRun() = %#v, want 1 merged and 1 error", result)
	}
	if got := transitionPairs(store.transitions); !equalStrings(got, []string{"active->merging", "merging->failed"}) {
		t.Fatalf("MergeRun() transitions = %#v", got)
	}
	assertStatusChangePairs(t, recorder, []string{"active->merging", "merging->failed"})
	if got := len(recorder.merged); got != 0 {
		t.Fatalf("merged events = %d, want 0", got)
	}
	if got := len(recorder.mergeErrors); got != 1 {
		t.Fatalf("merge error events = %d, want 1", got)
	}
	if got := recorder.mergeErrors[0].Message; got != result.Files[1].Reason {
		t.Fatalf("merge error message = %q, want %q", got, result.Files[1].Reason)
	}
	if got := store.files[0].State; got != fileStateMerged {
		t.Fatalf("file[0] state = %q, want %q", got, fileStateMerged)
	}
	if got := store.files[1].State; got != fileStateError {
		t.Fatalf("file[1] state = %q, want %q", got, fileStateError)
	}
	assertFileContent(t, filepath.Join(store.run.SourceRoot, "ok.txt"), "workspace-ok")
	info, err := os.Lstat(badPath)
	if err != nil {
		t.Fatalf("Lstat(bad source) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("bad source mode = %v, want symlink", info.Mode())
	}
}

type mergeEventRecorder struct {
	statusChanges []workspacedto.WorkspaceRunStatusChanged
	merged        []workspacedto.WorkspaceRunMerged
	mergeErrors   []workspacedto.WorkspaceRunMergeError
}

type failOnceUpsertStore struct {
	*testWorkspaceStore
	failRelativePath string
	failErr          error
	failed           bool
}

func (s *failOnceUpsertStore) UpsertFile(
	ctx context.Context,
	file storeworkspace.WorkspaceRunFile,
) (*storeworkspace.WorkspaceRunFile, error) {
	if !s.failed && file.RelativePath == s.failRelativePath {
		s.failed = true
		return nil, s.failErr
	}
	return s.testWorkspaceStore.UpsertFile(ctx, file)
}

type mergeFixtureFile struct {
	RelativePath  string
	SourceBody    string
	WorkspaceBody string
}

func newTestServiceWithRecorder(store storeworkspace.Store, recorder *mergeEventRecorder) *service {
	return &service{
		store:       store,
		emitCreated: func(workspacedto.WorkspaceRunCreated) {},
		emitMerged:  func(event workspacedto.WorkspaceRunMerged) { recorder.merged = append(recorder.merged, event) },
		emitAborted: func(workspacedto.WorkspaceRunAborted) {},
		emitMergeError: func(event workspacedto.WorkspaceRunMergeError) {
			recorder.mergeErrors = append(recorder.mergeErrors, event)
		},
		emitStatusChange: func(event workspacedto.WorkspaceRunStatusChanged) {
			recorder.statusChanges = append(recorder.statusChanges, event)
		},
	}
}

func newMergeFixtureStore(
	t *testing.T,
	runKey string,
	files []mergeFixtureFile,
) *testWorkspaceStore {
	t.Helper()
	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	runFiles := make([]storeworkspace.WorkspaceRunFile, 0, len(files))
	for _, file := range files {
		sourcePath := filepath.Join(sourceRoot, file.RelativePath)
		workspaceFilePath := filepath.Join(workspacePath, file.RelativePath)
		if err := os.WriteFile(sourcePath, []byte(file.SourceBody), 0o644); err != nil {
			t.Fatalf("WriteFile(source %q) error = %v", file.RelativePath, err)
		}
		if err := os.WriteFile(workspaceFilePath, []byte(file.WorkspaceBody), 0o644); err != nil {
			t.Fatalf("WriteFile(workspace %q) error = %v", file.RelativePath, err)
		}
		sourceHash := sha256Hex(file.SourceBody)
		runFiles = append(runFiles, storeworkspace.WorkspaceRunFile{
			RunKey:             runKey,
			RelativePath:       file.RelativePath,
			BaselineSHA256:     sourceHash,
			WorkspaceSHA256:    sha256Hex(file.WorkspaceBody),
			SourceSHA256Before: sourceHash,
			SourceSHA256After:  sourceHash,
			State:              fileStateTracked,
		})
	}
	return &testWorkspaceStore{
		run: storeworkspace.WorkspaceRun{
			RunKey:        runKey,
			SourceRoot:    sourceRoot,
			WorkspacePath: workspacePath,
			Status:        statusActive,
		},
		files: runFiles,
	}
}

func assertStatusChangePairs(t *testing.T, recorder *mergeEventRecorder, want []string) {
	t.Helper()
	got := make([]string, 0, len(recorder.statusChanges))
	for _, event := range recorder.statusChanges {
		got = append(got, event.OldStatus+"->"+event.NewStatus)
	}
	if !equalStrings(got, want) {
		t.Fatalf("status change events = %#v, want %#v", got, want)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if got := string(data); got != want {
		t.Fatalf("ReadFile(%q) = %q, want %q", path, got, want)
	}
}
