package unified

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	goldentest "github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

type resumeCaptureDriver struct {
	name      string
	session   contract.Session
	resumeReq dto.ResumeSessionRequest
	resumed   int
}

func (d *resumeCaptureDriver) Name() string { return d.name }
func (d *resumeCaptureDriver) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	return d.session, nil
}
func (d *resumeCaptureDriver) ResumeSession(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	d.resumed++
	d.resumeReq = req
	return d.session, nil
}

func TestSessionResolverAutoResumePassesCodexIdentityGolden(t *testing.T) {
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111111"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{ThreadID: "public-thread-1", AgentID: "agent-1"}},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:           "codex",
				AgentID:            "agent-1",
				ProviderThreadID:   "11111111-aaaa-bbbb-cccc-111111111111",
				Cwd:                "/repo",
				CodexHome:          "/Users/test/.codex",
				CodexInstanceKey:   "codex-instance-key-1",
				CodexModelProvider: "openai",
			},
		}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "public-thread-1"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumed != 1 {
		t.Fatalf("ResumeSession calls = %d, want 1", driver.resumed)
	}
	goldentest.AssertJSON(t, goldentest.Case{
		BaseDir: "testdata/golden",
		Domain:  goldentest.DomainIntegration,
		Name:    "auto_resume_identity_request",
	}, driver.resumeReq)
}

func TestSessionResolverProviderThreadAutoResumeDoesNotUseCodexThreadID(t *testing.T) {
	// Phase 2 of the session-stopped rootfix removed the
	// binding.CodexThreadID -> req.ThreadID fallback because CodexThreadID is
	// a routing key (often agent_xxx placeholder) and feeding it into the
	// driver as a thread id let placeholders cross provider boundaries into
	// claudecli, which caused the 5s system:init deadlock. After the change
	// req.ThreadID stays empty when no public thread id is provided, even if
	// CodexThreadID happens to hold a non-empty value.
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "33333333-aaaa-bbbb-cccc-333333333333"}}
	resolver := &sessionResolver{
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:33333333-aaaa-bbbb-cccc-333333333333": {
				Provider:         "codex",
				AgentID:          "agent-3",
				ProviderThreadID: "33333333-aaaa-bbbb-cccc-333333333333",
				CodexThreadID:    "public-thread-3",
			},
		}},
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
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "22222222-aaaa-bbbb-cccc-222222222222"}}
	resolver := &sessionResolver{
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:22222222-aaaa-bbbb-cccc-222222222222": {
				Provider:         "codex",
				AgentID:          "agent-2",
				ProviderThreadID: "22222222-aaaa-bbbb-cccc-222222222222",
			},
		}},
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
