package uistate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestNewServiceDefersInitialThreadLoad(t *testing.T) {
	t.Parallel()

	threadErr := errors.New("schema not migrated")
	lister := &threadListerStub{err: threadErr}

	svc, _, err := NewService(nil, lister, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if lister.calls != 0 {
		t.Fatalf("List() calls during NewService = %d, want 0", lister.calls)
	}
	state, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if len(state.Threads) != 0 {
		t.Fatalf("initial Threads = %#v, want empty before lifecycle load", state.Threads)
	}
}

func TestLoadInitialStatePopulatesThreads(t *testing.T) {
	t.Parallel()

	lister := &threadListerStub{refs: []contract.ThreadRef{{
		ID:      "thread-1",
		Name:    "Demo",
		AgentID: "agent-1",
		Status:  "running",
	}}}
	svc, _, err := NewService(nil, lister, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if err := svc.loadInitialState(context.Background()); err != nil {
		t.Fatalf("loadInitialState() error = %v", err)
	}
	if lister.calls != 1 {
		t.Fatalf("List() calls = %d, want 1", lister.calls)
	}
	state, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if len(state.Threads) != 1 || state.Threads[0].ID != "thread-1" || state.Threads[0].LifecycleStatus != "running" {
		t.Fatalf("Threads = %#v, want loaded thread", state.Threads)
	}
}

func TestApplyTaskRuntimeToThreadRuntimeConfig_Pin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		threadID   string
		cfg        map[string]any
		runtimeMap map[string]map[string]any
		want       map[string]map[string]any
	}{
		{
			name:       "empty config leaves runtime map unchanged",
			threadID:   "thread-5",
			cfg:        map[string]any{},
			runtimeMap: map[string]map[string]any{"thread-5": {"taskId": "keep"}},
			want:       map[string]map[string]any{"thread-5": {"taskId": "keep"}},
		},
		{
			name:       "creates runtime entry from canonical keys",
			threadID:   "thread-6",
			cfg:        map[string]any{"taskId": "task-1", "taskTitle": "Title", "handoffFile": "handoff.md", "ownerThreadId": "thread-parent", "rootTaskId": "task-root"},
			runtimeMap: map[string]map[string]any{},
			want: map[string]map[string]any{
				"thread-6": {
					"taskId":        "task-1",
					"taskTitle":     "Title",
					"handoffFile":   "handoff.md",
					"ownerThreadId": "thread-parent",
					"rootTaskId":    "task-root",
				},
			},
		},
		{
			name:     "updates existing runtime entry from alias keys and skips blank values",
			threadID: "thread-7",
			cfg: map[string]any{
				"task_id":         " task-2 ",
				"task_title":      42,
				"handoff_file":    "  ",
				"owner_thread_id": " owner-thread ",
				"root_task_id":    " task-root-snake ",
			},
			runtimeMap: map[string]map[string]any{
				"thread-7": {
					"taskId":      "keep-task",
					"taskTitle":   "keep-title",
					"handoffFile": "keep-file",
					"stable":      true,
				},
			},
			want: map[string]map[string]any{
				"thread-7": {
					"taskId":        "task-2",
					"taskTitle":     "keep-title",
					"handoffFile":   "keep-file",
					"ownerThreadId": "owner-thread",
					"rootTaskId":    "task-root-snake",
					"stable":        true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			applyTaskRuntimeToThreadRuntimeConfig(tt.threadID, tt.cfg, tt.runtimeMap)

			if !reflect.DeepEqual(tt.runtimeMap, tt.want) {
				t.Fatalf("runtimeMap mismatch (-got +want):\n got: %#v\nwant: %#v", tt.runtimeMap, tt.want)
			}
		})
	}
}

type threadListerStub struct {
	refs  []contract.ThreadRef
	err   error
	calls int
}

func (s *threadListerStub) List(context.Context) ([]contract.ThreadRef, error) {
	s.calls++
	return s.refs, s.err
}

type runtimeConfigLookupStub struct {
	cfg       map[string]any
	err       error
	threadIDs []string
}

func (s *runtimeConfigLookupStub) ReadRuntimeConfig(_ context.Context, threadID string) (map[string]any, error) {
	s.threadIDs = append(s.threadIDs, threadID)
	return s.cfg, s.err
}

func (s *runtimeConfigLookupStub) ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error) {
	s.threadIDs = append(s.threadIDs, threadIDs...)
	if s.err != nil {
		return nil, s.err
	}
	result := make(map[string]map[string]any)
	for _, id := range threadIDs {
		result[id] = s.cfg
	}
	return result, nil
}
