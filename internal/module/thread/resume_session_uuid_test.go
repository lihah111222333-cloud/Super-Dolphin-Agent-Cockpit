package thread

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestServiceResumePrefersSessionUUIDOverStaleProviderThreadID(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-public",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "claude-3",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "agent-1",
		CodexThreadID:    "thread-public",
		SessionUUID:      "session-uuid-1",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.Provider != "claude" {
			t.Fatalf("Provider = %q, want claude", req.Provider)
		}
		if req.ProviderThreadID != "session-uuid-1" {
			t.Fatalf("ProviderThreadID = %q, want session-uuid-1", req.ProviderThreadID)
		}
		session := &stubSession{threadID: "session-uuid-1"}
		sessions.session = session
		return session, nil
	}}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	result, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-public"})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.SessionID != "session-uuid-1" {
		t.Fatalf("SessionID = %q, want session-uuid-1", result.SessionID)
	}
}
