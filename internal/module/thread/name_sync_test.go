package thread

import (
	"context"
	"errors"
	"reflect"
	"testing"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestSetNameSyncsProviderWhenSupported(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "before",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	session := &stubSession{threadID: "thread-1"}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	sessions := &stubSessionProvider{session: session}
	svc := NewService(silentLogger(), threads, bindings, sessions, nil, nil, nil, nil)

	if err := svc.SetName(context.Background(), "thread-1", "after"); err != nil {
		t.Fatalf("SetName() error = %v", err)
	}
	if threads.upsert.Prompt != "after" {
		t.Fatalf("persisted prompt = %q, want after", threads.upsert.Prompt)
	}
	if !reflect.DeepEqual(session.setThreadNameCalls, []string{"thread-1:after"}) {
		t.Fatalf("provider rename calls = %#v", session.setThreadNameCalls)
	}
}

func TestSetNameSucceedsWithoutActiveSessionAfterRestart(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:        "thread-1",
		AgentID:         "agent-1",
		Prompt:          "before",
		CreatedAt:       123,
		Status:          statusCreated,
		ManuallyRenamed: false,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:       "agent-1",
		Provider:      "codex",
		CodexThreadID: "thread-1",
		SessionUUID:   "019d5f6b-fb3c-7760-9d6f-54005553f701",
	}}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, nil, nil, nil, nil)

	if err := svc.SetName(context.Background(), "thread-1", "after restart"); err != nil {
		t.Fatalf("SetName() error = %v, want nil when provider session is not active", err)
	}
	if threads.upsert.Prompt != "after restart" || threads.upsert.Name != "after restart" {
		t.Fatalf("persisted name/prompt = %q/%q, want after restart", threads.upsert.Name, threads.upsert.Prompt)
	}
	if !threads.upsert.ManuallyRenamed {
		t.Fatalf("ManuallyRenamed = false, want true")
	}
}

func TestSetNameReturnsProviderRenameErrorForActiveSession(t *testing.T) {
	t.Parallel()

	renameErr := errors.New("provider rename failed")
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "before",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	session := &stubSession{threadID: "thread-1", setThreadNameErr: renameErr}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{session: session}, nil, nil, nil, nil)

	err := svc.SetName(context.Background(), "thread-1", "after")
	if !errors.Is(err, renameErr) {
		t.Fatalf("SetName() error = %v, want provider rename failure", err)
	}
	if threads.upsert.Prompt != "after" {
		t.Fatalf("persisted prompt = %q, want after", threads.upsert.Prompt)
	}
}
