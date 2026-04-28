package uistate

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

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
			svc:        &service{runtimeConfig: &runtimeConfigLookupStub{cfg: map[string]any{"taskId": "task-1", "taskTitle": "Title", "handoffFile": "handoff.md", "ownerThreadId": "thread-parent"}}},
			thread:     ThreadSummary{ID: "thread-6"},
			runtimeMap: map[string]map[string]any{},
			want: map[string]map[string]any{
				"thread-6": {
					"taskId":        "task-1",
					"taskTitle":     "Title",
					"handoffFile":   "handoff.md",
					"ownerThreadId": "thread-parent",
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

type runtimeConfigLookupStub struct {
	cfg       map[string]any
	err       error
	threadIDs []string
}

func (s *runtimeConfigLookupStub) ReadRuntimeConfig(_ context.Context, threadID string) (map[string]any, error) {
	s.threadIDs = append(s.threadIDs, threadID)
	return s.cfg, s.err
}
