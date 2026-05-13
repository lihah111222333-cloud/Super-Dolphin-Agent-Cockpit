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

func TestApplyTaskRuntimeToThreadRuntime_Pin(t *testing.T) {
	t.Parallel()

	readErr := errors.New("boom")

	tests := []struct {
		name       string
		svc        *service
		thread     ThreadSummary
		runtimeMap map[string]map[string]any
		want       map[string]map[string]any
		wantReads  []string
	}{
		{
			name:       "nil service returns without mutation",
			svc:        nil,
			thread:     ThreadSummary{ID: "thread-1"},
			runtimeMap: map[string]map[string]any{"thread-1": {"taskId": "keep"}},
			want:       map[string]map[string]any{"thread-1": {"taskId": "keep"}},
		},
		{
			name:       "nil runtime config reader returns without mutation",
			svc:        &service{},
			thread:     ThreadSummary{ID: "thread-2"},
			runtimeMap: map[string]map[string]any{"thread-2": {"taskId": "keep"}},
			want:       map[string]map[string]any{"thread-2": {"taskId": "keep"}},
		},
		{
			name:   "blank thread id skips runtime config lookup",
			svc:    &service{runtimeConfig: &runtimeConfigLookupStub{cfg: map[string]any{"taskId": "task-1"}}},
			thread: ThreadSummary{ID: "   "},
			runtimeMap: map[string]map[string]any{
				"thread-3": {"taskId": "keep"},
			},
			want: map[string]map[string]any{
				"thread-3": {"taskId": "keep"},
			},
		},
		{
			name:       "lookup error leaves runtime map unchanged",
			svc:        &service{runtimeConfig: &runtimeConfigLookupStub{cfg: map[string]any{"taskId": "task-1"}, err: readErr}},
			thread:     ThreadSummary{ID: "thread-4"},
			runtimeMap: map[string]map[string]any{"thread-4": {"taskId": "keep"}},
			want:       map[string]map[string]any{"thread-4": {"taskId": "keep"}},
			wantReads:  []string{"thread-4"},
		},
		{
			name:       "empty config leaves runtime map unchanged",
			svc:        &service{runtimeConfig: &runtimeConfigLookupStub{cfg: map[string]any{}}},
			thread:     ThreadSummary{ID: "thread-5"},
			runtimeMap: map[string]map[string]any{"thread-5": {"taskId": "keep"}},
			want:       map[string]map[string]any{"thread-5": {"taskId": "keep"}},
			wantReads:  []string{"thread-5"},
		},
		{
			name:       "creates runtime entry from canonical keys",
			svc:        &service{runtimeConfig: &runtimeConfigLookupStub{cfg: map[string]any{"taskId": "task-1", "taskTitle": "Title", "handoffFile": "handoff.md", "ownerThreadId": "thread-parent", "rootTaskId": "task-root"}}},
			thread:     ThreadSummary{ID: "thread-6"},
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
			wantReads: []string{"thread-6"},
		},
		{
			name: "updates existing runtime entry from alias keys and skips blank values",
			svc: &service{runtimeConfig: &runtimeConfigLookupStub{cfg: map[string]any{
				"task_id":         " task-2 ",
				"task_title":      42,
				"handoff_file":    "  ",
				"owner_thread_id": " owner-thread ",
				"root_task_id":    " task-root-snake ",
			}}},
			thread: ThreadSummary{ID: "thread-7"},
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
			wantReads: []string{"thread-7"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.svc.applyTaskRuntimeToThreadRuntime(context.Background(), tt.thread, tt.runtimeMap)

			if !reflect.DeepEqual(tt.runtimeMap, tt.want) {
				t.Fatalf("runtimeMap mismatch (-got +want):\n got: %#v\nwant: %#v", tt.runtimeMap, tt.want)
			}
			if tt.svc == nil || tt.svc.runtimeConfig == nil {
				return
			}
			stub, ok := tt.svc.runtimeConfig.(*runtimeConfigLookupStub)
			if !ok {
				t.Fatalf("runtimeConfig type = %T, want *runtimeConfigLookupStub", tt.svc.runtimeConfig)
			}
			if !reflect.DeepEqual(stub.threadIDs, tt.wantReads) {
				t.Fatalf("ReadRuntimeConfig() threadIDs = %#v, want %#v", stub.threadIDs, tt.wantReads)
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
