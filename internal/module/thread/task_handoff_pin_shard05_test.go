package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type prepareTaskHandoffStartPinCase struct {
	name   string
	setup  func(t *testing.T) (*service, *StartRequest)
	assert func(t *testing.T, svc *service, req *StartRequest, err error)
}

var prepareTaskHandoffStartPinCases = []prepareTaskHandoffStartPinCase{
	{
		name: "nil receiver and nil request are no-op",
		setup: func(t *testing.T) (*service, *StartRequest) {
			t.Helper()
			var nilSvc *service
			return nilSvc, nil
		},
		assert: func(t *testing.T, _ *service, _ *StartRequest, err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("prepareTaskHandoffStart() error = %v", err)
			}
		},
	},
	{
		name: "request without task metadata stays untouched",
		setup: func(t *testing.T) (*service, *StartRequest) {
			t.Helper()
			return &service{}, &StartRequest{Name: "Manual Thread", BaseInstructions: "base"}
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
		setup: func(t *testing.T) (*service, *StartRequest) {
			t.Helper()
			svc := &service{sharedFiles: &stubSharedFileStore{}}
			req := &StartRequest{AgentType: "worker", Name: "Memory Center Refactor"}
			return svc, req
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
		setup: func(t *testing.T) (*service, *StartRequest) {
			t.Helper()
			svc := &service{
				threadStore: &stubThreadStore{thread: &threadstore.Thread{
					ThreadID: "thread-source",
					Prompt:   "Source task",
					ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: map[string]any{
						taskConfigKeyID:          "task-demo",
						taskConfigKeyTitle:       "Memory Center Refactor",
						taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
					}}),
				}},
				bindingStore: &stubBindingStore{binding: &bindingstore.Binding{
					AgentID: "agent-root", ProviderThreadID: "thread-source", CodexThreadID: "thread-source",
				}},
				sharedFiles: &stubSharedFileStore{files: map[string]sharedfilestore.SharedFile{
					"handoff/tasks/task-demo.md": {Path: "handoff/tasks/task-demo.md", Content: "# Task Handoff\n\n## Latest Outcome\nalready done"},
				}},
			}
			req := &StartRequest{ParentAgentID: "agent-root", AgentType: "reviewer", Name: "Reviewer"}
			return svc, req
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
		name: "shell upsert error fails fast for non-continue tasks",
		setup: func(t *testing.T) (*service, *StartRequest) {
			t.Helper()
			svc := &service{sharedFiles: &stubSharedFileStore{upsertErr: errors.New("upsert failed")}}
			req := &StartRequest{Name: "Task Demo", Config: map[string]any{
				taskConfigKeyID:          "task-demo",
				taskConfigKeyTitle:       "Task Demo",
				taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
			}}
			return svc, req
		},
		assert: func(t *testing.T, svc *service, req *StartRequest, err error) {
			t.Helper()
			if err == nil || !strings.Contains(err.Error(), "upsert failed") {
				t.Fatalf("prepareTaskHandoffStart() error = %v, want upsert failed", err)
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
		name: "continue block load error fails fast after shell refresh",
		setup: func(t *testing.T) (*service, *StartRequest) {
			t.Helper()
			svc := &service{sharedFiles: &stubSharedFileStore{getErr: errors.New("read failed")}}
			req := &StartRequest{
				Name:             "Task Demo",
				BaseInstructions: "base",
				Config: map[string]any{
					taskConfigKeyID:          "task-demo",
					taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
					taskConfigKeyContinue:    true,
				},
			}
			return svc, req
		},
		assert: func(t *testing.T, svc *service, req *StartRequest, err error) {
			t.Helper()
			if err == nil || !strings.Contains(err.Error(), "read failed") {
				t.Fatalf("prepareTaskHandoffStart() error = %v, want read failed", err)
			}
			if req.BaseInstructions != "base" {
				t.Fatalf("BaseInstructions = %q, want base when block load fails", req.BaseInstructions)
			}
			if got := len(svc.sharedFiles.(*stubSharedFileStore).upserts); got != 0 {
				t.Fatalf("handoff shell upserts = %d, want 0 after shell read error", got)
			}
		},
	},
}

func TestPrepareTaskHandoffStart_Pin(t *testing.T) {
	t.Parallel()

	for _, tt := range prepareTaskHandoffStartPinCases {
		t.Run(tt.name, func(t *testing.T) {
			svc, req := tt.setup(t)
			err := svc.prepareTaskHandoffStart(context.Background(), req)
			tt.assert(t, svc, req, err)
		})
	}
}

type resolveTaskHandoffStartPinCase struct {
	name   string
	setup  func(t *testing.T) (*service, StartRequest)
	assert func(t *testing.T, meta taskHandoffMeta, sourceThreadID string)
}

var resolveTaskHandoffStartPinCases = []resolveTaskHandoffStartPinCase{
	{
		name:  "empty request without automation returns nothing",
		setup: func(t *testing.T) (*service, StartRequest) { t.Helper(); return &service{}, StartRequest{} },
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			if meta != (taskHandoffMeta{}) || sourceThreadID != "" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want empty", meta, sourceThreadID)
			}
		},
	},
	{
		name: "explicit camel config is preserved and normalized",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			req := StartRequest{Config: map[string]any{
				taskConfigKeyID: "task-demo", taskConfigKeyTitle: "Demo",
				taskConfigKeyHandoffFile: "/handoff/tasks/demo.md", taskConfigKeyContinue: true,
			}}
			return &service{}, req
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			want := taskHandoffMeta{TaskID: "task-demo", TaskTitle: "Demo", HandoffFile: "handoff/tasks/demo.md", Continue: true}
			if meta != want || sourceThreadID != "" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "")
			}
		},
	},
	{
		name: "snake config keys are accepted",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			req := StartRequest{Config: map[string]any{
				taskConfigKeyIDSnake: "task-snake", taskConfigKeyTitleSnake: "Snake",
				taskConfigKeyHandoffFileSnake: "handoff\\tasks\\snake.md",
			}}
			return &service{}, req
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			want := taskHandoffMeta{TaskID: "task-snake", TaskTitle: "Snake", HandoffFile: "handoff/tasks/snake.md"}
			if meta != want || sourceThreadID != "" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "")
			}
		},
	},
	{
		name: "owner thread inherits task metadata and auto continues",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			thread := taskHandoffTestThread(t, "thread-source", "", "Source prompt", map[string]any{
				taskConfigKeyID:          "task-inherited",
				taskConfigKeyHandoffFile: "handoff/tasks/task-inherited.md",
			})
			return &service{threadStore: &stubThreadStore{thread: thread}}, StartRequest{OwnerThreadID: "thread-source"}
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			want := taskHandoffMeta{TaskID: "task-inherited", TaskTitle: "Source prompt", HandoffFile: "handoff/tasks/task-inherited.md", Continue: true}
			if meta != want || sourceThreadID != "thread-source" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "thread-source")
			}
		},
	},
	{
		name: "parent agent resolves source thread via binding store",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			thread := taskHandoffTestThread(t, "thread-source", "", "Ignored prompt", map[string]any{
				taskConfigKeyID: "task-demo", taskConfigKeyTitle: "Inherited title", taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
			})
			svc := &service{threadStore: &stubThreadStore{thread: thread}, bindingStore: &stubBindingStore{binding: &bindingstore.Binding{
				AgentID: "agent-root", ProviderThreadID: "thread-source", CodexThreadID: "thread-source",
			}}}
			return svc, StartRequest{ParentAgentID: "agent-root", AgentType: "reviewer"}
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			want := taskHandoffMeta{TaskID: "task-demo", TaskTitle: "Inherited title", HandoffFile: "handoff/tasks/task-demo.md", Continue: true}
			if meta != want || sourceThreadID != "thread-source" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "thread-source")
			}
		},
	},
	{
		name: "parent agent without task request ignores inherited task metadata",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			thread := taskHandoffTestThread(t, "thread-source", "", "Ignored prompt", map[string]any{
				taskConfigKeyID: "task-demo", taskConfigKeyTitle: "Inherited title", taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
			})
			svc := &service{threadStore: &stubThreadStore{thread: thread}, bindingStore: &stubBindingStore{binding: &bindingstore.Binding{
				AgentID: "agent-root", ProviderThreadID: "thread-source", CodexThreadID: "thread-source",
			}}}
			return svc, StartRequest{ParentAgentID: "agent-root", Name: "Plain child"}
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			if meta != (taskHandoffMeta{}) || sourceThreadID != "" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want no task handoff", meta, sourceThreadID)
			}
		},
	},
	{
		name: "explicit task id overrides inherited id but reuses inherited title and file",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			thread := taskHandoffTestThread(t, "thread-source", "", "Ignored prompt", map[string]any{
				taskConfigKeyID: "task-old", taskConfigKeyTitle: "Old title", taskConfigKeyHandoffFile: "handoff/tasks/task-old.md",
			})
			req := StartRequest{OwnerThreadID: "thread-source", Name: "New title should not win", Config: map[string]any{taskConfigKeyID: "task-new"}}
			return &service{threadStore: &stubThreadStore{thread: thread}}, req
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			want := taskHandoffMeta{TaskID: "task-new", TaskTitle: "Old title", HandoffFile: "handoff/tasks/task-old.md"}
			if meta != want || sourceThreadID != "thread-source" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "thread-source")
			}
		},
	},
	{
		name: "explicit continue survives without source thread",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			req := StartRequest{Name: "Demo", Config: map[string]any{
				taskConfigKeyID:          "task-demo",
				taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
				taskConfigKeyContinue:    true,
			}}
			return &service{}, req
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			want := taskHandoffMeta{TaskID: "task-demo", TaskTitle: "Demo", HandoffFile: "handoff/tasks/task-demo.md", Continue: true}
			if meta != want || sourceThreadID != "" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "")
			}
		},
	},
	{
		name: "auto flag creates task from request name",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			return &service{}, StartRequest{Name: "Autocreate", Config: map[string]any{taskConfigKeyAuto: true}}
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			assertAutoTaskMeta(t, meta, "Autocreate")
			if meta.Continue || sourceThreadID != "" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want continue=false source=''", meta, sourceThreadID)
			}
		},
	},
	{
		name: "agent key wins over agent type for auto title",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			return &service{}, StartRequest{AgentType: "worker", AgentKey: "planner"}
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			assertAutoTaskMeta(t, meta, "planner")
			if meta.Continue || sourceThreadID != "" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want auto meta with planner title", meta, sourceThreadID)
			}
		},
	},
	{
		name: "owner thread without inherited task auto creates new task and keeps source",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			thread := taskHandoffTestThread(t, "thread-source", "", "Source prompt", nil)
			return &service{threadStore: &stubThreadStore{thread: thread}}, StartRequest{OwnerThreadID: "thread-source"}
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			assertAutoTaskMeta(t, meta, "Source prompt")
			if meta.Continue || sourceThreadID != "thread-source" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want source thread preserved with auto task", meta, sourceThreadID)
			}
		},
	},
	{
		name: "explicit task id with metadata is preserved",
		setup: func(t *testing.T) (*service, StartRequest) {
			t.Helper()
			return &service{}, StartRequest{Config: map[string]any{
				taskConfigKeyID:          "task-demo",
				taskConfigKeyTitle:       "Demo Task",
				taskConfigKeyHandoffFile: "handoff/tasks/task-demo.md",
			}}
		},
		assert: func(t *testing.T, meta taskHandoffMeta, sourceThreadID string) {
			t.Helper()
			want := taskHandoffMeta{TaskID: "task-demo", TaskTitle: "Demo Task", HandoffFile: "handoff/tasks/task-demo.md"}
			if meta != want || sourceThreadID != "" {
				t.Fatalf("resolveTaskHandoffStart() = (%#v, %q), want (%#v, %q)", meta, sourceThreadID, want, "")
			}
		},
	},
}

func TestResolveTaskHandoffStart_Pin(t *testing.T) {
	t.Parallel()

	for _, tt := range resolveTaskHandoffStartPinCases {
		t.Run(tt.name, func(t *testing.T) {
			svc, req := tt.setup(t)
			meta, sourceThreadID, err := svc.resolveTaskHandoffStart(context.Background(), &req)
			if err != nil {
				t.Fatalf("resolveTaskHandoffStart() error = %v", err)
			}
			tt.assert(t, meta, sourceThreadID)
		})
	}
}

func TestResolveTaskHandoffStartRejectsExplicitTaskWithoutMetadata(t *testing.T) {
	t.Parallel()

	svc := &service{}
	req := StartRequest{Config: map[string]any{taskConfigKeyID: "task-demo"}}
	_, _, err := svc.resolveTaskHandoffStart(context.Background(), &req)
	if err == nil || !strings.Contains(err.Error(), "task handoff title is required") {
		t.Fatalf("resolveTaskHandoffStart() error = %v, want title required", err)
	}
}

func TestResolveTaskHandoffStartRejectsMalformedConfig(t *testing.T) {
	t.Parallel()

	svc := &service{}
	req := StartRequest{Config: map[string]any{taskConfigKeyID: 123}}
	_, _, err := svc.resolveTaskHandoffStart(context.Background(), &req)
	if err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Fatalf("resolveTaskHandoffStart() error = %v, want string type error", err)
	}
}

func assertAutoTaskMeta(t *testing.T, meta taskHandoffMeta, wantTitle string) {
	t.Helper()

	if !strings.HasPrefix(meta.TaskID, "task_") {
		t.Fatalf("TaskID = %q, want task_ prefix", meta.TaskID)
	}
	if meta.TaskTitle != wantTitle {
		t.Fatalf("TaskTitle = %q, want %q", meta.TaskTitle, wantTitle)
	}
	if meta.HandoffFile != defaultTaskHandoffPath(meta.TaskID) {
		t.Fatalf("HandoffFile = %q, want %q", meta.HandoffFile, defaultTaskHandoffPath(meta.TaskID))
	}
}

func taskHandoffTestThread(t *testing.T, threadID, ownerThreadID, prompt string, runtime map[string]any) *threadstore.Thread {
	t.Helper()
	return &threadstore.Thread{
		ThreadID:      threadID,
		OwnerThreadID: ownerThreadID,
		Prompt:        prompt,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Runtime: runtime,
		}),
	}
}

type resolveRootTaskIDCase struct {
	name          string
	ownerThreadID string
	setup         func(t *testing.T) *service
	want          string
	wantErr       bool
}

var resolveRootTaskIDCases = []resolveRootTaskIDCase{
	{name: "empty owner fails fast", ownerThreadID: "", setup: func(t *testing.T) *service {
		t.Helper()
		return &service{threadStore: &stubThreadStore{threadByID: map[string]*threadstore.Thread{}}}
	}, wantErr: true},
	{name: "nil threadStore fails fast", ownerThreadID: "thread-A", setup: func(t *testing.T) *service { t.Helper(); return &service{} }, wantErr: true},
	{name: "store ErrNotFound fails fast", ownerThreadID: "thread-missing", setup: func(t *testing.T) *service {
		t.Helper()
		return &service{threadStore: &stubThreadStore{threadByID: map[string]*threadstore.Thread{}}}
	}, wantErr: true},
	{name: "single layer (owner is root) returns its taskId", ownerThreadID: "thread-A", setup: func(t *testing.T) *service {
		t.Helper()
		threads := map[string]*threadstore.Thread{"thread-A": taskHandoffTestThread(t, "thread-A", "", "", map[string]any{taskConfigKeyID: "task-root"})}
		return &service{threadStore: &stubThreadStore{threadByID: threads}}
	}, want: "task-root"},
	{name: "two layers traverses to root", ownerThreadID: "thread-mid", setup: func(t *testing.T) *service {
		t.Helper()
		threads := map[string]*threadstore.Thread{
			"thread-mid":  taskHandoffTestThread(t, "thread-mid", "thread-root", "", map[string]any{taskConfigKeyID: "task-mid"}),
			"thread-root": taskHandoffTestThread(t, "thread-root", "", "", map[string]any{taskConfigKeyID: "task-root"}),
		}
		return &service{threadStore: &stubThreadStore{threadByID: threads}}
	}, want: "task-root"},
	{name: "depth limit 10 cuts off cyclic chain", ownerThreadID: "thread-1", setup: func(t *testing.T) *service {
		t.Helper()
		return &service{threadStore: &stubThreadStore{threadByID: cyclicTaskHandoffThreads(t)}}
	}, wantErr: true},
	{name: "root with no taskId fails fast", ownerThreadID: "thread-A", setup: func(t *testing.T) *service {
		t.Helper()
		threads := map[string]*threadstore.Thread{"thread-A": taskHandoffTestThread(t, "thread-A", "", "", map[string]any{})}
		return &service{threadStore: &stubThreadStore{threadByID: threads}}
	}, wantErr: true},
}

func TestResolveRootTaskId(t *testing.T) {
	t.Parallel()

	for _, tt := range resolveRootTaskIDCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.setup(t).resolveRootTaskId(context.Background(), tt.ownerThreadID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveRootTaskId() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRootTaskId() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveRootTaskId() = %q, want %q", got, tt.want)
			}
		})
	}
}

func cyclicTaskHandoffThreads(t *testing.T) map[string]*threadstore.Thread {
	t.Helper()

	threads := map[string]*threadstore.Thread{}
	for i := 1; i <= 12; i++ {
		curID := fmt.Sprintf("thread-%d", i)
		nextID := fmt.Sprintf("thread-%d", i+1)
		threads[curID] = taskHandoffTestThread(t, curID, nextID, "", map[string]any{taskConfigKeyID: "task-fake"})
	}
	return threads
}

func TestPrepareTaskHandoffStart_RootTaskId(t *testing.T) {
	t.Parallel()

	t.Run("root task without OwnerThreadID falls back to self taskId", runRootTaskWithoutOwnerThreadID)
	t.Run("sub-agent 1 layer: rootTaskId from owner taskId", runRootTaskSingleOwnerLayer)
	t.Run("sub-agent 2 layers: traverses to root taskId", runRootTaskTwoOwnerLayers)
	t.Run("explicit rootTaskId in Config wins", runExplicitRootTaskIDWins)
}

func runRootTaskWithoutOwnerThreadID(t *testing.T) {
	t.Parallel()
	svc := &service{threadStore: &stubThreadStore{}, sharedFiles: &stubSharedFileStore{}, bindingStore: &stubBindingStore{}}
	req := StartRequest{AgentType: "worker", Name: "Root Task"}
	if err := svc.prepareTaskHandoffStart(context.Background(), &req); err != nil {
		t.Fatalf("prepareTaskHandoffStart() error = %v", err)
	}
	taskID := firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake)
	rootID := firstConfigString(req.Config, taskConfigKeyRoot, taskConfigKeyRootSnake)
	if taskID == "" || rootID != taskID {
		t.Fatalf("rootTaskId = %q, want self taskId %q", rootID, taskID)
	}
}

func runRootTaskSingleOwnerLayer(t *testing.T) {
	t.Parallel()
	rootThread := taskHandoffTestThread(t, "thread-root", "", "", map[string]any{taskConfigKeyID: "task-root", taskConfigKeyTitle: "Root Task", taskConfigKeyHandoffFile: defaultTaskHandoffPath("task-root")})
	svc := &service{threadStore: &stubThreadStore{thread: rootThread}, sharedFiles: &stubSharedFileStore{}, bindingStore: &stubBindingStore{}}
	req := StartRequest{OwnerThreadID: "thread-root", AgentType: "reviewer", Name: "Reviewer"}
	assertPreparedRootTaskID(t, svc, &req, "task-root")
}

func runRootTaskTwoOwnerLayers(t *testing.T) {
	t.Parallel()
	threadByID := map[string]*threadstore.Thread{
		"thread-mid":  taskHandoffTestThread(t, "thread-mid", "thread-root", "", map[string]any{taskConfigKeyID: "task-mid", taskConfigKeyTitle: "Mid Task", taskConfigKeyHandoffFile: defaultTaskHandoffPath("task-mid")}),
		"thread-root": taskHandoffTestThread(t, "thread-root", "", "", map[string]any{taskConfigKeyID: "task-root", taskConfigKeyTitle: "Root Task", taskConfigKeyHandoffFile: defaultTaskHandoffPath("task-root")}),
	}
	svc := &service{threadStore: &stubThreadStore{threadByID: threadByID}, sharedFiles: &stubSharedFileStore{}, bindingStore: &stubBindingStore{}}
	req := StartRequest{OwnerThreadID: "thread-mid", AgentType: "deep", Name: "Deep Worker"}
	assertPreparedRootTaskID(t, svc, &req, "task-root")
}

func runExplicitRootTaskIDWins(t *testing.T) {
	t.Parallel()
	rootThread := taskHandoffTestThread(t, "thread-root", "", "", map[string]any{taskConfigKeyID: "task-root", taskConfigKeyTitle: "Root Task", taskConfigKeyHandoffFile: defaultTaskHandoffPath("task-root")})
	svc := &service{threadStore: &stubThreadStore{thread: rootThread}, sharedFiles: &stubSharedFileStore{}, bindingStore: &stubBindingStore{}}
	req := StartRequest{OwnerThreadID: "thread-root", AgentType: "reviewer", Name: "Reviewer", Config: map[string]any{taskConfigKeyRoot: "task-explicit-override"}}
	assertPreparedRootTaskID(t, svc, &req, "task-explicit-override")
}

func assertPreparedRootTaskID(t *testing.T, svc *service, req *StartRequest, want string) {
	t.Helper()

	if err := svc.prepareTaskHandoffStart(context.Background(), req); err != nil {
		t.Fatalf("prepareTaskHandoffStart() error = %v", err)
	}
	if got := firstConfigString(req.Config, taskConfigKeyRoot, taskConfigKeyRootSnake); got != want {
		t.Fatalf("rootTaskId = %q, want %q", got, want)
	}
}
