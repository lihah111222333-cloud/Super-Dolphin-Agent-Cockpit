package unified

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
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
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "provider-thread-1"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &threadstore.Thread{ThreadID: "public-thread-1", AgentID: "agent-1"}},
		bindingStore: stubBindingLookup{bindings: map[string]*bindingstore.Binding{
			"codex:provider-thread-1": {
				Provider:           "codex",
				AgentID:            "agent-1",
				ProviderThreadID:   "provider-thread-1",
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

func TestSessionResolverProviderThreadAutoResumeUsesCodexThreadID(t *testing.T) {
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "provider-thread-3"}}
	resolver := &sessionResolver{
		bindingStore: stubBindingLookup{bindings: map[string]*bindingstore.Binding{
			"codex:provider-thread-3": {
				Provider:         "codex",
				AgentID:          "agent-3",
				ProviderThreadID: "provider-thread-3",
				CodexThreadID:    "public-thread-3",
			},
		}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "provider-thread-3"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumed != 1 {
		t.Fatalf("ResumeSession calls = %d, want 1", driver.resumed)
	}
	if driver.resumeReq.ThreadID != "public-thread-3" {
		t.Fatalf("ThreadID = %q, want public-thread-3", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.AgentID != "agent-3" {
		t.Fatalf("AgentID = %q, want agent-3", driver.resumeReq.AgentID)
	}
	if driver.resumeReq.ProviderThreadID != "provider-thread-3" {
		t.Fatalf("ProviderThreadID = %q, want provider-thread-3", driver.resumeReq.ProviderThreadID)
	}
}

func TestSessionResolverAutoResumeDoesNotUseAgentIDAsThreadIDWithoutPublicThread(t *testing.T) {
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "provider-thread-2"}}
	resolver := &sessionResolver{
		bindingStore: stubBindingLookup{bindings: map[string]*bindingstore.Binding{
			"codex:provider-thread-2": {
				Provider:         "codex",
				AgentID:          "agent-2",
				ProviderThreadID: "provider-thread-2",
			},
		}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "provider-thread-2"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumeReq.ThreadID == "agent-2" {
		t.Fatalf("auto-resume ThreadID = AgentID %q; want public thread id or empty when unavailable", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.ThreadID != "" {
		t.Fatalf("auto-resume ThreadID = %q, want empty without a public thread id source", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.ProviderThreadID != "provider-thread-2" {
		t.Fatalf("ProviderThreadID = %q, want provider-thread-2", driver.resumeReq.ProviderThreadID)
	}
}
