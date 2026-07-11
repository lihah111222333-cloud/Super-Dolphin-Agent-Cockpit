package uistate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
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

func TestGetStateAndSidebarFilterStaleActiveThreadPreferences(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.state.Threads = []ThreadSummary{{ID: "thread-live"}, {ID: "thread-cmd"}}
	mustSetThreadPreference(t, svc, preferenceActiveThreadID, "thread-live")
	mustSetThreadPreference(t, svc, preferenceActiveCmdThreadID, "thread-cmd")
	requireActiveSelections(t, svc, "live selections", "thread-live", "thread-cmd")

	mustSetThreadPreference(t, svc, preferenceActiveThreadID, "thread-stale")
	mustSetThreadPreference(t, svc, preferenceActiveCmdThreadID, "thread-stale-cmd")
	requireActiveSelections(t, svc, "stale selections", "", "")
}

func requireActiveSelections(t *testing.T, svc *service, label, wantActive, wantCmd string) {
	t.Helper()
	state := mustGetUIState(t, svc, context.Background(), "GetState() "+label)
	sidebar := mustGetSidebar(t, svc, "GetSidebar() "+label)
	assertActiveSelections(t, "GetState() "+label, state.ActiveThreadID, state.ActiveCmdThreadID, wantActive, wantCmd)
	assertActiveSelections(t, "GetSidebar() "+label, sidebar.ActiveThreadID, sidebar.ActiveCmdThreadID, wantActive, wantCmd)
}

func assertActiveSelections(t *testing.T, label, gotActive, gotCmd, wantActive, wantCmd string) {
	t.Helper()
	if gotActive != wantActive || gotCmd != wantCmd {
		t.Fatalf("%s active threads = %q/%q, want %q/%q", label, gotActive, gotCmd, wantActive, wantCmd)
	}
}

func TestApplyTaskRuntimeToThreadRuntimeConfigDoesNotExposeLegacyTaskMetadata(t *testing.T) {
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
			name:       "does not create runtime entry from legacy task keys",
			threadID:   "thread-6",
			cfg:        map[string]any{"taskId": "task-1", "taskTitle": "Title", "handoffFile": "handoff.md", "ownerThreadId": "thread-parent", "rootTaskId": "task-root"},
			runtimeMap: map[string]map[string]any{},
			want:       map[string]map[string]any{},
		},
		{
			name:     "keeps existing runtime entry unchanged when legacy task aliases are present",
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
					"taskId":      "keep-task",
					"taskTitle":   "keep-title",
					"handoffFile": "keep-file",
					"stable":      true,
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

func TestEnrichFromDBSkipsPerThreadRuntimeFallbackAfterBatchError(t *testing.T) {
	t.Parallel()

	lookup := &runtimeConfigLookupStub{err: errors.New("batch config unavailable")}
	svc, _, err := NewService(nil, nil, nil, nil, nil, lookup)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	svc.enrichFromDB(context.Background(), nil, []ThreadSummary{
		{ID: "thread-1"},
		{ID: " thread-2 "},
		{ID: " "},
	}, map[string]map[string]any{})

	wantBatchIDs := []string{"thread-1", "thread-2"}
	if len(lookup.batchThreadIDs) != 1 || !reflect.DeepEqual(lookup.batchThreadIDs[0], wantBatchIDs) {
		t.Fatalf("ReadRuntimeConfigs IDs = %#v, want one batch %#v", lookup.batchThreadIDs, wantBatchIDs)
	}
	if len(lookup.singleThreadIDs) != 0 {
		t.Fatalf("ReadRuntimeConfig fallback calls = %#v, want none after batch error", lookup.singleThreadIDs)
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

type agentListStub struct {
	items []contract.AgentSnapshot
	err   error
	calls int
}

func (s *agentListStub) ListAgents(context.Context) ([]contract.AgentSnapshot, error) {
	s.calls++
	return s.items, s.err
}

type runtimeConfigLookupStub struct {
	cfg             map[string]any
	err             error
	threadIDs       []string
	singleThreadIDs []string
	batchThreadIDs  [][]string
}

func (s *runtimeConfigLookupStub) ReadRuntimeConfig(_ context.Context, threadID string) (map[string]any, error) {
	s.threadIDs = append(s.threadIDs, threadID)
	s.singleThreadIDs = append(s.singleThreadIDs, threadID)
	return s.cfg, s.err
}

func (s *runtimeConfigLookupStub) ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error) {
	s.threadIDs = append(s.threadIDs, threadIDs...)
	s.batchThreadIDs = append(s.batchThreadIDs, append([]string(nil), threadIDs...))
	if s.err != nil {
		return nil, s.err
	}
	result := make(map[string]map[string]any)
	for _, id := range threadIDs {
		result[id] = s.cfg
	}
	return result, nil
}
