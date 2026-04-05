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
	// SessionUUID must look like a real UUID so the resume logic prefers it
	// over the stale ProviderThreadID placeholder.
	const realUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "agent-1",
		CodexThreadID:    "thread-public",
		SessionUUID:      realUUID,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.Provider != "claude" {
			t.Fatalf("Provider = %q, want claude", req.Provider)
		}
		if req.ProviderThreadID != realUUID {
			t.Fatalf("ProviderThreadID = %q, want %s", req.ProviderThreadID, realUUID)
		}
		session := &stubSession{threadID: realUUID}
		sessions.session = session
		return session, nil
	}}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	result, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-public"})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.SessionID != realUUID {
		t.Fatalf("SessionID = %q, want %s", result.SessionID, realUUID)
	}
}
