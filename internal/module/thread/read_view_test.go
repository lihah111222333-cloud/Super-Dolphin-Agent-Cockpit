package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

func TestReadThreadHistoryUsesSessionThreadList(t *testing.T) {
	t.Parallel()

	svc, ok := NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Prompt: "demo"}),
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}),
		&historyTestSessionProvider{sessions: map[string]contract.Session{
			"agent-1": &historyTestSession{
				threadID: "provider-thread-1",
				threads: []dto.ThreadRef{
					{ID: "provider-thread-1"},
					{ID: "provider-thread-2"},
				},
			},
		}},
		nil,
		nil,
		nil,
		nil,
	).(*service)
	if !ok {
		t.Fatal("NewService() type assertion failed")
	}

	got, err := svc.ReadThreadHistory(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("ReadThreadHistory() error = %v", err)
	}
	want := &ReadHistoryResult{
		History: []ReadHistoryThread{
			{ThreadID: "provider-thread-1"},
			{ThreadID: "provider-thread-2"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadThreadHistory() = %#v, want %#v", got, want)
	}
}

func TestReadMessagesReadsPersistedHistoryWithoutLiveSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rolloutPath := filepath.Join(dir, "rollout-demo-thread.jsonl")
	raw := "" +
		"{\"timestamp\":\"2026-03-21T01:02:03Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"hello\"}]}}\n" +
		"{\"timestamp\":\"2026-03-21T01:03:03Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"world\"}]}}\n"
	if err := os.WriteFile(rolloutPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	svc := NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Prompt: "demo"}),
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
			RolloutPath:      rolloutPath,
		}),
		&historyTestSessionProvider{},
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.ReadMessages(context.Background(), "thread-1", 10, "")
	if err != nil {
		t.Fatalf("ReadMessages() error = %v", err)
	}
	if got.Total != 2 || len(got.Messages) != 2 {
		t.Fatalf("ReadMessages() = %#v, want two persisted messages", got)
	}
	if got.Messages[0].Role != "assistant" || got.Messages[0].Content != "world" {
		t.Fatalf("first page message = %#v, want assistant world", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "hello" {
		t.Fatalf("second page message = %#v, want user hello", got.Messages[1])
	}
}

func TestReadMessagesPersistedPagesKeepStableIDsAcrossBeforeCursor(t *testing.T) {
	t.Parallel()

	svc := newPersistedMessagesPageService(t, []string{"one", "two", "three", "four"})
	first, err := svc.ReadMessages(context.Background(), "thread-1", 2, "")
	if err != nil {
		t.Fatalf("ReadMessages(first) error = %v", err)
	}
	if !first.HasMore || first.NextBefore == "" {
		t.Fatalf("first page metadata = hasMore:%v nextBefore:%q, want cursor", first.HasMore, first.NextBefore)
	}
	second, err := svc.ReadMessages(context.Background(), "thread-1", 2, first.NextBefore)
	if err != nil {
		t.Fatalf("ReadMessages(second) error = %v", err)
	}
	requireReadMessagesContents(t, first.Messages, []string{"four", "three"})
	requireReadMessagesContents(t, second.Messages, []string{"two", "one"})
	requireReadMessagesIDsDoNotOverlap(t, first.Messages, second.Messages)
}

func newPersistedMessagesPageService(t *testing.T, contents []string) Service {
	t.Helper()
	dir := t.TempDir()
	rolloutPath := filepath.Join(dir, "rollout-demo-thread.jsonl")
	raw := ""
	for _, content := range contents {
		raw += "{\"timestamp\":\"2026-03-21T01:02:03Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"" + content + "\"}]}}\n"
	}
	if err := os.WriteFile(rolloutPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Prompt: "demo"}),
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
			RolloutPath:      rolloutPath,
		}),
		&historyTestSessionProvider{},
		nil,
		nil,
		nil,
		nil,
	)
}

func requireReadMessagesContents(t *testing.T, messages []dto.Message, want []string) {
	t.Helper()
	got := make([]string, 0, len(messages))
	for _, msg := range messages {
		got = append(got, msg.Content)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("message contents = %#v, want %#v", got, want)
	}
}

func requireReadMessagesIDsDoNotOverlap(t *testing.T, first, second []dto.Message) {
	t.Helper()
	seen := make(map[int64]string, len(first))
	for _, msg := range first {
		if msg.ID == 0 {
			t.Fatalf("first page message %q has zero ID", msg.Content)
		}
		seen[msg.ID] = msg.Content
	}
	for _, msg := range second {
		if msg.ID == 0 {
			t.Fatalf("second page message %q has zero ID", msg.Content)
		}
		if previous, ok := seen[msg.ID]; ok {
			t.Fatalf("message ID %d reused by %q and %q", msg.ID, previous, msg.Content)
		}
	}
}

func TestReadMessagesFailsFastWithoutSessionOrPersistedHistory(t *testing.T) {
	t.Parallel()

	svc := NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Prompt: "demo"}),
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:       "agent-1",
			Provider:      "codex",
			CodexThreadID: "thread-1",
			CodexHome:     t.TempDir(),
		}),
		&historyTestSessionProvider{},
		nil,
		nil,
		nil,
		nil,
	)

	_, err := svc.ReadMessages(context.Background(), "thread-1", 10, "")
	if !errors.Is(err, contract.ErrSessionNotFound) {
		t.Fatalf("ReadMessages() error = %v, want missing session error", err)
	}
}

func TestReadMessagesReturnsThreadNotFoundForMissingPersistedThread(t *testing.T) {
	t.Parallel()

	threadID := "thread-missing"
	svc := NewService(
		silentLogger(),
		historyThreadStore(),
		&failFastBindingStore{
			agentErr: map[string]error{
				threadID: platformdb.WrapStoreError(platformdb.ErrNotFound, "get_by_agent_id", "binding"),
			},
		},
		&historyTestSessionProvider{},
		nil,
		nil,
		nil,
		nil,
	)

	_, err := svc.ReadMessages(context.Background(), threadID, 10, "")
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("ReadMessages() error = %v, want not found", err)
	}
	text := err.Error()
	if !strings.Contains(text, threadID) || !strings.Contains(text, "not found") {
		t.Fatalf("ReadMessages() error = %q, want thread id and not found", text)
	}
	if strings.Contains(text, "get_by_agent_id") || strings.Contains(text, "binding") {
		t.Fatalf("ReadMessages() error = %q, want semantic thread not found without binding store details", text)
	}
}

func TestListProjectsPersistedStatusIntoRef(t *testing.T) {
	t.Parallel()

	svc := NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{ThreadID: "thread-archived", AgentID: "agent-archived", Prompt: "demo", Status: "archived"}),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "thread-archived" || got[0].Status != "archived" {
		t.Fatalf("List() = %#v, want archived status projected", got)
	}
}

func TestListKeepsUnnamedThreadNameEmpty(t *testing.T) {
	t.Parallel()

	svc := NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{ThreadID: "agent-empty-name", AgentID: "agent-empty-name", Status: "created"}),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List() = %#v, want one ref", got)
	}
	if got[0].Name != "" {
		t.Fatalf("ref name = %q, want empty", got[0].Name)
	}
}

func TestGetReturnsProviderIdentityFromBinding(t *testing.T) {
	t.Parallel()

	const providerUUID = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	svc := NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{
			ThreadID: "agent-1",
			AgentID:  "agent-1",
			Prompt:   "codex-1",
			Model:    "gpt-5.5",
			Cwd:      "/repo",
			Port:     9567,
			Status:   "created",
		}),
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: providerUUID,
			SessionUUID:      providerUUID,
			CodexThreadID:    "agent-1",
			RolloutPath:      writeExistingProviderHistoryFile(t, providerUUID),
			Cwd:              "/repo",
		}),
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.Get(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex", got.Provider)
	}
	if got.ProviderThreadID != providerUUID {
		t.Fatalf("ProviderThreadID = %q, want %s", got.ProviderThreadID, providerUUID)
	}
	if got.SessionID != providerUUID {
		t.Fatalf("SessionID = %q, want %s", got.SessionID, providerUUID)
	}
	if got.CWD != "/repo" || got.Model != "gpt-5.5" || got.Port != 9567 {
		t.Fatalf("runtime fields = cwd:%q model:%q port:%d, want /repo gpt-5.5 9567", got.CWD, got.Model, got.Port)
	}
}

func TestGetPrefersSessionUUIDForResolvedIdentity(t *testing.T) {
	t.Parallel()

	const sessionUUID = "019e218f-b9c9-7c60-87f7-449577c795dc"
	svc := NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{ThreadID: "agent-1", AgentID: "agent-1", Prompt: "codex-1"}),
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "agent-1",
			SessionUUID:      sessionUUID,
			CodexThreadID:    "agent-1",
			RolloutPath:      writeExistingProviderHistoryFile(t, sessionUUID),
		}),
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.Get(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ProviderThreadID != sessionUUID {
		t.Fatalf("ProviderThreadID = %q, want %s", got.ProviderThreadID, sessionUUID)
	}
	if got.SessionID != sessionUUID {
		t.Fatalf("SessionID = %q, want %s", got.SessionID, sessionUUID)
	}
}

func TestGetRecoversOfficialCodexUUIDWithoutHistoryFile(t *testing.T) {
	t.Parallel()

	const sessionUUID = "019e218f-b9c9-7c60-87f7-449577c795dc"
	svc := NewService(
		silentLogger(),
		historyThreadStore(ThreadRecord{ThreadID: "agent-1", AgentID: "agent-1", Prompt: "codex-1"}),
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "agent-1",
			SessionUUID:      sessionUUID,
			CodexThreadID:    "agent-1",
			CodexHome:        t.TempDir(),
		}),
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.Get(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ProviderThreadID != sessionUUID {
		t.Fatalf("ProviderThreadID = %q, want official UUID %s without provider history file", got.ProviderThreadID, sessionUUID)
	}
	if got.SessionID != sessionUUID {
		t.Fatalf("SessionID = %q, want %s", got.SessionID, sessionUUID)
	}
}

func historyThreadStore(threads ...ThreadRecord) *historyThreadPageStore {
	store := &stubThreadStore{threads: append([]ThreadRecord(nil), threads...)}
	if len(store.threads) == 0 {
		return &historyThreadPageStore{stubThreadStore: store}
	}
	store.threadByID = make(map[string]*ThreadRecord, len(store.threads))
	for i := range store.threads {
		thread := &store.threads[i]
		store.threadByID[strings.TrimSpace(thread.ThreadID)] = thread
	}
	return &historyThreadPageStore{stubThreadStore: store}
}

// historyThreadPageStore adds bounded list-page behavior to the shared thread test store.
type historyThreadPageStore struct {
	*stubThreadStore
}

// ListPage returns a bounded test projection for legacy List() read-view tests.
func (s *historyThreadPageStore) ListPage(ctx context.Context, params contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	threads, err := s.ListAll(ctx)
	if err != nil {
		return contract.ThreadListPage{}, err
	}
	records := make([]contract.ThreadListRecord, 0, len(threads))
	for _, thread := range threads {
		records = append(records, contract.ThreadListRecord{
			ThreadID:         thread.ThreadID,
			AgentID:          thread.AgentID,
			ParentAgentID:    thread.ParentAgentID,
			AgentType:        thread.AgentType,
			AgentMemoryScope: thread.AgentMemoryScope,
			Name:             thread.Name,
			Prompt:           thread.Prompt,
			Model:            thread.Model,
			Cwd:              thread.Cwd,
			Status:           thread.Status,
			Port:             thread.Port,
			PID:              thread.PID,
			CreatedAt:        thread.CreatedAt,
			UpdatedAt:        thread.UpdatedAt,
			FinishedAt:       thread.FinishedAt,
			LastEventType:    thread.LastEventType,
			ErrorMessage:     thread.ErrorMessage,
			WorkspaceRunKey:  thread.WorkspaceRunKey,
			OwnerThreadID:    thread.OwnerThreadID,
			ConfigOverride:   thread.ConfigOverride,
			AgentKey:         thread.AgentKey,
			PromptVersionID:  thread.PromptVersionID,
			PendingLaunch:    thread.PendingLaunch,
			ManuallyRenamed:  thread.ManuallyRenamed,
		})
	}
	if params.Limit > 0 && len(records) > params.Limit {
		return contract.ThreadListPage{
			Threads:             records[:params.Limit],
			HasMore:             true,
			NextCursorCreatedAt: records[params.Limit-1].CreatedAt,
			NextCursorThreadID:  records[params.Limit-1].ThreadID,
		}, nil
	}
	return contract.ThreadListPage{Threads: records}, nil
}
