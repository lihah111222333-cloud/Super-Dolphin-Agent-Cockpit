package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestWorkspaceGetRunHandlerReturnsV2CompatFields(t *testing.T) {
	t.Parallel()

	store := newWorkspaceHandlerTestStore()
	store.runs["run-1"] = workspaceHandlerTestRun("run-1", "dag-1", t.TempDir(), t.TempDir())
	server := newWorkspaceHandlerTestServer(store)

	raw := workspaceDispatchOK(t, server, "workspace/run/get", `{"runKey":"run-1"}`)
	var got struct {
		Run *Run `json:"run"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Run == nil {
		t.Fatal("run envelope missing")
	}
	if got.Run.RunKey != "run-1" || got.Run.DagKey != "dag-1" {
		t.Fatalf("run identity = (%q,%q), want (run-1,dag-1)", got.Run.RunKey, got.Run.DagKey)
	}
	if got.Run.SourceRoot == "" || got.Run.WorkspacePath == "" || got.Run.Status != statusActive {
		t.Fatalf("run v2-compatible fields not populated: %+v", *got.Run)
	}
	if store.getRunCalls != 1 {
		t.Fatalf("GetRun calls = %d, want 1", store.getRunCalls)
	}
}

func TestWorkspaceMergeRunDryRunDoesNotMutateSource(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	writeWorkspaceTestFile(t, sourceRoot, "tracked.txt", "source-original")
	writeWorkspaceTestFile(t, workspacePath, "tracked.txt", "workspace-change")
	baseline := workspaceHashTestFile(t, filepath.Join(sourceRoot, "tracked.txt"))

	store := newWorkspaceHandlerTestStore()
	store.runs["run-dry"] = workspaceHandlerTestRun("run-dry", "", sourceRoot, workspacePath)
	store.files[workspaceFileKey("run-dry", "tracked.txt")] = storeworkspace.WorkspaceRunFile{
		RunKey:             "run-dry",
		RelativePath:       "tracked.txt",
		BaselineSHA256:     baseline,
		SourceSHA256Before: baseline,
		SourceSHA256After:  baseline,
		State:              fileStateTracked,
	}
	server := newWorkspaceHandlerTestServer(store)

	raw := workspaceDispatchOK(t, server, "workspace/run/merge", `{"run_key":"run-dry","dry_run":true,"updated_by":"tester"}`)
	var got mergeResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Result == nil || !got.Result.DryRun || got.Result.Merged != 1 || got.Result.Status != statusActive {
		t.Fatalf("dry-run result = %+v, want dry merged active", got.Result)
	}
	if content := readWorkspaceTestFile(t, sourceRoot, "tracked.txt"); content != "source-original" {
		t.Fatalf("source content = %q, want source-original", content)
	}
	if store.upsertFileCalls != 0 {
		t.Fatalf("UpsertFile calls = %d, want 0 for dry-run", store.upsertFileCalls)
	}
}

func TestWorkspaceMergeRunReportsConflict(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	writeWorkspaceTestFile(t, sourceRoot, "tracked.txt", "baseline")
	baseline := workspaceHashTestFile(t, filepath.Join(sourceRoot, "tracked.txt"))
	writeWorkspaceTestFile(t, sourceRoot, "tracked.txt", "source-changed")
	writeWorkspaceTestFile(t, workspacePath, "tracked.txt", "workspace-change")

	store := newWorkspaceHandlerTestStore()
	store.runs["run-conflict"] = workspaceHandlerTestRun("run-conflict", "", sourceRoot, workspacePath)
	store.files[workspaceFileKey("run-conflict", "tracked.txt")] = storeworkspace.WorkspaceRunFile{
		RunKey:             "run-conflict",
		RelativePath:       "tracked.txt",
		BaselineSHA256:     baseline,
		SourceSHA256Before: baseline,
		SourceSHA256After:  baseline,
		State:              fileStateTracked,
	}
	server := newWorkspaceHandlerTestServer(store)

	raw := workspaceDispatchOK(t, server, "workspace/run/merge", `{"run_key":"run-conflict","updated_by":"tester"}`)
	var got mergeResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Result == nil || got.Result.Conflicts != 1 || got.Result.Status != statusFailed {
		t.Fatalf("conflict result = %+v, want one conflict and failed status", got.Result)
	}
	if content := readWorkspaceTestFile(t, sourceRoot, "tracked.txt"); content != "source-changed" {
		t.Fatalf("source content = %q, want source-changed", content)
	}
	stored := store.file("run-conflict", "tracked.txt")
	if stored.State != fileStateConflict || !strings.Contains(stored.LastError, "source changed") {
		t.Fatalf("stored file = %+v, want conflict with reason", stored)
	}
}

func TestWorkspaceAbortRunIdempotent(t *testing.T) {
	t.Parallel()

	store := newWorkspaceHandlerTestStore()
	store.runs["run-abort"] = workspaceHandlerTestRun("run-abort", "", t.TempDir(), t.TempDir())
	server := newWorkspaceHandlerTestServer(store)

	const workers = 2
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Go(func() {
			_, err := server.Dispatch(context.Background(), "workspace/run/abort", json.RawMessage(`{"run_key":"run-abort","updated_by":"tester","reason":"stop"}`))
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("workspace/run/abort error = %v", err)
		}
	}
	if got := store.run("run-abort").Status; got != statusAborted {
		t.Fatalf("run status = %q, want aborted", got)
	}
	if store.updateRunStatusCalls != workers {
		t.Fatalf("UpdateRunStatus calls = %d, want %d", store.updateRunStatusCalls, workers)
	}
}

func TestWorkspaceHandlersRejectMissingRunKey(t *testing.T) {
	t.Parallel()

	store := newWorkspaceHandlerTestStore()
	server := newWorkspaceHandlerTestServer(store)
	tests := []struct {
		name   string
		method string
		params string
	}{
		{name: "get", method: "workspace/run/get", params: `{}`},
		{name: "merge", method: "workspace/run/merge", params: `{"dry_run":true}`},
		{name: "abort", method: "workspace/run/abort", params: `{}`},
		{name: "files-list", method: "workspace/run/files/list", params: `{}`},
		{name: "file-get", method: "workspace/run/file/get", params: `{"path":"tracked.txt"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.Dispatch(context.Background(), tt.method, json.RawMessage(tt.params))
			assertWorkspaceInvalidParams(t, err)
		})
	}
	if store.totalCalls() != 0 {
		t.Fatalf("store calls = %d, want 0 when validation fails", store.totalCalls())
	}
}

func TestWorkspaceCreateRunValidatesInputAndPropagatesStoreError(t *testing.T) {
	t.Parallel()

	store := newWorkspaceHandlerTestStore()
	server := newWorkspaceHandlerTestServer(store)
	_, err := server.Dispatch(context.Background(), "workspace/run/create", json.RawMessage(`{}`))
	assertWorkspaceInvalidParams(t, err)

	sourceRoot := t.TempDir()
	store.upsertRunErr = errors.New("store unavailable")
	_, err = server.Dispatch(context.Background(), "workspace/run/create", json.RawMessage(`{"run_key":"run-create","source_root":`+quoteWorkspaceJSONString(sourceRoot)+`}`))
	if err == nil || !strings.Contains(err.Error(), "store unavailable") {
		t.Fatalf("create error = %v, want store unavailable", err)
	}
	if store.upsertRunCalls != 1 {
		t.Fatalf("UpsertRun calls = %d, want 1", store.upsertRunCalls)
	}
}

func TestWorkspaceCreateRunRejectsSourceRootOutsideScope(t *testing.T) {
	t.Parallel()

	allowedRoot := t.TempDir()
	sourceRoot := t.TempDir()
	store := newWorkspaceHandlerTestStore()
	svc := NewService(store, nil)

	_, err := svc.CreateRun(context.Background(), CreateRunRequest{
		RunKey:     "run-outside",
		SourceRoot: sourceRoot,
		CWD:        allowedRoot,
		CreatedBy:  "tester",
	})
	if err == nil {
		t.Fatal("CreateRun() error = nil, want source root scope rejection")
	}
	if !strings.Contains(err.Error(), "outside allowed workspace roots") {
		t.Fatalf("CreateRun() error = %v, want allowed workspace roots rejection", err)
	}
	if store.upsertRunCalls != 0 {
		t.Fatalf("UpsertRun calls = %d, want 0 when source root is out of scope", store.upsertRunCalls)
	}
}

type workspaceHandlerTestStore struct {
	mu sync.Mutex

	runs  map[string]storeworkspace.WorkspaceRun
	files map[string]storeworkspace.WorkspaceRunFile

	upsertRunErr error
	getRunErr    error
	listFilesErr error

	upsertRunCalls        int
	getRunCalls           int
	listRunsCalls         int
	updateRunStatusCalls  int
	transitionStatusCalls int
	upsertFileCalls       int
	getFileCalls          int
	listFilesCalls        int
}

func newWorkspaceHandlerTestStore() *workspaceHandlerTestStore {
	return &workspaceHandlerTestStore{
		runs:  make(map[string]storeworkspace.WorkspaceRun),
		files: make(map[string]storeworkspace.WorkspaceRunFile),
	}
}

func (s *workspaceHandlerTestStore) WithTx(ctx context.Context, fn func(txStore storeworkspace.Store) error) error {
	return fn(s)
}

func (s *workspaceHandlerTestStore) UpsertRun(ctx context.Context, run storeworkspace.WorkspaceRun) (*storeworkspace.WorkspaceRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertRunCalls++
	if s.upsertRunErr != nil {
		return nil, s.upsertRunErr
	}
	now := time.Now()
	if run.ID == 0 {
		run.ID = int64(len(s.runs) + 1)
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	run.UpdatedAt = now
	s.runs[run.RunKey] = cloneWorkspaceTestRun(run)
	out := cloneWorkspaceTestRun(run)
	return &out, nil
}

func (s *workspaceHandlerTestStore) GetRun(ctx context.Context, runKey string) (*storeworkspace.WorkspaceRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getRunCalls++
	if s.getRunErr != nil {
		return nil, s.getRunErr
	}
	run, ok := s.runs[strings.TrimSpace(runKey)]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	out := cloneWorkspaceTestRun(run)
	return &out, nil
}

func (s *workspaceHandlerTestStore) ListRuns(ctx context.Context, filter storeworkspace.ListRunsFilter) ([]storeworkspace.WorkspaceRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listRunsCalls++
	out := make([]storeworkspace.WorkspaceRun, 0, len(s.runs))
	for _, run := range s.runs {
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		if filter.DagKey != "" && run.DagKey != filter.DagKey {
			continue
		}
		out = append(out, cloneWorkspaceTestRun(run))
	}
	return out, nil
}

func (s *workspaceHandlerTestStore) UpdateRunStatus(ctx context.Context, input storeworkspace.UpdateRunStatusInput) (*storeworkspace.WorkspaceRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateRunStatusCalls++
	run, ok := s.runs[strings.TrimSpace(input.RunKey)]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	run.Status = strings.TrimSpace(input.Status)
	run.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	run.Metadata = append(json.RawMessage(nil), input.Metadata...)
	run.UpdatedAt = time.Now()
	s.runs[run.RunKey] = run
	out := cloneWorkspaceTestRun(run)
	return &out, nil
}

func (s *workspaceHandlerTestStore) TransitionRunStatus(ctx context.Context, input storeworkspace.TransitionRunStatusInput) (*storeworkspace.WorkspaceRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transitionStatusCalls++
	run, ok := s.runs[strings.TrimSpace(input.RunKey)]
	if !ok || run.Status != strings.TrimSpace(input.FromStatus) {
		return nil, platformdb.ErrNotFound
	}
	run.Status = strings.TrimSpace(input.Status)
	run.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	run.Metadata = append(json.RawMessage(nil), input.Metadata...)
	run.UpdatedAt = time.Now()
	s.runs[run.RunKey] = run
	out := cloneWorkspaceTestRun(run)
	return &out, nil
}

func (s *workspaceHandlerTestStore) UpsertFile(ctx context.Context, file storeworkspace.WorkspaceRunFile) (*storeworkspace.WorkspaceRunFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertFileCalls++
	file.UpdatedAt = time.Now()
	if file.CreatedAt.IsZero() {
		file.CreatedAt = file.UpdatedAt
	}
	s.files[workspaceFileKey(file.RunKey, file.RelativePath)] = file
	out := file
	return &out, nil
}

func (s *workspaceHandlerTestStore) GetFile(ctx context.Context, runKey, relativePath string) (*storeworkspace.WorkspaceRunFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getFileCalls++
	file, ok := s.files[workspaceFileKey(runKey, relativePath)]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	out := file
	return &out, nil
}

func (s *workspaceHandlerTestStore) ListFiles(ctx context.Context, filter storeworkspace.ListFilesFilter) ([]storeworkspace.WorkspaceRunFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listFilesCalls++
	if s.listFilesErr != nil {
		return nil, s.listFilesErr
	}
	out := make([]storeworkspace.WorkspaceRunFile, 0, len(s.files))
	for _, file := range s.files {
		if file.RunKey != strings.TrimSpace(filter.RunKey) {
			continue
		}
		if filter.State != "" && file.State != strings.TrimSpace(filter.State) {
			continue
		}
		out = append(out, file)
	}
	return out, nil
}

func (s *workspaceHandlerTestStore) run(runKey string) storeworkspace.WorkspaceRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneWorkspaceTestRun(s.runs[runKey])
}

func (s *workspaceHandlerTestStore) file(runKey, relativePath string) storeworkspace.WorkspaceRunFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files[workspaceFileKey(runKey, relativePath)]
}

func (s *workspaceHandlerTestStore) totalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upsertRunCalls + s.getRunCalls + s.listRunsCalls + s.updateRunStatusCalls +
		s.transitionStatusCalls + s.upsertFileCalls + s.getFileCalls + s.listFilesCalls
}

func newWorkspaceHandlerTestServer(store *workspaceHandlerTestStore) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewWorkspaceHandlers(NewService(store, nil)).Handlers)
	return server
}

func workspaceDispatchOK(t *testing.T, server *platformrpc.Server, method, params string) json.RawMessage {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), method, json.RawMessage(params))
	if err != nil {
		t.Fatalf("Dispatch(%s) error = %v", method, err)
	}
	return raw
}

func assertWorkspaceInvalidParams(t *testing.T, err error) {
	t.Helper()
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch error = %T %[1]v, want *jrpc2.Error", err)
	}
	if rpcErr.Code == 0 {
		t.Fatalf("rpcErr.Code = %v, want non-zero rejection code", rpcErr.Code)
	}
	if !strings.Contains(rpcErr.Message, "required") {
		t.Fatalf("rpcErr.Message = %q, want required-field rejection", rpcErr.Message)
	}
}

func workspaceHandlerTestRun(runKey, dagKey, sourceRoot, workspacePath string) storeworkspace.WorkspaceRun {
	now := time.Now()
	return storeworkspace.WorkspaceRun{
		ID:            1,
		RunKey:        runKey,
		DagKey:        dagKey,
		SourceRoot:    sourceRoot,
		WorkspacePath: workspacePath,
		Status:        statusActive,
		Metadata:      json.RawMessage(`{}`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func cloneWorkspaceTestRun(run storeworkspace.WorkspaceRun) storeworkspace.WorkspaceRun {
	run.Metadata = append(json.RawMessage(nil), run.Metadata...)
	if run.FinishedAt != nil {
		finished := *run.FinishedAt
		run.FinishedAt = &finished
	}
	return run
}

func workspaceFileKey(runKey, relativePath string) string {
	return strings.TrimSpace(runKey) + "\x00" + strings.TrimSpace(relativePath)
}

func writeWorkspaceTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func readWorkspaceTestFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return string(data)
}

func workspaceHashTestFile(t *testing.T, path string) string {
	t.Helper()
	hash, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile(%s) error = %v", path, err)
	}
	return hash
}

func quoteWorkspaceJSONString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
