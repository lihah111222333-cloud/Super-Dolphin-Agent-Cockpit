package thread

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type pinDeleteThreadStore struct {
	*stubThreadStore
	deletedIDs []string
	deleteErr  error
	calls      *[]string
}

func (s *pinDeleteThreadStore) DeleteByThreadID(_ context.Context, threadID string) error {
	s.deletedIDs = append(s.deletedIDs, threadID)
	recordCall(s.calls, "thread_delete:"+threadID)
	return s.deleteErr
}

type pinDeleteBindingStore struct {
	*stubThreadBindingStore
	deleteErr error
}

func (s *pinDeleteBindingStore) DeleteByAgentID(_ context.Context, agentID string) error {
	s.deletedAgentIDs = append(s.deletedAgentIDs, agentID)
	recordCall(s.calls, "binding_delete:"+agentID)
	return s.deleteErr
}

func TestDelete_Pin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		threadID string
		setup    func(t *testing.T) *service
		assert   func(t *testing.T, svc *service, err error)
	}{
		{
			name:     "pending launch hard delete cleans mutex and emits pending reason",
			threadID: "thread-pending",
			setup: func(t *testing.T) *service {
				t.Helper()
				store := &pinDeleteThreadStore{
					stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
						ThreadID:      "thread-pending",
						Status:        statusCreated,
						PendingLaunch: true,
					}},
				}
				svc := &service{threadStore: store}
				_ = svc.acquirePendingLaunchLock("thread-pending")
				svc.emitStopped = func(evt threaddto.Stopped) {
					store.thread = &threadstore.Thread{
						ThreadID: evt.ThreadID,
						AgentID:  evt.AgentID,
						Status:   evt.Status,
						Prompt:   evt.Reason,
					}
				}
				return svc
			},
			assert: func(t *testing.T, svc *service, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Delete() error = %v", err)
				}
				store := svc.threadStore.(*pinDeleteThreadStore)
				if len(store.deletedIDs) != 1 || store.deletedIDs[0] != "thread-pending" {
					t.Fatalf("deletedIDs = %v, want [thread-pending]", store.deletedIDs)
				}
				if _, loaded := svc.pendingLaunchMu.Load("thread-pending"); loaded {
					t.Fatal("pendingLaunchMu still contains deleted pending thread")
				}
				if store.thread == nil || store.thread.Status != "deleted" || store.thread.Prompt != "deleted_pending_launch" {
					t.Fatalf("stopped event snapshot = %#v, want status=deleted reason=deleted_pending_launch", store.thread)
				}
			},
		},
		{
			name:     "active thread soft delete stops runtime before deleting durable state",
			threadID: "thread-1",
			setup: func(t *testing.T) *service {
				t.Helper()
				calls := []string{}
				store := &pinDeleteThreadStore{
					stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
						ThreadID: "thread-1",
						AgentID:  "agent-1",
						Status:   statusCreated,
					}},
					calls: &calls,
				}
				return &service{
					threadStore: store,
					bindingStore: &pinDeleteBindingStore{stubThreadBindingStore: &stubThreadBindingStore{
						binding: &bindingstore.Binding{
							AgentID:          "agent-1",
							Provider:         "codex",
							ProviderThreadID: "provider-thread-1",
							CodexThreadID:    "thread-1",
						},
						calls: &calls,
					}},
					sessions:      &stubThreadSessions{agentID: "agent-1", session: &stubThreadSession{threadID: "thread-1", calls: &calls}},
					turns:         &stubTurnService{calls: &calls},
					orchestration: &stubThreadOrchestration{calls: &calls},
				}
			},
			assert: func(t *testing.T, svc *service, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Delete() error = %v", err)
				}
				store := svc.threadStore.(*pinDeleteThreadStore)
				if len(store.deletedIDs) != 1 || store.deletedIDs[0] != "thread-1" {
					t.Fatalf("deletedIDs = %v, want [thread-1]", store.deletedIDs)
				}
				orch := svc.orchestration.(*stubThreadOrchestration)
				if orch.stoppedAgentID != "agent-1" {
					t.Fatalf("stopped agent = %q, want agent-1", orch.stoppedAgentID)
				}
				bindingStore := svc.bindingStore.(*pinDeleteBindingStore)
				if len(bindingStore.deletedAgentIDs) != 1 || bindingStore.deletedAgentIDs[0] != "agent-1" {
					t.Fatalf("deletedAgentIDs = %v, want [agent-1]", bindingStore.deletedAgentIDs)
				}
				calls := append([]string(nil), (*orch.calls)...)
				if callIndex(calls, "agent_stop:agent-1") > callIndex(calls, "binding_delete:agent-1") {
					t.Fatalf("call order = %v, want agent stop before binding delete", calls)
				}
				if callIndex(calls, "agent_stop:agent-1") > callIndex(calls, "thread_delete:thread-1") {
					t.Fatalf("call order = %v, want agent stop before thread delete", calls)
				}
				if callIndex(calls, "turn_cleanup:thread-1:thread_deleted") == -1 {
					t.Fatalf("call order = %v, want thread cleanup", calls)
				}
			},
		},
		{
			name:     "missing thread still falls through to thread store delete",
			threadID: "thread-missing",
			setup: func(t *testing.T) *service {
				t.Helper()
				return &service{
					threadStore: &pinDeleteThreadStore{stubThreadStore: &stubThreadStore{}},
				}
			},
			assert: func(t *testing.T, svc *service, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Delete() error = %v", err)
				}
				store := svc.threadStore.(*pinDeleteThreadStore)
				if len(store.deletedIDs) != 1 || store.deletedIDs[0] != "thread-missing" {
					t.Fatalf("deletedIDs = %v, want [thread-missing]", store.deletedIDs)
				}
			},
		},
		{
			name:     "unmanaged scratchpad path is preserved by cleanup guard",
			threadID: "thread-external",
			setup: func(t *testing.T) *service {
				t.Helper()
				external := t.TempDir()
				raw := mustStoredThreadConfigRaw(t, storedThreadConfig{
					Runtime: map[string]any{"scratchpadDir": external},
				})
				return &service{
					threadStore: &pinDeleteThreadStore{stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
						ThreadID:       "thread-external",
						ConfigOverride: raw,
					}}},
					emitStopped: func(threaddto.Stopped) {
						if _, err := os.Stat(external); err != nil {
							t.Fatalf("external scratchpad removed: %v", err)
						}
					},
				}
			},
			assert: func(t *testing.T, svc *service, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Delete() error = %v", err)
				}
			},
		},
		{
			name:     "missing managed agent does not block delete",
			threadID: "thread-404-agent",
			setup: func(t *testing.T) *service {
				t.Helper()
				calls := []string{}
				return &service{
					threadStore: &pinDeleteThreadStore{stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
						ThreadID: "thread-404-agent",
						AgentID:  "agent-404",
						Status:   statusCreated,
					}}, calls: &calls},
					bindingStore: &pinDeleteBindingStore{stubThreadBindingStore: &stubThreadBindingStore{
						binding: &bindingstore.Binding{
							AgentID:          "agent-404",
							Provider:         "codex",
							ProviderThreadID: "provider-thread-404",
							CodexThreadID:    "thread-404-agent",
						},
						calls: &calls,
					}},
					sessions:      &stubThreadSessions{agentID: "agent-404", session: &stubThreadSession{threadID: "thread-404-agent", calls: &calls}},
					turns:         &stubTurnService{calls: &calls},
					orchestration: &stubThreadOrchestration{calls: &calls, stopErr: contract.ErrAgentNotFound},
				}
			},
			assert: func(t *testing.T, svc *service, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Delete() error = %v", err)
				}
				orch := svc.orchestration.(*stubThreadOrchestration)
				if len(orch.stopCalls) != 1 || orch.stopCalls[0] != "agent-404" {
					t.Fatalf("stopCalls = %v, want [agent-404]", orch.stopCalls)
				}
				store := svc.threadStore.(*pinDeleteThreadStore)
				if len(store.deletedIDs) != 1 || store.deletedIDs[0] != "thread-404-agent" {
					t.Fatalf("deletedIDs = %v, want [thread-404-agent]", store.deletedIDs)
				}
			},
		},
		{
			name:     "binding delete failure aborts before thread delete",
			threadID: "thread-bind-fail",
			setup: func(t *testing.T) *service {
				t.Helper()
				calls := []string{}
				return &service{
					threadStore: &pinDeleteThreadStore{stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
						ThreadID: "thread-bind-fail",
						AgentID:  "agent-bind-fail",
						Status:   statusCreated,
					}}, calls: &calls},
					bindingStore: &pinDeleteBindingStore{
						stubThreadBindingStore: &stubThreadBindingStore{
							binding: &bindingstore.Binding{
								AgentID:          "agent-bind-fail",
								Provider:         "codex",
								ProviderThreadID: "provider-thread-bind-fail",
								CodexThreadID:    "thread-bind-fail",
							},
							calls: &calls,
						},
						deleteErr: errors.New("binding delete failed"),
					},
					sessions:      &stubThreadSessions{agentID: "agent-bind-fail", session: &stubThreadSession{threadID: "thread-bind-fail", calls: &calls}},
					turns:         &stubTurnService{calls: &calls},
					orchestration: &stubThreadOrchestration{calls: &calls},
				}
			},
			assert: func(t *testing.T, svc *service, err error) {
				t.Helper()
				if err == nil || err.Error() != "binding delete failed" {
					t.Fatalf("Delete() error = %v, want binding delete failed", err)
				}
				store := svc.threadStore.(*pinDeleteThreadStore)
				if len(store.deletedIDs) != 0 {
					t.Fatalf("deletedIDs = %v, want none", store.deletedIDs)
				}
				calls := append([]string(nil), (*svc.orchestration.(*stubThreadOrchestration).calls)...)
				if callIndex(calls, "agent_stop:agent-bind-fail") == -1 {
					t.Fatalf("call order = %v, want agent stop attempt", calls)
				}
				if callIndex(calls, "binding_delete:agent-bind-fail") == -1 {
					t.Fatalf("call order = %v, want binding delete attempt", calls)
				}
				if callIndex(calls, "thread_delete:thread-bind-fail") != -1 {
					t.Fatalf("call order = %v, thread delete should not run after binding failure", calls)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := tt.setup(t)
			err := svc.Delete(context.Background(), tt.threadID)
			tt.assert(t, svc, err)
		})
	}
}
