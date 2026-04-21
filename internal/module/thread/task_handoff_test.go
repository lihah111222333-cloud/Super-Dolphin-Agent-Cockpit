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
	svc.onTurnCompleted(turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"}},
		},
		Success: true,
		Status:  "completed",
		Summary: "Implemented durable memory promote flow",
	})
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
