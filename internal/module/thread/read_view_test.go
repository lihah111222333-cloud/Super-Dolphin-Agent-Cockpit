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
