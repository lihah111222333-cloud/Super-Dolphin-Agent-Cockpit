package unified

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	rolloutPath := writeExistingProviderHistoryFile(t)
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111111"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{ThreadID: "public-thread-1", AgentID: "agent-1"}},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:           "codex",
				AgentID:            "agent-1",
				ProviderThreadID:   "11111111-aaaa-bbbb-cccc-111111111111",
				RolloutPath:        rolloutPath,
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

func TestSessionResolverAutoResumePassesRuntimeConfig(t *testing.T) {
	rolloutPath := writeExistingProviderHistoryFile(t)
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111112"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID: "public-thread-1",
			AgentID:  "agent-1",
			RuntimeConfig: map[string]any{
				"additionalWorkingDirectories": []any{"/repo/extra"},
			},
		}},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:         "codex",
				AgentID:          "agent-1",
				ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111112",
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
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
	want := map[string]any{"additionalWorkingDirectories": []any{"/repo/extra"}}
	if !reflect.DeepEqual(driver.resumeReq.Config, want) {
		t.Fatalf("ResumeSession Config = %#v, want %#v", driver.resumeReq.Config, want)
	}
}

func TestSessionResolverAutoResumeBackfillsCodexIdentityFromRuntimeConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime map[string]any
	}{
		{
			name: "canonical keys",
			runtime: map[string]any{
				"codexHome":          "/runtime/.codex",
				"codexInstanceKey":   "runtime-instance-key",
				"codexModelProvider": "runtime-provider",
			},
		},
		{
			name: "snake case aliases",
			runtime: map[string]any{
				"codex_home":           "/runtime/snake/.codex",
				"codex_instance_key":   "runtime-snake-instance-key",
				"codex_model_provider": "runtime-snake-provider",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rolloutPath := writeExistingProviderHistoryFile(t)
			driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "66666666-aaaa-bbbb-cccc-666666666666"}}
			resolver := &sessionResolver{
				threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
					ThreadID:      "public-thread-1",
					AgentID:       "agent-1",
					RuntimeConfig: tc.runtime,
				}},
				bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
					"codex:provider-thread-1": {
						Provider:         "codex",
						AgentID:          "agent-1",
						ProviderThreadID: "66666666-aaaa-bbbb-cccc-666666666666",
						RolloutPath:      rolloutPath,
						Cwd:              "/repo",
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
			if driver.resumeReq.CodexHome != codexIdentityTestString(tc.runtime, "codexHome", "codex_home") ||
				driver.resumeReq.CodexInstanceKey != codexIdentityTestString(tc.runtime, "codexInstanceKey", "codex_instance_key") ||
				driver.resumeReq.CodexModelProvider != codexIdentityTestString(tc.runtime, "codexModelProvider", "codex_model_provider") {
				t.Fatalf("ResumeSession codex identity = %q/%q/%q, want runtime config identity",
					driver.resumeReq.CodexHome,
					driver.resumeReq.CodexInstanceKey,
					driver.resumeReq.CodexModelProvider)
			}
		})
	}
}

func TestSessionResolverAutoResumePrefersBindingCodexIdentityOverRuntimeConfig(t *testing.T) {
	rolloutPath := writeExistingProviderHistoryFile(t)
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "77777777-aaaa-bbbb-cccc-777777777777"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID: "public-thread-1",
			AgentID:  "agent-1",
			RuntimeConfig: map[string]any{
				"codexHome":          "/runtime/.codex",
				"codexInstanceKey":   "runtime-instance-key",
				"codexModelProvider": "runtime-provider",
			},
		}},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:           "codex",
				AgentID:            "agent-1",
				ProviderThreadID:   "77777777-aaaa-bbbb-cccc-777777777777",
				RolloutPath:        rolloutPath,
				Cwd:                "/repo",
				CodexHome:          "/binding/.codex",
				CodexInstanceKey:   "binding-instance-key",
				CodexModelProvider: "binding-provider",
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
	if driver.resumeReq.CodexHome != "/binding/.codex" ||
		driver.resumeReq.CodexInstanceKey != "binding-instance-key" ||
		driver.resumeReq.CodexModelProvider != "binding-provider" {
		t.Fatalf("ResumeSession codex identity = %q/%q/%q, want binding identity",
			driver.resumeReq.CodexHome,
			driver.resumeReq.CodexInstanceKey,
			driver.resumeReq.CodexModelProvider)
	}
}

func codexIdentityTestString(config map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := config[key].(string)
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func TestSessionResolverProviderThreadAutoResumeDoesNotUseCodexThreadID(t *testing.T) {
	// Phase 2 of the session-stopped rootfix removed the
	// binding.CodexThreadID -> req.ThreadID fallback because CodexThreadID is
	// a routing key (often agent-placeholder value) and feeding it into the
	// driver as a thread id let placeholders cross provider boundaries into
	// claudecli, which caused the 5s system:init deadlock. After the change
	// req.ThreadID stays empty when no public thread id is provided, even if
	// CodexThreadID happens to hold a non-empty value.
	rolloutPath := writeExistingProviderHistoryFile(t)
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "33333333-aaaa-bbbb-cccc-333333333333"}}
	resolver := &sessionResolver{
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:33333333-aaaa-bbbb-cccc-333333333333": {
				Provider:         "codex",
				AgentID:          "agent-3",
				ProviderThreadID: "33333333-aaaa-bbbb-cccc-333333333333",
				CodexThreadID:    "public-thread-3",
				RolloutPath:      rolloutPath,
				Cwd:              t.TempDir(),
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
	rolloutPath := writeExistingProviderHistoryFile(t)
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "22222222-aaaa-bbbb-cccc-222222222222"}}
	resolver := &sessionResolver{
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:22222222-aaaa-bbbb-cccc-222222222222": {
				Provider:         "codex",
				AgentID:          "agent-2",
				ProviderThreadID: "22222222-aaaa-bbbb-cccc-222222222222",
				RolloutPath:      rolloutPath,
				Cwd:              t.TempDir(),
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

func TestSessionResolverAutoResumeRequiresProviderHistoryFile(t *testing.T) {
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "44444444-aaaa-bbbb-cccc-444444444444"}}
	resolver := &sessionResolver{
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:44444444-aaaa-bbbb-cccc-444444444444": {
				Provider:         "codex",
				AgentID:          "agent-4",
				ProviderThreadID: "44444444-aaaa-bbbb-cccc-444444444444",
			},
		}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	_, err := resolver.ResolveSession(context.Background(), "44444444-aaaa-bbbb-cccc-444444444444")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ResolveSession() error = %v, want not found without provider history", err)
	}
	if errors.Is(err, contract.ErrSessionNotFound) {
		t.Fatalf("ResolveSession() error = %v, want lookup not found wrapper", err)
	}
	if driver.resumed != 0 {
		t.Fatalf("ResumeSession calls = %d, want 0", driver.resumed)
	}
}

func TestSessionResolverAutoResumeRejectsMissingBindingCWD(t *testing.T) {
	rolloutPath := writeExistingProviderHistoryFile(t)
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "55555555-aaaa-bbbb-cccc-555555555555"}}
	resolver := &sessionResolver{
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:55555555-aaaa-bbbb-cccc-555555555555": {
				Provider:         "codex",
				AgentID:          "agent-5",
				ProviderThreadID: "55555555-aaaa-bbbb-cccc-555555555555",
				RolloutPath:      rolloutPath,
			},
		}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	_, err := resolver.ResolveSession(context.Background(), "55555555-aaaa-bbbb-cccc-555555555555")
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("ResolveSession() error = %v, want cwd required", err)
	}
	if driver.resumed != 0 {
		t.Fatalf("ResumeSession calls = %d, want 0", driver.resumed)
	}
}

func writeExistingProviderHistoryFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write provider history file: %v", err)
	}
	return path
}
