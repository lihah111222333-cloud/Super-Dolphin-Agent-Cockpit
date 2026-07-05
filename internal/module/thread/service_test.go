package thread

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// TestListThreadsUsesLimitAndCursor 验证列表页读取必须把 limit/cursor 下推到 store。
func TestListThreadsUsesLimitAndCursor(t *testing.T) {
	t.Parallel()

	store := &pageAwareThreadStore{
		page: contract.ThreadListPage{
			Threads: []contract.ThreadListRecord{
				{ThreadID: "thread-next", AgentID: "agent-next", Status: statusCreated, CreatedAt: 41},
			},
			HasMore:             true,
			NextCursorCreatedAt: 41,
			NextCursorThreadID:  "thread-next",
		},
	}
	svc := &service{threadStore: store}

	got, err := svc.ListPage(context.Background(), ListPageRequest{
		Limit:           500,
		CursorCreatedAt: 42,
		CursorThreadID:  "thread-cursor",
	})

	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if store.listAllCalled {
		t.Fatal("ListPage() called ListAll; want bounded store page query")
	}
	requireThreadPageParams(t, store.pageParams, 200, 42, "thread-cursor")
	requireThreadPageResult(t, got, "thread-next", 41)
}

// TestLoadedThreadsUsesSQLFilter 验证 loaded 线程页必须使用 store 的 SQL 过滤入口。
func TestLoadedThreadsUsesSQLFilter(t *testing.T) {
	t.Parallel()

	store := &pageAwareThreadStore{
		loadedPage: contract.ThreadListPage{
			Threads: []contract.ThreadListRecord{
				{ThreadID: "thread-loaded", AgentID: "agent-loaded", Status: statusCreated},
			},
		},
	}
	svc := &service{threadStore: store}

	got, err := svc.ListLoadedPage(context.Background(), ListPageRequest{Limit: 25})

	if err != nil {
		t.Fatalf("ListLoadedPage() error = %v", err)
	}
	if store.listAllCalled {
		t.Fatal("ListLoadedPage() called ListAll; want SQL status filter")
	}
	requireThreadPageParams(t, store.loadedPageParams, 25, 0, "")
	requireThreadListIDs(t, got.Threads, "thread-loaded")
}

// TestLegacyListUsesHardCap 验证旧 no-arg List 兼容入口也只能读取有限页。
func TestLegacyListUsesHardCap(t *testing.T) {
	t.Parallel()

	store := &pageAwareThreadStore{
		page: contract.ThreadListPage{
			Threads: []contract.ThreadListRecord{
				{ThreadID: "thread-legacy", Status: statusCreated},
			},
		},
	}
	svc := &service{threadStore: store}

	got, err := svc.List(context.Background())

	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if store.listAllCalled {
		t.Fatal("List() called ListAll; want hard-capped compatibility page")
	}
	requireThreadPageParams(t, store.pageParams, 200, 0, "")
	requireThreadListIDs(t, got, "thread-legacy")
}

// requireThreadPageParams 断言 service 传给 store 的分页参数完整且已按上限裁剪。
func requireThreadPageParams(t *testing.T, got contract.ThreadListPageParams, wantLimit int, wantCreatedAt int64, wantThreadID string) {
	t.Helper()
	if got.Limit != wantLimit {
		t.Fatalf("page limit = %d, want %d", got.Limit, wantLimit)
	}
	if got.CursorCreatedAt != wantCreatedAt {
		t.Fatalf("cursor created_at = %d, want %d", got.CursorCreatedAt, wantCreatedAt)
	}
	if got.CursorThreadID != wantThreadID {
		t.Fatalf("cursor thread_id = %q, want %q", got.CursorThreadID, wantThreadID)
	}
}

// requireThreadPageResult 断言 service 把 store page 元数据原样投影到返回值。
func requireThreadPageResult(t *testing.T, got ListPageResult, wantID string, wantCreatedAt int64) {
	t.Helper()
	requireThreadListIDs(t, got.Threads, wantID)
	if !got.HasMore {
		t.Fatalf("HasMore = false, want true")
	}
	if got.NextCursorCreatedAt != wantCreatedAt {
		t.Fatalf("NextCursorCreatedAt = %d, want %d", got.NextCursorCreatedAt, wantCreatedAt)
	}
	if got.NextCursorThreadID != wantID {
		t.Fatalf("NextCursorThreadID = %q, want %q", got.NextCursorThreadID, wantID)
	}
}

// requireThreadListIDs 断言线程列表只包含一个指定 id。
func requireThreadListIDs(t *testing.T, got []Ref, wantID string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("threads = %#v, want one thread", got)
	}
	if got[0].ID != wantID {
		t.Fatalf("thread id = %q, want %q", got[0].ID, wantID)
	}
}

// pageAwareThreadStore 记录分页调用，任何 ListAll 调用都会让测试失败。
type pageAwareThreadStore struct {
	*stubThreadStore
	page             contract.ThreadListPage
	loadedPage       contract.ThreadListPage
	pageParams       contract.ThreadListPageParams
	loadedPageParams contract.ThreadListPageParams
	listAllCalled    bool
}

// ListAll 记录 legacy 全量读取调用，分页路径不允许触发它。
func (s *pageAwareThreadStore) ListAll(context.Context) ([]threadstore.Thread, error) {
	s.listAllCalled = true
	return nil, errors.New("ListAll should not be called by paged thread readers")
}

// ListPage 记录普通线程分页参数并返回预设页。
func (s *pageAwareThreadStore) ListPage(_ context.Context, params contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	s.pageParams = params
	return s.page, nil
}

// ListLoadedPage 记录 loaded 线程分页参数并返回预设页。
func (s *pageAwareThreadStore) ListLoadedPage(_ context.Context, params contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	s.loadedPageParams = params
	return s.loadedPage, nil
}

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

func TestDeletePinPendingLaunchHardDelete(t *testing.T) {
	t.Parallel()

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

	err := svc.Delete(context.Background(), "thread-pending")

	assertDeleteOK(t, err)
	assertDeletedIDs(t, store, "thread-pending")
	if _, loaded := svc.pendingLaunchMu.Load("thread-pending"); loaded {
		t.Fatal("pendingLaunchMu still contains deleted pending thread")
	}
	if store.thread == nil || store.thread.Status != "deleted" || store.thread.Prompt != "deleted_pending_launch" {
		t.Fatalf("stopped event snapshot = %#v, want status=deleted reason=deleted_pending_launch", store.thread)
	}
}

func TestDeleteFailsWhenBindingStoreMissing(t *testing.T) {
	t.Parallel()

	store := &pinDeleteThreadStore{stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
		ThreadID: "thread-active",
		Status:   statusCreated,
	}}}
	svc := &service{threadStore: store}

	err := svc.Delete(context.Background(), "thread-active")

	if err == nil || !strings.Contains(err.Error(), "binding store is not configured") {
		t.Fatalf("Delete() error = %v, want binding store not configured", err)
	}
	if len(store.deletedIDs) != 0 {
		t.Fatalf("deletedIDs = %v, want none", store.deletedIDs)
	}
}

func TestDeletePendingLaunchStillHandlesMissingBindingRecord(t *testing.T) {
	t.Parallel()

	store := &pinDeleteThreadStore{
		stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
			ThreadID:      "thread-pending-missing-binding",
			Status:        statusCreated,
			PendingLaunch: true,
		}},
	}
	svc := &service{
		threadStore:  store,
		bindingStore: &stubThreadBindingStore{},
	}

	err := svc.Delete(context.Background(), "thread-pending-missing-binding")

	assertDeleteOK(t, err)
	assertDeletedIDs(t, store, "thread-pending-missing-binding")
}

func TestSetArchivedFailsWhenBindingStoreMissing(t *testing.T) {
	t.Parallel()

	t.Run("binding store missing", func(t *testing.T) {
		t.Parallel()

		svc := &service{threadStore: &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1"}}}

		err := svc.setBindingArchived(context.Background(), "thread-1", true)

		if err == nil || !strings.Contains(err.Error(), "binding store is not configured") {
			t.Fatalf("setBindingArchived() error = %v, want binding store not configured", err)
		}
	})

	t.Run("thread store missing", func(t *testing.T) {
		t.Parallel()

		svc := &service{bindingStore: &stubThreadBindingStore{binding: &bindingstore.Binding{AgentID: "thread-1"}}}

		err := svc.setBindingArchived(context.Background(), "thread-1", true)

		if err == nil || !strings.Contains(err.Error(), "thread store is not configured") {
			t.Fatalf("setBindingArchived() error = %v, want thread store not configured", err)
		}
	})
}

func TestDeletePinActiveThreadSoftDelete(t *testing.T) {
	t.Parallel()

	calls := []string{}
	svc := newPinDeleteManagedService("thread-1", "agent-1", "provider-thread-1", &calls, nil, nil)

	err := svc.Delete(context.Background(), "thread-1")

	assertDeleteOK(t, err)
	assertDeletedIDs(t, svc.threadStore.(*pinDeleteThreadStore), "thread-1")
	orch := svc.orchestration.(*stubThreadOrchestration)
	if orch.stoppedAgentID != "agent-1" {
		t.Fatalf("stopped agent = %q, want agent-1", orch.stoppedAgentID)
	}
	bindingStore := svc.bindingStore.(*pinDeleteBindingStore)
	if len(bindingStore.deletedAgentIDs) != 1 || bindingStore.deletedAgentIDs[0] != "agent-1" {
		t.Fatalf("deletedAgentIDs = %v, want [agent-1]", bindingStore.deletedAgentIDs)
	}
	callLog := deleteCallLog(t, svc)
	assertCallBefore(t, callLog, "agent_stop:agent-1", "binding_delete:agent-1")
	assertCallBefore(t, callLog, "agent_stop:agent-1", "thread_delete:thread-1")
	assertCallPresent(t, callLog, "turn_cleanup:thread-1:thread_deleted")
}

func TestDeletePinMissingThreadFallsThrough(t *testing.T) {
	t.Parallel()

	store := &pinDeleteThreadStore{stubThreadStore: &stubThreadStore{}}
	svc := &service{threadStore: store}

	err := svc.Delete(context.Background(), "thread-missing")

	if err == nil || !strings.Contains(err.Error(), "binding store is not configured") {
		t.Fatalf("Delete() error = %v, want binding store not configured", err)
	}
	if len(store.deletedIDs) != 0 {
		t.Fatalf("deletedIDs = %v, want none", store.deletedIDs)
	}
}

func TestDeletePinPreservesUnmanagedScratchpad(t *testing.T) {
	t.Parallel()

	external := t.TempDir()
	raw := mustStoredThreadConfigRaw(t, storedThreadConfig{
		Runtime: map[string]any{"scratchpadDir": external},
	})
	svc := &service{
		threadStore: &pinDeleteThreadStore{stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
			ThreadID:       "thread-external",
			AgentID:        "agent-external",
			ConfigOverride: raw,
		}}},
		bindingStore: &stubThreadBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-external",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-external",
			CodexThreadID:    "thread-external",
		}},
		emitStopped: func(threaddto.Stopped) {
			if _, err := os.Stat(external); err != nil {
				t.Fatalf("external scratchpad removed: %v", err)
			}
		},
	}

	err := svc.Delete(context.Background(), "thread-external")

	assertDeleteOK(t, err)
}

func TestDeletePinMissingManagedAgentDoesNotBlockDelete(t *testing.T) {
	t.Parallel()

	calls := []string{}
	svc := newPinDeleteManagedService("thread-404-agent", "agent-404", "provider-thread-404", &calls, contract.ErrAgentNotFound, nil)

	err := svc.Delete(context.Background(), "thread-404-agent")

	assertDeleteOK(t, err)
	orch := svc.orchestration.(*stubThreadOrchestration)
	if len(orch.stopCalls) != 1 || orch.stopCalls[0] != "agent-404" {
		t.Fatalf("stopCalls = %v, want [agent-404]", orch.stopCalls)
	}
	assertDeletedIDs(t, svc.threadStore.(*pinDeleteThreadStore), "thread-404-agent")
}

func TestDeletePinBindingDeleteFailureAbortsBeforeThreadDelete(t *testing.T) {
	t.Parallel()

	calls := []string{}
	svc := newPinDeleteManagedService("thread-bind-fail", "agent-bind-fail", "provider-thread-bind-fail", &calls, nil, errors.New("binding delete failed"))

	err := svc.Delete(context.Background(), "thread-bind-fail")

	if err == nil || err.Error() != "binding delete failed" {
		t.Fatalf("Delete() error = %v, want binding delete failed", err)
	}
	store := svc.threadStore.(*pinDeleteThreadStore)
	if len(store.deletedIDs) != 0 {
		t.Fatalf("deletedIDs = %v, want none", store.deletedIDs)
	}
	callLog := deleteCallLog(t, svc)
	assertCallPresent(t, callLog, "agent_stop:agent-bind-fail")
	assertCallPresent(t, callLog, "binding_delete:agent-bind-fail")
	if callIndex(callLog, "thread_delete:thread-bind-fail") != -1 {
		t.Fatalf("call order = %v, thread delete should not run after binding failure", callLog)
	}
}

func newPinDeleteManagedService(threadID, agentID, providerThreadID string, calls *[]string, stopErr, bindingErr error) *service {
	return &service{
		threadStore: &pinDeleteThreadStore{stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
			ThreadID: threadID,
			AgentID:  agentID,
			Status:   statusCreated,
		}}, calls: calls},
		bindingStore: &pinDeleteBindingStore{
			stubThreadBindingStore: &stubThreadBindingStore{
				binding: &bindingstore.Binding{
					AgentID:          agentID,
					Provider:         "codex",
					ProviderThreadID: providerThreadID,
					CodexThreadID:    threadID,
				},
				calls: calls,
			},
			deleteErr: bindingErr,
		},
		sessions:      &stubThreadSessions{agentID: agentID, session: &stubThreadSession{threadID: threadID, calls: calls}},
		turns:         &stubTurnService{calls: calls},
		orchestration: &stubThreadOrchestration{calls: calls, stopErr: stopErr},
	}
}

func assertDeleteOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func assertDeletedIDs(t *testing.T, store *pinDeleteThreadStore, want string) {
	t.Helper()
	if len(store.deletedIDs) != 1 || store.deletedIDs[0] != want {
		t.Fatalf("deletedIDs = %v, want [%s]", store.deletedIDs, want)
	}
}

func deleteCallLog(t *testing.T, svc *service) []string {
	t.Helper()
	orch := svc.orchestration.(*stubThreadOrchestration)
	return append([]string(nil), (*orch.calls)...)
}

func assertCallBefore(t *testing.T, calls []string, before, after string) {
	t.Helper()
	if callIndex(calls, before) > callIndex(calls, after) {
		t.Fatalf("call order = %v, want %s before %s", calls, before, after)
	}
}

func assertCallPresent(t *testing.T, calls []string, want string) {
	t.Helper()
	if callIndex(calls, want) == -1 {
		t.Fatalf("call order = %v, want %s", calls, want)
	}
}
