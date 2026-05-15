package thread

import (
	"context"
	"strings"
	"testing"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type stubSharedFileStore struct {
	files      map[string]sharedfilestore.SharedFile
	upserts    []sharedfilestore.UpsertParams
	getErr     error
	upsertErr  error
	deleteErr  error
	deletePath string
}

func (s *stubSharedFileStore) Get(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.files == nil {
		return nil, nil
	}
	file, ok := s.files[path]
	if !ok {
		return nil, nil
	}
	copy := file
	return &copy, nil
}

func (s *stubSharedFileStore) List(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

func (s *stubSharedFileStore) Upsert(_ context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	s.upserts = append(s.upserts, params)
	if s.files == nil {
		s.files = map[string]sharedfilestore.SharedFile{}
	}
	file := sharedfilestore.SharedFile{
		Path:      params.Path,
		Content:   params.Content,
		UpdatedBy: params.UpdatedBy,
		UpdatedAt: time.Now().UTC(),
	}
	s.files[params.Path] = file
	copy := file
	return &copy, nil
}

func (s *stubSharedFileStore) Delete(_ context.Context, path string) (int64, error) {
	if s.deleteErr != nil {
		return 0, s.deleteErr
	}
	s.deletePath = path
	delete(s.files, path)
	return 1, nil
}

func TestPrepareTaskHandoffStartAutoCreatesTaskForAutomatedThread(t *testing.T) {
	t.Parallel()
	files := &stubSharedFileStore{}
	svc := &service{
		threadStore:  &stubThreadStore{},
		sharedFiles:  files,
		bindingStore: &stubBindingStore{},
	}
	req := StartRequest{
		AgentType: "worker",
		Name:      "Memory Center Refactor",
	}
	if err := svc.prepareTaskHandoffStart(context.Background(), &req); err != nil {
		t.Fatalf("prepareTaskHandoffStart() error = %v", err)
	}
	taskID := firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake)
	handoffFile := firstConfigString(req.Config, taskConfigKeyHandoffFile, taskConfigKeyHandoffFileSnake)
	if taskID == "" || handoffFile == "" {
		t.Fatalf("task config = %#v, want taskId + handoffFile", req.Config)
	}
	if !strings.HasPrefix(handoffFile, taskHandoffPrefix) {
		t.Fatalf("handoffFile = %q, want %q prefix", handoffFile, taskHandoffPrefix)
	}
	if len(files.upserts) != 1 {
		t.Fatalf("shared file upserts = %d, want 1", len(files.upserts))
	}
	if got := files.upserts[0].Path; got != handoffFile {
		t.Fatalf("handoff upsert path = %q, want %q", got, handoffFile)
	}
}

func TestPrepareTaskHandoffStartInheritsFromParentAgent(t *testing.T) {
	t.Parallel()
	sourceThread := &threadstore.Thread{
		ThreadID: "thread-source",
		Prompt:   "Source task",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Runtime: map[string]any{
				taskConfigKeyID:          "task-demo",
				taskConfigKeyTitle:       "Memory Center Refactor",
				taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
			},
		}),
	}
	files := &stubSharedFileStore{
		files: map[string]sharedfilestore.SharedFile{
			"handoff/tasks/task-demo.md": {
				Path:    "handoff/tasks/task-demo.md",
				Content: "# Task Handoff\n\n## Latest Outcome\nalready done",
			},
		},
	}
	svc := &service{
		threadStore: &stubThreadStore{thread: sourceThread},
		bindingStore: &stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-root",
			ProviderThreadID: "thread-source",
			CodexThreadID:    "thread-source",
		}},
		sharedFiles: files,
	}
	req := StartRequest{
		ParentAgentID: "agent-root",
		AgentType:     "reviewer",
		Name:          "Reviewer",
	}
	if err := svc.prepareTaskHandoffStart(context.Background(), &req); err != nil {
		t.Fatalf("prepareTaskHandoffStart() error = %v", err)
	}
	if got := firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake); got != "task-demo" {
		t.Fatalf("taskId = %q, want %q", got, "task-demo")
	}
	if req.OwnerThreadID != "thread-source" {
		t.Fatalf("OwnerThreadID = %q, want thread-source", req.OwnerThreadID)
	}
	if !strings.Contains(req.BaseInstructions, "Task Handoff") || !strings.Contains(req.BaseInstructions, "already done") {
		t.Fatalf("BaseInstructions = %q, want injected handoff block", req.BaseInstructions)
	}
}

func TestOnTurnCompletedRefreshesTaskHandoff(t *testing.T) {
	t.Parallel()
	thread := &threadstore.Thread{
		ThreadID: "thread-1",
		Prompt:   "Demo Task",
		Status:   "running",
		Cwd:      "/repo",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Runtime: map[string]any{
				taskConfigKeyID:          "task-1",
				taskConfigKeyTitle:       "Demo Task",
				taskConfigKeyHandoffFile: "handoff/tasks/task-1.md",
			},
		}),
	}
	files := &stubSharedFileStore{}
	svc := &service{
		threadStore: &stubThreadStore{thread: thread},
		sharedFiles: files,
	}
	// P22 P2 thread S3: onTurnCompleted now only enqueues into the
	// taskHandoffWorker; the worker runs refreshTaskHandoffFromThread on
	// its own goroutine. Wire + start the worker so the integration path
	// under test still exercises event -> enqueue -> refresh -> upsert.
	svc.taskHandoffWorker = newTaskHandoffWorker(svc, silentLogger())
	svc.taskHandoffWorker.Start()

	svc.onTurnCompleted(turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"}},
		},
		Success: true,
		Status:  "completed",
		Summary: "Implemented durable memory promote flow",
	})

	// Stop synchronously drains pending before the worker goroutine
	// exits, so by the time Stop returns the upsert is already recorded
	// on the stub. This avoids a polling loop and the data race between
	// the worker goroutine writing files.upserts and the test reading it.
	if err := svc.taskHandoffWorker.Stop(context.Background()); err != nil {
		t.Fatalf("task handoff worker Stop: %v", err)
	}

	if len(files.upserts) != 1 {
		t.Fatalf("handoff upserts = %d, want 1", len(files.upserts))
	}
	if got := files.upserts[0].Path; got != "handoff/tasks/task-1.md" {
		t.Fatalf("handoff path = %q, want %q", got, "handoff/tasks/task-1.md")
	}
	if !strings.Contains(files.upserts[0].Content, "Implemented durable memory promote flow") {
		t.Fatalf("handoff content = %q, want latest summary", files.upserts[0].Content)
	}
}

func TestBackfillResumeRootTaskId(t *testing.T) {
	t.Parallel()

	makeThread := func(t *testing.T, threadID, ownerThreadID string, runtime map[string]any) *threadstore.Thread {
		t.Helper()
		return &threadstore.Thread{
			ThreadID:      threadID,
			OwnerThreadID: ownerThreadID,
			ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
				Runtime: runtime,
			}),
		}
	}

	mustEncode := func(t *testing.T, runtime map[string]any) []byte {
		t.Helper()
		return mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: runtime})
	}

	t.Run("empty raw returns unchanged", func(t *testing.T) {
		t.Parallel()
		svc := &service{threadStore: &stubThreadStore{}}
		got := svc.backfillResumeRootTaskId(context.Background(), "", nil)
		if got != nil {
			t.Fatalf("got = %v, want nil", got)
		}
	})

	t.Run("no taskId returns unchanged", func(t *testing.T) {
		t.Parallel()
		svc := &service{threadStore: &stubThreadStore{}}
		raw := mustEncode(t, map[string]any{"otherKey": "value"})
		got := svc.backfillResumeRootTaskId(context.Background(), "thread-X", raw)
		if string(got) != string(raw) {
			t.Fatalf("got mutated, want unchanged")
		}
	})

	t.Run("already has rootTaskId returns unchanged", func(t *testing.T) {
		t.Parallel()
		svc := &service{threadStore: &stubThreadStore{}}
		raw := mustEncode(t, map[string]any{
			taskConfigKeyID:   "task-X",
			taskConfigKeyRoot: "task-already-set",
		})
		got := svc.backfillResumeRootTaskId(context.Background(), "thread-X", raw)
		if string(got) != string(raw) {
			t.Fatalf("got mutated, want unchanged")
		}
	})

	t.Run("missing rootTaskId with resolvable owner chain", func(t *testing.T) {
		t.Parallel()
		threadByID := map[string]*threadstore.Thread{
			"thread-root": makeThread(t, "thread-root", "", map[string]any{taskConfigKeyID: "task-root"}),
		}
		svc := &service{threadStore: &stubThreadStore{threadByID: threadByID}}
		raw := mustEncode(t, map[string]any{taskConfigKeyID: "task-mid"})
		got := svc.backfillResumeRootTaskId(context.Background(), "thread-root", raw)
		stored := decodeStoredThreadConfig(got)
		if rootID := firstConfigString(stored.Runtime, taskConfigKeyRoot, taskConfigKeyRootSnake); rootID != "task-root" {
			t.Fatalf("rootTaskId = %q, want task-root", rootID)
		}
		if taskID := firstConfigString(stored.Runtime, taskConfigKeyID, taskConfigKeyIDSnake); taskID != "task-mid" {
			t.Fatalf("taskId mutated to %q, want task-mid", taskID)
		}
	})

	t.Run("missing rootTaskId without owner falls back to self taskId", func(t *testing.T) {
		t.Parallel()
		svc := &service{threadStore: &stubThreadStore{}}
		raw := mustEncode(t, map[string]any{taskConfigKeyID: "task-self"})
		got := svc.backfillResumeRootTaskId(context.Background(), "", raw)
		stored := decodeStoredThreadConfig(got)
		if rootID := firstConfigString(stored.Runtime, taskConfigKeyRoot, taskConfigKeyRootSnake); rootID != "task-self" {
			t.Fatalf("rootTaskId = %q, want fallback task-self", rootID)
		}
	})
}

func TestEnsureHandoffExists(t *testing.T) {
	t.Parallel()

	t.Run("nil sharedFiles returns error", func(t *testing.T) {
		t.Parallel()
		svc := &service{}
		err := svc.EnsureHandoffExists(context.Background(), "task-X")
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("empty taskID returns error", func(t *testing.T) {
		t.Parallel()
		svc := &service{sharedFiles: &stubSharedFileStore{}}
		err := svc.EnsureHandoffExists(context.Background(), "")
		if err == nil {
			t.Fatalf("expected error for empty taskId")
		}
	})

	t.Run("handoff file exists returns nil", func(t *testing.T) {
		t.Parallel()
		files := &stubSharedFileStore{files: map[string]sharedfilestore.SharedFile{
			"handoff/tasks/task-A.md": {Path: "handoff/tasks/task-A.md", Content: "# Handoff"},
		}}
		svc := &service{sharedFiles: files}
		err := svc.EnsureHandoffExists(context.Background(), "task-A")
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("handoff file missing returns wrapped error", func(t *testing.T) {
		t.Parallel()
		files := &stubSharedFileStore{files: map[string]sharedfilestore.SharedFile{}}
		svc := &service{sharedFiles: files}
		err := svc.EnsureHandoffExists(context.Background(), "task-Z")
		if err == nil {
			t.Fatalf("expected error for missing file")
		}
		if !strings.Contains(err.Error(), "handoff/tasks/task-Z.md") {
			t.Fatalf("error should contain path; got %v", err)
		}
	})
}

// Phase 3.10a: handoff 模板末尾追加「Long-running Progress Protocol」段，
// 让 agent 启动读 handoff 时能看到约定路径，配合前端 watchdog 识别 progress/done。
func TestRenderTaskHandoffDocumentIncludesProgressProtocol(t *testing.T) {
	t.Parallel()

	t.Run("with task id renders progress and done paths", func(t *testing.T) {
		t.Parallel()
		meta := taskHandoffMeta{TaskID: "task_abc123", TaskTitle: "demo", HandoffFile: "handoff/tasks/task_abc123.md"}
		doc := renderTaskHandoffDocument(meta, nil, taskHandoffRenderSeed{Status: "initialized"}, nil)
		if !strings.Contains(doc, "## Long-running Progress Protocol") {
			t.Fatalf("expected progress protocol heading, got:\n%s", doc)
		}
		if !strings.Contains(doc, "_internal/progress/task_abc123.md") {
			t.Fatalf("expected progress path with real task id, got:\n%s", doc)
		}
		if !strings.Contains(doc, "_internal/done/task_abc123.md") {
			t.Fatalf("expected done path with real task id, got:\n%s", doc)
		}
		// 协议段必须在 ## Risks 之后（约定为文档末尾固定段）
		protocolIdx := strings.Index(doc, "## Long-running Progress Protocol")
		risksIdx := strings.Index(doc, "## Risks")
		if risksIdx == -1 || protocolIdx == -1 || protocolIdx <= risksIdx {
			t.Fatalf("protocol must follow ## Risks; risks=%d protocol=%d", risksIdx, protocolIdx)
		}
	})

	t.Run("empty task id omits protocol section to avoid broken paths", func(t *testing.T) {
		t.Parallel()
		meta := taskHandoffMeta{TaskID: "   ", TaskTitle: "untitled"}
		doc := renderTaskHandoffDocument(meta, nil, taskHandoffRenderSeed{}, nil)
		if strings.Contains(doc, "## Long-running Progress Protocol") {
			t.Fatalf("expected no protocol section when task id blank, got:\n%s", doc)
		}
		if strings.Contains(doc, "_internal/progress/.md") || strings.Contains(doc, "_internal/done/.md") {
			t.Fatalf("must not emit broken empty-id paths, got:\n%s", doc)
		}
	})
}
