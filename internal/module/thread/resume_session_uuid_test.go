package thread

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
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

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-public",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "claude-3",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	// SessionUUID must look like a real UUID so the resume logic prefers it
	// over the stale ProviderThreadID placeholder when the CLI file exists.
	const realUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	rolloutPath := writeExistingProviderHistoryFile(t)
	bindings := &stubBindingStore{binding: &BindingRecord{
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

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-public",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "claude-3",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:       "agent-1",
		Provider:      "claude",
		CodexThreadID: "thread-public",
		Cwd:           "/repo",
	}}
	sessions := &stubSessionProvider{}
	resumeCalled := false
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		resumeCalled = true
		t.Fatalf("ResumeSession called without recoverable ProviderThreadID: %#v", req)
		return nil, nil
	}}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-public"})
	if err == nil || !strings.Contains(err.Error(), "provider thread id is required") {
		t.Fatalf("Resume() error = %v, want provider thread id required", err)
	}
	if resumeCalled {
		t.Fatal("ResumeSession should not be called without recoverable ProviderThreadID")
	}
	if bindings.upsert.ProviderThreadID == "agent-1" {
		t.Fatalf("binding upsert provider_thread_id = agent id %q", bindings.upsert.ProviderThreadID)
	}
}

func TestServiceRecoverUsesSessionUUIDForProviderResumeWhenPublicThreadIsAgentID(t *testing.T) {
	t.Parallel()

	const realUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "agent-1",
		AgentID:   "agent-1",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
	}, promptSnapshot: &PromptSnapshotRecord{
		DisplayName:           "Recovered Thread",
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  promptSnapshotHash("Recovered Thread", "stored base", "stored dev", "codex", nil, nil, 0),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "agent-1",
		CodexThreadID:    "agent-1",
		RolloutPath:      rolloutPath,
		SessionUUID:      realUUID,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.ProviderThreadID != realUUID {
			t.Fatalf("ProviderThreadID = %q, want %s", req.ProviderThreadID, realUUID)
		}
		if req.ThreadID != "agent-1" || req.AgentID != "agent-1" {
			t.Fatalf("ResumeSession request = %#v", req)
		}
		session := &stubSession{threadID: realUUID, rolloutPath: rolloutPath}
		sessions.session = session
		return session, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Recover(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if result.ThreadID != "agent-1" || result.Mode != "relaunch_resume" {
		t.Fatalf("Recover() result = %#v", result)
	}
	if bindings.upsert.ProviderThreadID != realUUID {
		t.Fatalf("binding upsert ProviderThreadID = %q, want %s", bindings.upsert.ProviderThreadID, realUUID)
	}
}

func TestServiceRecoverRejectsProviderResumeWithoutRecoverableUUID(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "agent-1",
		AgentID:   "agent-1",
		Prompt:    "Recovered Thread",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "agent-1",
		CodexThreadID:    "agent-1",
		Cwd:              "/repo",
	}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called without a recoverable provider session id")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, orch, nil).(*service)

	_, err := svc.Recover(context.Background(), "agent-1")
	if err == nil || !strings.Contains(err.Error(), "recover provider session id is required") {
		t.Fatalf("Recover() error = %v, want recover provider session id required", err)
	}
	if len(orch.recovered) != 0 || orch.launch.AgentID != "" {
		t.Fatalf("orchestration side effects = recovered %#v launch %#v, want none", orch.recovered, orch.launch)
	}
}
