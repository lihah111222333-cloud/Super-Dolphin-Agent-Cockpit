package unified

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestSessionResolverProviderThreadAutoResumeDoesNotUseCodexThreadID(t *testing.T) {
	// Phase 2 of the session-stopped rootfix removed the
	// binding.CodexThreadID -> req.ThreadID fallback because CodexThreadID is
	// a routing key (often agent-placeholder value) and feeding it into the
	// driver as a thread id let placeholders cross provider boundaries into
	// claudecli, which caused the 5s system:init deadlock. After the change
	// req.ThreadID stays empty when no public thread id is provided, even if
	// CodexThreadID happens to hold a non-empty value.
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("33333333-aaaa-bbbb-cccc-333333333333")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "33333333-aaaa-bbbb-cccc-333333333333"}}
	resolver := &sessionResolver{
		threadStore: keyedThreadLookup{
			"public-thread-3": {
				ThreadID:       "public-thread-3",
				AgentID:        "agent-3",
				Status:         "running",
				PromptSnapshot: autoResumePromptSnapshotForTest(),
			},
		},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:33333333-aaaa-bbbb-cccc-333333333333": {
				Provider:         "codex",
				AgentID:          "agent-3",
				ProviderThreadID: "33333333-aaaa-bbbb-cccc-333333333333",
				CodexThreadID:    "public-thread-3",
				RolloutPath:      rolloutPath,
				Cwd:              t.TempDir(),
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "33333333-aaaa-bbbb-cccc-333333333333"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumed != 1 {
		t.Fatalf("ResumeSession calls = %d, want 1", driver.resumed)
	}
	if driver.resumeReq.ThreadID != "" {
		t.Fatalf("ThreadID = %q, want empty (CodexThreadID fallback removed)", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.AgentID != "agent-3" {
		t.Fatalf("AgentID = %q, want agent-3", driver.resumeReq.AgentID)
	}
	if driver.resumeReq.ProviderThreadID != "33333333-aaaa-bbbb-cccc-333333333333" {
		t.Fatalf("ProviderThreadID = %q, want 33333333-aaaa-bbbb-cccc-333333333333", driver.resumeReq.ProviderThreadID)
	}
}

func TestSessionResolverAutoResumeDoesNotUseAgentIDAsThreadIDWithoutPublicThread(t *testing.T) {
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("22222222-aaaa-bbbb-cccc-222222222222")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "22222222-aaaa-bbbb-cccc-222222222222"}}
	resolver := &sessionResolver{
		threadStore: keyedThreadLookup{
			"agent-2": {
				ThreadID:       "agent-2",
				AgentID:        "agent-2",
				Status:         "running",
				PromptSnapshot: autoResumePromptSnapshotForTest(),
			},
		},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:22222222-aaaa-bbbb-cccc-222222222222": {
				Provider:         "codex",
				AgentID:          "agent-2",
				ProviderThreadID: "22222222-aaaa-bbbb-cccc-222222222222",
				RolloutPath:      rolloutPath,
				Cwd:              t.TempDir(),
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "22222222-aaaa-bbbb-cccc-222222222222"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumeReq.ThreadID == "agent-2" {
		t.Fatalf("auto-resume ThreadID = AgentID %q; want public thread id or empty when unavailable", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.ThreadID != "" {
		t.Fatalf("auto-resume ThreadID = %q, want empty without a public thread id source", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.ProviderThreadID != "22222222-aaaa-bbbb-cccc-222222222222" {
		t.Fatalf("ProviderThreadID = %q, want 22222222-aaaa-bbbb-cccc-222222222222", driver.resumeReq.ProviderThreadID)
	}
}
