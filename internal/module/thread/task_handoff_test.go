package thread

import (
	"context"
	"errors"
	"fmt"
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

func TestPrepareTaskHandoffStart_Pin(t *testing.T) {
	t.Parallel()

	var nilSvc *service
	tests := []struct {
		name   string
		svc    *service
		req    *StartRequest
		assert func(t *testing.T, svc *service, req *StartRequest, err error)
	}{
		{
			name: "nil receiver and nil request are no-op",
			svc:  nilSvc,
			req:  nil,
			assert: func(t *testing.T, _ *service, _ *StartRequest, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("prepareTaskHandoffStart() error = %v", err)
				}
			},
		},
		{
			name: "request without task metadata stays untouched",
			svc:  &service{},
			req: &StartRequest{
				Name:             "Manual Thread",
				BaseInstructions: "base",
			},
			assert: func(t *testing.T, _ *service, req *StartRequest, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("prepareTaskHandoffStart() error = %v", err)
				}
				if req.Config != nil {
					t.Fatalf("Config = %#v, want nil", req.Config)
				}
				if req.BaseInstructions != "base" {
					t.Fatalf("BaseInstructions = %q, want base", req.BaseInstructions)
				}
			},
		},
		{
			name: "automated thread auto creates task shell",
			svc: &service{
				sharedFiles: &stubSharedFileStore{},
			},
			req: &StartRequest{
				AgentType: "worker",
				Name:      "Memory Center Refactor",
			},
			assert: func(t *testing.T, svc *service, req *StartRequest, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("prepareTaskHandoffStart() error = %v", err)
				}
				taskID := firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake)
				taskTitle := firstConfigString(req.Config, taskConfigKeyTitle, taskConfigKeyTitleSnake)
				handoffFile := firstConfigString(req.Config, taskConfigKeyHandoffFile, taskConfigKeyHandoffFileSnake)
				if !strings.HasPrefix(taskID, "task_") {
					t.Fatalf("taskID = %q, want task_ prefix", taskID)
				}
				if taskTitle != "Memory Center Refactor" {
					t.Fatalf("taskTitle = %q, want Memory Center Refactor", taskTitle)
				}
				if handoffFile != defaultTaskHandoffPath(taskID) {
					t.Fatalf("handoffFile = %q, want %q", handoffFile, defaultTaskHandoffPath(taskID))
				}
				files := svc.sharedFiles.(*stubSharedFileStore)
				if len(files.upserts) != 1 || files.upserts[0].Path != handoffFile {
					t.Fatalf("shared file upserts = %#v, want one shell upsert for %q", files.upserts, handoffFile)
				}
				if req.OwnerThreadID != "" {
					t.Fatalf("OwnerThreadID = %q, want empty", req.OwnerThreadID)
				}
			},
		},
		{
			name: "parent agent inherits task metadata and injects continue block",
			svc: &service{
				threadStore: &stubThreadStore{thread: &threadstore.Thread{
					ThreadID: "thread-source",
					Prompt:   "Source task",
					ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
						Runtime: map[string]any{
							taskConfigKeyID:          "task-demo",
							taskConfigKeyTitle:       "Memory Center Refactor",
							taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
						},
					}),
				}},
				bindingStore: &stubBindingStore{binding: &bindingstore.Binding{
					AgentID:          "agent-root",
					ProviderThreadID: "thread-source",
					CodexThreadID:    "thread-source",
				}},
				sharedFiles: &stubSharedFileStore{
					files: map[string]sharedfilestore.SharedFile{
						"handoff/tasks/task-demo.md": {
							Path:    "handoff/tasks/task-demo.md",
							Content: "# Task Handoff\n\n## Latest Outcome\nalready done",
						},
					},
				},
			},
			req: &StartRequest{
				ParentAgentID: "agent-root",
				AgentType:     "reviewer",
				Name:          "Reviewer",
			},
			assert: func(t *testing.T, svc *service, req *StartRequest, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("prepareTaskHandoffStart() error = %v", err)
				}
				if got := firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake); got != "task-demo" {
					t.Fatalf("taskID = %q, want task-demo", got)
				}
				if req.OwnerThreadID != "thread-source" {
					t.Fatalf("OwnerThreadID = %q, want thread-source", req.OwnerThreadID)
				}
				if !strings.Contains(req.BaseInstructions, "Task Handoff") || !strings.Contains(req.BaseInstructions, "already done") {
					t.Fatalf("BaseInstructions = %q, want injected handoff block", req.BaseInstructions)
				}
				if got := len(svc.sharedFiles.(*stubSharedFileStore).upserts); got != 0 {
					t.Fatalf("handoff shell upserts = %d, want 0 when file already exists", got)
				}
			},
		},
		{
			name: "shell upsert error is ignored for non-continue tasks",
			svc: &service{
				sharedFiles: &stubSharedFileStore{upsertErr: errors.New("upsert failed")},
			},
			req: &StartRequest{
				Name: "Task Demo",
				Config: map[string]any{
					taskConfigKeyID: "task-demo",
				},
			},
			assert: func(t *testing.T, svc *service, req *StartRequest, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("prepareTaskHandoffStart() error = %v", err)
				}
				if got := firstConfigString(req.Config, taskConfigKeyHandoffFile, taskConfigKeyHandoffFileSnake); got != "handoff/tasks/task-demo.md" {
					t.Fatalf("handoffFile = %q, want handoff/tasks/task-demo.md", got)
				}
				if req.BaseInstructions != "" {
					t.Fatalf("BaseInstructions = %q, want empty", req.BaseInstructions)
				}
				if got := len(svc.sharedFiles.(*stubSharedFileStore).upserts); got != 0 {
					t.Fatalf("handoff shell upserts = %d, want 0 after upsert error", got)
				}
			},
		},
		{
			name: "continue block load error is ignored after shell refresh",
			svc: &service{
				sharedFiles: &stubSharedFileStore{getErr: errors.New("read failed")},
			},
			req: &StartRequest{
				Name:             "Task Demo",
				BaseInstructions: "base",
				Config: map[string]any{
					taskConfigKeyID:          "task-demo",
					taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
					taskConfigKeyContinue:    true,
				},
			},
			assert: func(t *testing.T, svc *service, req *StartRequest, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("prepareTaskHandoffStart() error = %v", err)
				}
				if req.BaseInstructions != "base" {
					t.Fatalf("BaseInstructions = %q, want base when block load fails", req.BaseInstructions)
				}
				if got := len(svc.sharedFiles.(*stubSharedFileStore).upserts); got != 1 {
					t.Fatalf("handoff shell upserts = %d, want 1", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.svc.prepareTaskHandoffStart(context.Background(), tt.req)
			tt.assert(t, tt.svc, tt.req, err)
		})
	}
}

func TestResolveTaskHandoffStart_Pin(t *testing.T) {
	t.Parallel()

	makeThread := func(t *testing.T, threadID, prompt string, runtime map[string]any) *threadstore.Thread {
		t.Helper()
		return &threadstore.Thread{
			ThreadID: threadID,
			Prompt:   prompt,
			ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
				Runtime: runtime,
			}),
		}
	}

	tests := []struct {
		name   string
		svc    *service
		req    StartRequest
		assert func(t *testing.T, meta taskHandoffMeta, sourceThreadID string)
	}{
		{
			name: "empty request without automation returns nothing",
			svc:  &service{},
			req:  StartRequest{},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				if meta != (taskHandoffMeta{}) || sourceThreadID != "" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want empty", meta, sourceThreadID)
				}
			},
		},
		{
			name: "explicit camel config is preserved and normalized",
			svc:  &service{},
			req: StartRequest{Config: map[string]any{
				taskConfigKeyID:          "task-demo",
				taskConfigKeyTitle:       "Demo",
				taskConfigKeyHandoffFile: "/handoff/tasks/demo.md",
				taskConfigKeyContinue:    true,
			}},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				want := taskHandoffMeta{
					TaskID:      "task-demo",
					TaskTitle:   "Demo",
					HandoffFile: "handoff/tasks/demo.md",
					Continue:    true,
				}
				if meta != want || sourceThreadID != "" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "")
				}
			},
		},
		{
			name: "snake config keys are accepted",
			svc:  &service{},
			req: StartRequest{Config: map[string]any{
				taskConfigKeyIDSnake:          "task-snake",
				taskConfigKeyTitleSnake:       "Snake",
				taskConfigKeyHandoffFileSnake: "handoff\\tasks\\snake.md",
			}},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				want := taskHandoffMeta{
					TaskID:      "task-snake",
					TaskTitle:   "Snake",
					HandoffFile: "handoff/tasks/snake.md",
				}
				if meta != want || sourceThreadID != "" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "")
				}
			},
		},
		{
			name: "owner thread inherits task metadata and auto continues",
			svc: &service{
				threadStore: &stubThreadStore{thread: makeThread(t, "thread-source", "Source prompt", map[string]any{
					taskConfigKeyID: "task-inherited",
				})},
			},
			req: StartRequest{OwnerThreadID: "thread-source"},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				want := taskHandoffMeta{
					TaskID:      "task-inherited",
					TaskTitle:   "Source prompt",
					HandoffFile: "handoff/tasks/task-inherited.md",
					Continue:    true,
				}
				if meta != want || sourceThreadID != "thread-source" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "thread-source")
				}
			},
		},
		{
			name: "parent agent resolves source thread via binding store",
			svc: &service{
				threadStore: &stubThreadStore{thread: makeThread(t, "thread-source", "Ignored prompt", map[string]any{
					taskConfigKeyID:          "task-demo",
					taskConfigKeyTitle:       "Inherited title",
					taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
				})},
				bindingStore: &stubBindingStore{binding: &bindingstore.Binding{
					AgentID:          "agent-root",
					ProviderThreadID: "thread-source",
					CodexThreadID:    "thread-source",
				}},
			},
			req: StartRequest{ParentAgentID: "agent-root"},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				want := taskHandoffMeta{
					TaskID:      "task-demo",
					TaskTitle:   "Inherited title",
					HandoffFile: "handoff/tasks/task-demo.md",
					Continue:    true,
				}
				if meta != want || sourceThreadID != "thread-source" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "thread-source")
				}
			},
		},
		{
			name: "explicit task id overrides inherited id but reuses inherited title and file",
			svc: &service{
				threadStore: &stubThreadStore{thread: makeThread(t, "thread-source", "Ignored prompt", map[string]any{
					taskConfigKeyID:          "task-old",
					taskConfigKeyTitle:       "Old title",
					taskConfigKeyHandoffFile: "handoff/tasks/task-old.md",
				})},
			},
			req: StartRequest{
				OwnerThreadID: "thread-source",
				Name:          "New title should not win",
				Config: map[string]any{
					taskConfigKeyID: "task-new",
				},
			},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				want := taskHandoffMeta{
					TaskID:      "task-new",
					TaskTitle:   "Old title",
					HandoffFile: "handoff/tasks/task-old.md",
				}
				if meta != want || sourceThreadID != "thread-source" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "thread-source")
				}
			},
		},
		{
			name: "explicit continue survives without source thread",
			svc:  &service{},
			req: StartRequest{
				Name: "Demo",
				Config: map[string]any{
					taskConfigKeyID:       "task-demo",
					taskConfigKeyContinue: true,
				},
			},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				want := taskHandoffMeta{
					TaskID:      "task-demo",
					TaskTitle:   "Demo",
					HandoffFile: "handoff/tasks/task-demo.md",
					Continue:    true,
				}
				if meta != want || sourceThreadID != "" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "")
				}
			},
		},
		{
			name: "auto flag creates task from request name",
			svc:  &service{},
			req: StartRequest{
				Name: "Autocreate",
				Config: map[string]any{
					taskConfigKeyAuto: true,
				},
			},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				if !strings.HasPrefix(meta.TaskID, "task_") {
					t.Fatalf("TaskID = %q, want task_ prefix", meta.TaskID)
				}
				if meta.TaskTitle != "Autocreate" {
					t.Fatalf("TaskTitle = %q, want Autocreate", meta.TaskTitle)
				}
				if meta.HandoffFile != defaultTaskHandoffPath(meta.TaskID) {
					t.Fatalf("HandoffFile = %q, want %q", meta.HandoffFile, defaultTaskHandoffPath(meta.TaskID))
				}
				if meta.Continue || sourceThreadID != "" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want continue=false source=''", meta, sourceThreadID)
				}
			},
		},
		{
			name: "agent key wins over agent type for auto title",
			svc:  &service{},
			req: StartRequest{
				AgentType: "worker",
				AgentKey:  "planner",
			},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				if !strings.HasPrefix(meta.TaskID, "task_") {
					t.Fatalf("TaskID = %q, want task_ prefix", meta.TaskID)
				}
				if meta.TaskTitle != "planner" {
					t.Fatalf("TaskTitle = %q, want planner", meta.TaskTitle)
				}
				if meta.HandoffFile != defaultTaskHandoffPath(meta.TaskID) || meta.Continue || sourceThreadID != "" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want auto meta with planner title", meta, sourceThreadID)
				}
			},
		},
		{
			name: "owner thread without inherited task auto creates new task and keeps source",
			svc: &service{
				threadStore: &stubThreadStore{thread: makeThread(t, "thread-source", "Source prompt", nil)},
			},
			req: StartRequest{OwnerThreadID: "thread-source"},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				if !strings.HasPrefix(meta.TaskID, "task_") {
					t.Fatalf("TaskID = %q, want task_ prefix", meta.TaskID)
				}
				if meta.TaskTitle != "Source prompt" {
					t.Fatalf("TaskTitle = %q, want Source prompt", meta.TaskTitle)
				}
				if meta.HandoffFile != defaultTaskHandoffPath(meta.TaskID) || meta.Continue || sourceThreadID != "thread-source" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want source thread preserved with auto task", meta, sourceThreadID)
				}
			},
		},
		{
			name: "explicit task id without metadata falls back to Automated Task title",
			svc:  &service{},
			req: StartRequest{
				Config: map[string]any{
					taskConfigKeyID: "task-demo",
				},
			},
			assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
				t.Helper()
				want := taskHandoffMeta{
					TaskID:      "task-demo",
					TaskTitle:   "Automated Task",
					HandoffFile: "handoff/tasks/task-demo.md",
				}
				if meta != want || sourceThreadID != "" {
					t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, sourceThreadID := tt.svc.resolveTaskHandoffStart(context.Background(), &tt.req)
			tt.assert(t, meta, sourceThreadID)
		})
	}
}

func TestResolveRootTaskId(t *testing.T) {
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

	tests := []struct {
		name           string
		ownerThreadID  string
		threadByID     map[string]*threadstore.Thread
		nilThreadStore bool
		want           string
	}{
		{
			name:          "empty owner returns empty",
			ownerThreadID: "",
			threadByID:    map[string]*threadstore.Thread{},
			want:          "",
		},
		{
			name:           "nil threadStore returns empty",
			ownerThreadID:  "thread-A",
			nilThreadStore: true,
			want:           "",
		},
		{
			name:          "store ErrNotFound returns empty",
			ownerThreadID: "thread-missing",
			threadByID:    map[string]*threadstore.Thread{},
			want:          "",
		},
		{
			name:          "single layer (owner is root) returns its taskId",
			ownerThreadID: "thread-A",
			threadByID: map[string]*threadstore.Thread{
				"thread-A": makeThread(t, "thread-A", "", map[string]any{
					taskConfigKeyID: "task-root",
				}),
			},
			want: "task-root",
		},
		{
			name:          "two layers traverses to root",
			ownerThreadID: "thread-mid",
			threadByID: map[string]*threadstore.Thread{
				"thread-mid":  makeThread(t, "thread-mid", "thread-root", map[string]any{taskConfigKeyID: "task-mid"}),
				"thread-root": makeThread(t, "thread-root", "", map[string]any{taskConfigKeyID: "task-root"}),
			},
			want: "task-root",
		},
		{
			name:          "depth limit 10 cuts off cyclic chain",
			ownerThreadID: "thread-1",
			threadByID: func() map[string]*threadstore.Thread {
				m := map[string]*threadstore.Thread{}
				// 12 个 thread 形成 1 → 2 → 3 ... → 12，没有顶端
				for i := 1; i <= 12; i++ {
					curID := fmt.Sprintf("thread-%d", i)
					nextID := fmt.Sprintf("thread-%d", i+1)
					m[curID] = makeThread(t, curID, nextID, map[string]any{taskConfigKeyID: "task-fake"})
				}
				return m
			}(),
			want: "",
		},
		{
			name:          "root with no taskId returns empty",
			ownerThreadID: "thread-A",
			threadByID: map[string]*threadstore.Thread{
				"thread-A": makeThread(t, "thread-A", "", map[string]any{}),
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var svc *service
			if tt.nilThreadStore {
				svc = &service{}
			} else {
				svc = &service{threadStore: &stubThreadStore{threadByID: tt.threadByID}}
			}
			got := svc.resolveRootTaskId(context.Background(), tt.ownerThreadID)
			if got != tt.want {
				t.Fatalf("resolveRootTaskId() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrepareTaskHandoffStart_RootTaskId(t *testing.T) {
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

	t.Run("root task without OwnerThreadID falls back to self taskId", func(t *testing.T) {
		t.Parallel()
		svc := &service{
			threadStore:  &stubThreadStore{},
			sharedFiles:  &stubSharedFileStore{},
			bindingStore: &stubBindingStore{},
		}
		req := StartRequest{AgentType: "worker", Name: "Root Task"}
		if err := svc.prepareTaskHandoffStart(context.Background(), &req); err != nil {
			t.Fatalf("prepareTaskHandoffStart() error = %v", err)
		}
		taskID := firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake)
		rootID := firstConfigString(req.Config, taskConfigKeyRoot, taskConfigKeyRootSnake)
		if taskID == "" || rootID != taskID {
			t.Fatalf("rootTaskId = %q, want self taskId %q", rootID, taskID)
		}
	})

	t.Run("sub-agent 1 layer: rootTaskId from owner taskId", func(t *testing.T) {
		t.Parallel()
		rootThread := makeThread(t, "thread-root", "", map[string]any{
			taskConfigKeyID:    "task-root",
			taskConfigKeyTitle: "Root Task",
		})
		svc := &service{
			threadStore:  &stubThreadStore{thread: rootThread},
			sharedFiles:  &stubSharedFileStore{},
			bindingStore: &stubBindingStore{},
		}
		req := StartRequest{
			OwnerThreadID: "thread-root",
			AgentType:     "reviewer",
			Name:          "Reviewer",
		}
		if err := svc.prepareTaskHandoffStart(context.Background(), &req); err != nil {
			t.Fatalf("prepareTaskHandoffStart() error = %v", err)
		}
		if got := firstConfigString(req.Config, taskConfigKeyRoot, taskConfigKeyRootSnake); got != "task-root" {
			t.Fatalf("rootTaskId = %q, want task-root", got)
		}
	})

	t.Run("sub-agent 2 layers: traverses to root taskId", func(t *testing.T) {
		t.Parallel()
		threadByID := map[string]*threadstore.Thread{
			"thread-mid":  makeThread(t, "thread-mid", "thread-root", map[string]any{taskConfigKeyID: "task-mid"}),
			"thread-root": makeThread(t, "thread-root", "", map[string]any{taskConfigKeyID: "task-root"}),
		}
		svc := &service{
			threadStore:  &stubThreadStore{threadByID: threadByID},
			sharedFiles:  &stubSharedFileStore{},
			bindingStore: &stubBindingStore{},
		}
		req := StartRequest{
			OwnerThreadID: "thread-mid",
			AgentType:     "deep",
			Name:          "Deep Worker",
		}
		if err := svc.prepareTaskHandoffStart(context.Background(), &req); err != nil {
			t.Fatalf("prepareTaskHandoffStart() error = %v", err)
		}
		if got := firstConfigString(req.Config, taskConfigKeyRoot, taskConfigKeyRootSnake); got != "task-root" {
			t.Fatalf("rootTaskId = %q, want task-root (traversed from mid)", got)
		}
	})

	t.Run("explicit rootTaskId in Config wins", func(t *testing.T) {
		t.Parallel()
		rootThread := makeThread(t, "thread-root", "", map[string]any{taskConfigKeyID: "task-root"})
		svc := &service{
			threadStore:  &stubThreadStore{thread: rootThread},
			sharedFiles:  &stubSharedFileStore{},
			bindingStore: &stubBindingStore{},
		}
		req := StartRequest{
			OwnerThreadID: "thread-root",
			AgentType:     "reviewer",
			Name:          "Reviewer",
			Config: map[string]any{
				taskConfigKeyRoot: "task-explicit-override",
			},
		}
		if err := svc.prepareTaskHandoffStart(context.Background(), &req); err != nil {
			t.Fatalf("prepareTaskHandoffStart() error = %v", err)
		}
		if got := firstConfigString(req.Config, taskConfigKeyRoot, taskConfigKeyRootSnake); got != "task-explicit-override" {
			t.Fatalf("rootTaskId = %q, want explicit override", got)
		}
	})
}
