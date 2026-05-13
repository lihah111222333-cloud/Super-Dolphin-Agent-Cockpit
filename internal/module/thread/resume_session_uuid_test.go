package thread

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func writeExistingProviderHistoryFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write provider history file: %v", err)
	}
	return path
}

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
	// over the stale ProviderThreadID placeholder when the CLI file exists.
	const realUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	rolloutPath := writeExistingProviderHistoryFile(t)
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "agent-1",
		CodexThreadID:    "thread-public",
		RolloutPath:      rolloutPath,
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
		session := &stubSession{threadID: realUUID, rolloutPath: rolloutPath}
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

func TestServiceResumeDoesNotUseAgentIDAsClaudeProviderThreadID(t *testing.T) {
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
		AgentID:       "agent-1",
		Provider:      "claude",
		CodexThreadID: "thread-public",
		Cwd:           "/repo",
	}}
	sessions := &stubSessionProvider{}
	var resumeReq dto.ResumeSessionRequest
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		resumeReq = req
		session := &stubSession{}
		sessions.session = session
		return session, nil
	}}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "agent-1"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumeReq.ProviderThreadID != "" {
		t.Fatalf("ResumeSessionRequest.ProviderThreadID = %q, want empty", resumeReq.ProviderThreadID)
	}
	if bindings.upsert.ProviderThreadID == "agent-1" {
		t.Fatalf("binding upsert provider_thread_id = agent id %q", bindings.upsert.ProviderThreadID)
	}
}
