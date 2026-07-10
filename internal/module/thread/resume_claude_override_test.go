package thread

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestServiceResumeClaudeWithoutStoredOverrideDoesNotInventConfigOverride(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555555"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID: "thread-1", AgentID: "agent-1", Prompt: "resume",
		Model: "sonnet", Cwd: "/repo", CreatedAt: 123,
		Status: statusCreated, LastEventType: "",
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID: "agent-1", Provider: "claude", ProviderThreadID: providerThreadID,
		CodexThreadID: "thread-1", RolloutPath: rolloutPath, Cwd: "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.Model != "sonnet" || req.Effort != "" || req.ConfigOverride.Model != nil || req.ConfigOverride.Effort != nil {
			t.Fatalf("ResumeSession request = %#v, want sonnet with empty override", req)
		}
		session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
		sessions.session = session
		return session, nil
	}}

	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !reflect.DeepEqual(orch.launchReq.Env, []string{"AGENT_PROVIDER=claude", "AGENT_MODEL=sonnet"}) {
		t.Fatalf("launch env = %#v", orch.launchReq.Env)
	}
}

func TestBackgroundResumeIfNeededRehydratesClaudeOverrideConfig(t *testing.T) {
	t.Parallel()

	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	const providerThreadID = "11111111-2222-3333-4444-555555555555"
	rolloutPath := writeExistingProviderHistoryFile(t)
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID: "thread-1", AgentID: "agent-1", Prompt: "resume",
		Model: "sonnet", Cwd: "/repo", CreatedAt: 123, Status: statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Model:  model,
			Effort: effort,
			Runtime: map[string]any{
				"legacyPromptSnapshotMigration": true,
			},
		}),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID: "agent-1", Provider: "claude", ProviderThreadID: providerThreadID,
		CodexThreadID: "thread-1", RolloutPath: rolloutPath, Cwd: "/repo",
	}}
	sessions := &stubSessionProvider{}
	resumeReqCh := make(chan dto.ResumeSessionRequest, 1)
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		select {
		case resumeReqCh <- req:
		default:
		}
		session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
		sessions.session = session
		return session, nil
	}}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)
	svc.backgroundResumeIfNeeded(context.Background(), "thread-1")

	select {
	case req := <-resumeReqCh:
		assertBackgroundClaudeOverrideResumeRequest(t, req, providerThreadID, model, effort)
	case <-time.After(time.Second):
		t.Fatal("backgroundResumeIfNeeded() did not trigger resume")
	}
}

func assertBackgroundClaudeOverrideResumeRequest(t *testing.T, req dto.ResumeSessionRequest, providerThreadID, model, effort string) {
	t.Helper()
	if req.ProviderThreadID != providerThreadID {
		t.Fatalf("ProviderThreadID = %q, want %s", req.ProviderThreadID, providerThreadID)
	}
	if req.Model != model || req.Effort != effort {
		t.Fatalf("ResumeSession request = %#v, want model/effort restored", req)
	}
	assertClaudeOverrideConfig(t, req.ConfigOverride, model, effort)
}
