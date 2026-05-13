package thread

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestReadThreadHistoryUsesSessionThreadList(t *testing.T) {
	t.Parallel()

	svc, ok := NewService(
		silentLogger(),
		&historyTestThreadStore{threads: map[string]threadstore.Thread{
			"thread-1": {ThreadID: "thread-1", AgentID: "agent-1", Prompt: "demo"},
		}},
		newHistoryTestBindingStore(&bindingstore.Binding{
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

func TestReadMessagesFallsBackToPersistedRolloutWithoutSession(t *testing.T) {
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
		&historyTestThreadStore{threads: map[string]threadstore.Thread{
			"thread-1": {ThreadID: "thread-1", AgentID: "agent-1", Prompt: "demo"},
		}},
		newHistoryTestBindingStore(&bindingstore.Binding{
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
	want := dto.ThreadMessagesResult{
		Messages: []dto.Message{
			{ID: 2, AgentID: "agent-1", Role: "assistant", EventType: "agent_message", Content: "world", Timestamp: time.Date(2026, 3, 21, 1, 3, 3, 0, time.UTC)},
			{ID: 1, AgentID: "agent-1", Role: "user", EventType: "", Content: "hello", Timestamp: time.Date(2026, 3, 21, 1, 2, 3, 0, time.UTC)},
		},
		Total: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadMessages() = %#v, want %#v", got, want)
	}
}

func TestListProjectsPersistedStatusIntoRef(t *testing.T) {
	t.Parallel()

	svc := NewService(
		silentLogger(),
		&historyTestThreadStore{threads: map[string]threadstore.Thread{
			"thread-archived": {ThreadID: "thread-archived", AgentID: "agent-archived", Prompt: "demo", Status: "archived"},
		}},
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
		&historyTestThreadStore{threads: map[string]threadstore.Thread{
			"agent-empty-name": {ThreadID: "agent-empty-name", AgentID: "agent-empty-name", Status: "created"},
		}},
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
		&historyTestThreadStore{threads: map[string]threadstore.Thread{
			"agent-1": {
				ThreadID: "agent-1",
				AgentID:  "agent-1",
				Prompt:   "codex-1",
				Model:    "gpt-5.5",
				Cwd:      "/repo",
				Port:     9567,
				Status:   "created",
			},
		}},
		newHistoryTestBindingStore(&bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: providerUUID,
			SessionUUID:      providerUUID,
			CodexThreadID:    "agent-1",
			RolloutPath:      writeExistingProviderHistoryFile(t),
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
		&historyTestThreadStore{threads: map[string]threadstore.Thread{
			"agent-1": {ThreadID: "agent-1", AgentID: "agent-1", Prompt: "codex-1"},
		}},
		newHistoryTestBindingStore(&bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "agent-1",
			SessionUUID:      sessionUUID,
			CodexThreadID:    "agent-1",
			RolloutPath:      writeExistingProviderHistoryFile(t),
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

func TestGetDoesNotPromoteSessionUUIDWithoutHistoryFile(t *testing.T) {
	t.Parallel()

	const sessionUUID = "019e218f-b9c9-7c60-87f7-449577c795dc"
	svc := NewService(
		silentLogger(),
		&historyTestThreadStore{threads: map[string]threadstore.Thread{
			"agent-1": {ThreadID: "agent-1", AgentID: "agent-1", Prompt: "codex-1"},
		}},
		newHistoryTestBindingStore(&bindingstore.Binding{
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
	if got.ProviderThreadID != "" {
		t.Fatalf("ProviderThreadID = %q, want empty without provider history file", got.ProviderThreadID)
	}
	if got.SessionID != sessionUUID {
		t.Fatalf("SessionID = %q, want %s", got.SessionID, sessionUUID)
	}
}
