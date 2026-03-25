package thread

import (
	"context"
	"reflect"
	"testing"

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
