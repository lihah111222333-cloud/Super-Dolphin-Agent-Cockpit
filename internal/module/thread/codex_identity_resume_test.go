package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestResolveResumeRequestCanonicalizesCodexIdentityFromSymlinkSources(t *testing.T) {
	t.Parallel()

	realHome, aliasHome := createCodexHomeSymlinkAlias(t)
	tests := []struct {
		name    string
		runtime map[string]any
		binding BindingRecord
		request ResumeRequest
	}{
		{
			name: "binding",
			binding: BindingRecord{
				CodexHome:          aliasHome,
				CodexInstanceKey:   "default",
				CodexModelProvider: "openai",
			},
			request: ResumeRequest{ThreadID: "thread-binding"},
		},
		{
			name: "runtime",
			runtime: map[string]any{
				contract.CodexHomeKey:          aliasHome,
				contract.CodexInstanceKeyKey:   "default",
				contract.CodexModelProviderKey: "openai",
			},
			request: ResumeRequest{ThreadID: "thread-runtime"},
		},
		{
			name: "request",
			request: ResumeRequest{
				ThreadID:           "thread-request",
				CodexHome:          aliasHome,
				CodexInstanceKey:   "default",
				CodexModelProvider: "openai",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threads, bindings := resumeCodexIdentityStores(t, "thread-"+tt.name, tt.runtime, tt.binding)
			svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, &stubSessionStarter{}, nil, nil, nil).(*service)

			got, _, err := svc.resolveResumeRequest(context.Background(), tt.request)
			if err != nil {
				t.Fatalf("resolveResumeRequest() error = %v", err)
			}
			assertResumeCodexIdentityCanonical(t, got, realHome)
			assertResumeConfigCodexIdentity(t, got.Config, realHome)
		})
	}
}

func TestResolveResumeRequestCanonicalizesConfigCodexIdentity(t *testing.T) {
	t.Parallel()

	canonicalHome, aliasHome := createCleanCodexHomeAlias(t)
	threads, bindings := resumeCodexIdentityStores(t, "thread-config-resolve", nil, BindingRecord{})
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, &stubSessionStarter{}, nil, nil, nil).(*service)

	got, _, err := svc.resolveResumeRequest(context.Background(), ResumeRequest{
		ThreadID: "thread-config-resolve",
		Config: map[string]any{
			contract.CodexHomeKey:          aliasHome,
			contract.CodexInstanceKeyKey:   "default",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err != nil {
		t.Fatalf("resolveResumeRequest() error = %v", err)
	}
	assertResumeCodexIdentityCanonical(t, got, canonicalHome)
	assertResumeConfigCodexIdentity(t, got.Config, canonicalHome)
}

func TestResumeSessionCanonicalizesCodexIdentityBeforeProvider(t *testing.T) {
	t.Parallel()

	realHome, aliasHome := createCodexHomeSymlinkAlias(t)
	threads, bindings := resumeCodexIdentityStores(t, "thread-provider", nil, BindingRecord{})
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.CodexHome != realHome ||
			req.CodexInstanceKey != "default" ||
			req.CodexModelProvider != "openai" {
			t.Fatalf("provider resume codex identity = (%q,%q,%q), want (%q,%q,%q)",
				req.CodexHome,
				req.CodexInstanceKey,
				req.CodexModelProvider,
				realHome,
				"default",
				"openai")
		}
		assertResumeConfigCodexIdentity(t, req.Config, realHome)
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f710"}
		sessions.session = session
		return session, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)

	if _, err := svc.resumeSession(context.Background(), ResumeRequest{
		ThreadID:           "thread-provider",
		CodexHome:          aliasHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}); err != nil {
		t.Fatalf("resumeSession() error = %v", err)
	}
}

func TestResumeSessionCanonicalizesCleanCodexHomeBeforeProvider(t *testing.T) {
	t.Parallel()

	canonicalHome, aliasHome := createCleanCodexHomeAlias(t)
	threads, bindings := resumeCodexIdentityStores(t, "thread-clean-provider", nil, BindingRecord{})
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.CodexHome != canonicalHome ||
			req.CodexInstanceKey != "default" ||
			req.CodexModelProvider != "openai" {
			t.Fatalf("provider resume codex identity = (%q,%q,%q), want (%q,%q,%q)",
				req.CodexHome,
				req.CodexInstanceKey,
				req.CodexModelProvider,
				canonicalHome,
				"default",
				"openai")
		}
		assertResumeConfigCodexIdentity(t, req.Config, canonicalHome)
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f712"}
		sessions.session = session
		return session, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)

	if _, err := svc.resumeSession(context.Background(), ResumeRequest{
		ThreadID:           "thread-clean-provider",
		CodexHome:          aliasHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}); err != nil {
		t.Fatalf("resumeSession() error = %v", err)
	}
}

func TestResumeSessionCanonicalizesConfigCodexIdentityBeforeProvider(t *testing.T) {
	t.Parallel()

	canonicalHome, aliasHome := createCleanCodexHomeAlias(t)
	threads, bindings := resumeCodexIdentityStores(t, "thread-config-provider", nil, BindingRecord{})
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.CodexHome != canonicalHome ||
			req.CodexInstanceKey != "default" ||
			req.CodexModelProvider != "openai" {
			t.Fatalf("provider resume codex identity = (%q,%q,%q), want (%q,%q,%q)",
				req.CodexHome,
				req.CodexInstanceKey,
				req.CodexModelProvider,
				canonicalHome,
				"default",
				"openai")
		}
		assertResumeConfigCodexIdentity(t, req.Config, canonicalHome)
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f713"}
		sessions.session = session
		return session, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)

	if _, err := svc.resumeSession(context.Background(), ResumeRequest{
		ThreadID: "thread-config-provider",
		Config: map[string]any{
			contract.CodexHomeKey:          aliasHome,
			contract.CodexInstanceKeyKey:   "default",
			contract.CodexModelProviderKey: "openai",
		},
	}); err != nil {
		t.Fatalf("resumeSession() error = %v", err)
	}
}

func TestValidateExplicitResumeCodexIdentityDoesNotResolveCompleteIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request ResumeRequest
	}{
		{
			name: "request fields",
			request: ResumeRequest{
				Provider:           "codex",
				CodexHome:          filepath.Join(t.TempDir(), "missing-after-validate"),
				CodexInstanceKey:   "default",
				CodexModelProvider: "openai",
			},
		},
		{
			name: "config fields",
			request: ResumeRequest{
				Provider: "codex",
				Config: map[string]any{
					contract.CodexHomeKey:          filepath.Join(t.TempDir(), "missing-after-validate"),
					contract.CodexInstanceKeyKey:   "default",
					contract.CodexModelProviderKey: "openai",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateExplicitResumeCodexIdentity(tt.request); err != nil {
				t.Fatalf("validateExplicitResumeCodexIdentity() error = %v, want no resolve before canonicalize", err)
			}
			_, err := canonicalizeResumeCodexIdentity(tt.request)
			if !errors.Is(err, contract.ErrCodexHomeNotFound) {
				t.Fatalf("canonicalizeResumeCodexIdentity() error = %v, want %v", err, contract.ErrCodexHomeNotFound)
			}
		})
	}
}

func TestServiceResumeForwardsResolvedCleanAliasCodexIdentity(t *testing.T) {
	t.Parallel()

	canonicalHome, aliasHome := createCleanCodexHomeAlias(t)
	threads, bindings := resumeCodexIdentityStores(t, "thread-single-canonicalize", nil, BindingRecord{
		CodexHome:          aliasHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	})
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.CodexHome != canonicalHome ||
			req.CodexInstanceKey != "default" ||
			req.CodexModelProvider != "openai" {
			t.Fatalf("provider resume codex identity = (%q,%q,%q), want single canonical identity (%q,%q,%q)",
				req.CodexHome,
				req.CodexInstanceKey,
				req.CodexModelProvider,
				canonicalHome,
				"default",
				"openai")
		}
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f70f"}
		sessions.session = session
		return session, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)

	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-single-canonicalize"}); err != nil {
		t.Fatalf("Resume() error = %v, want resolved codex identity forwarded", err)
	}
}

func TestServiceResumeRejectsInvalidCompleteHistoricalCodexIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		home      string
		wantError error
	}{
		{
			name:      "missing absolute home",
			home:      filepath.Join(t.TempDir(), "missing-codex-home"),
			wantError: contract.ErrCodexHomeNotFound,
		},
		{
			name:      "relative home",
			home:      "relative-codex-home",
			wantError: contract.ErrCodexIdentityInvalidType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threads, bindings := resumeCodexIdentityStores(t, "thread-invalid-history-"+tt.name, nil, BindingRecord{
				CodexHome:          tt.home,
				CodexInstanceKey:   "default",
				CodexModelProvider: "openai",
			})
			starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
				t.Fatal("ResumeSession should not be called when historical codex identity is complete but invalid")
				return nil, nil
			}}
			svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, &stubThreadOrchestration{}, nil).(*service)

			_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: threads.thread.ThreadID})
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Resume() error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

func TestServiceResumeRejectsPartialCodexIdentityBeforeProvider(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	superHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(superHome, "providers", "codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll app-managed codex home: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", superHome)

	explicitHome := t.TempDir()
	threads, bindings := resumeCodexIdentityStores(t, "thread-partial", nil, BindingRecord{})
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when codex identity is partial")
		return nil, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, &stubThreadOrchestration{}, nil).(*service)

	_, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID:  "thread-partial",
		CodexHome: explicitHome,
	})
	if !errors.Is(err, contract.ErrCodexInstanceKeyRequired) {
		t.Fatalf("Resume() error = %v, want %v", err, contract.ErrCodexInstanceKeyRequired)
	}
}

func TestServiceResumeRejectsPartialConfigCodexIdentityBeforeProvider(t *testing.T) {
	t.Parallel()

	explicitHome := t.TempDir()
	threads, bindings := resumeCodexIdentityStores(t, "thread-config-partial", nil, BindingRecord{})
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when config codex identity is partial")
		return nil, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, &stubThreadOrchestration{}, nil).(*service)

	_, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID: "thread-config-partial",
		Config: map[string]any{
			contract.CodexHomeKey: explicitHome,
		},
	})
	if !errors.Is(err, contract.ErrCodexInstanceKeyRequired) {
		t.Fatalf("Resume() error = %v, want %v", err, contract.ErrCodexInstanceKeyRequired)
	}
}

func TestResumeSessionLeavesNonCodexIdentityUnchanged(t *testing.T) {
	t.Parallel()

	_, aliasHome := createCleanCodexHomeAlias(t)
	threads, bindings := resumeCodexIdentityStores(t, "thread-claude-codex-fields", nil, BindingRecord{Provider: "claude"})
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.Provider != "claude" {
			t.Fatalf("Provider = %q, want claude", req.Provider)
		}
		if req.CodexHome != aliasHome ||
			req.CodexInstanceKey != "raw-instance" ||
			req.CodexModelProvider != "raw-model-provider" {
			t.Fatalf("non-codex codex identity fields = (%q,%q,%q), want unchanged alias identity",
				req.CodexHome,
				req.CodexInstanceKey,
				req.CodexModelProvider)
		}
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f711"}
		sessions.session = session
		return session, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)

	if _, err := svc.resumeSession(context.Background(), ResumeRequest{
		ThreadID:           "thread-claude-codex-fields",
		CodexHome:          aliasHome,
		CodexInstanceKey:   "raw-instance",
		CodexModelProvider: "raw-model-provider",
	}); err != nil {
		t.Fatalf("resumeSession() error = %v", err)
	}
}

func resumeCodexIdentityStores(
	t *testing.T,
	threadID string,
	runtimeConfig map[string]any,
	binding BindingRecord,
) (*stubThreadStore, *stubBindingStore) {
	t.Helper()
	if runtimeConfig == nil {
		runtimeConfig = map[string]any{}
	}
	runtimeConfig["legacyPromptSnapshotMigration"] = true
	providerThreadID := "019d5f6b-fb3c-7760-9d6f-54005553f70f"
	cwd := t.TempDir()
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       threadID,
		AgentID:        "agent-" + threadID,
		Prompt:         "resume",
		Model:          "stored-model",
		Cwd:            cwd,
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: runtimeConfig}),
	}}
	binding.AgentID = "agent-" + threadID
	if binding.Provider == "" {
		binding.Provider = "codex"
	}
	binding.ProviderThreadID = providerThreadID
	binding.CodexThreadID = threadID
	binding.RolloutPath = writeExistingProviderHistoryFile(t, providerThreadID, binding.Provider)
	binding.Cwd = cwd
	return threads, &stubBindingStore{binding: &binding}
}

func createCodexHomeSymlinkAlias(t *testing.T) (string, string) {
	t.Helper()
	realHome := t.TempDir()
	aliasHome := filepath.Join(t.TempDir(), "codex-home-link")
	if err := os.Symlink(realHome, aliasHome); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("os.Symlink requires Windows Developer Mode or SeCreateSymbolicLinkPrivilege; skipping symlink alias canonicalization test: %v", err)
		}
		t.Fatalf("create codex home symlink alias: %v", err)
	}
	canonicalHome, err := contract.CanonicalizeCodexHome(realHome)
	if err != nil {
		t.Fatalf("CanonicalizeCodexHome(real home) error = %v", err)
	}
	return canonicalHome, aliasHome
}

func createCleanCodexHomeAlias(t *testing.T) (string, string) {
	t.Helper()
	realHome := t.TempDir()
	aliasHome := realHome + string(os.PathSeparator) + "."
	canonicalHome, err := contract.CanonicalizeCodexHome(realHome)
	if err != nil {
		t.Fatalf("CanonicalizeCodexHome(real home) error = %v", err)
	}
	return canonicalHome, aliasHome
}

func assertResumeCodexIdentityCanonical(t *testing.T, req ResumeRequest, home string) {
	t.Helper()
	if req.CodexHome != home ||
		req.CodexInstanceKey != "default" ||
		req.CodexModelProvider != "openai" {
		t.Fatalf("resume codex identity = (%q,%q,%q), want (%q,%q,%q)",
			req.CodexHome,
			req.CodexInstanceKey,
			req.CodexModelProvider,
			home,
			"default",
			"openai")
	}
}

func assertResumeConfigCodexIdentity(t *testing.T, config map[string]any, home string) {
	t.Helper()
	if config[contract.CodexHomeKey] != home ||
		config[contract.CodexInstanceKeyKey] != "default" ||
		config[contract.CodexModelProviderKey] != "openai" {
		t.Fatalf("resume config codex identity = (%#v,%#v,%#v), want (%q,%q,%q)",
			config[contract.CodexHomeKey],
			config[contract.CodexInstanceKeyKey],
			config[contract.CodexModelProviderKey],
			home,
			"default",
			"openai")
	}
}

type launchHookThreadOrchestration struct {
	stubThreadOrchestration
	onLaunch func()
}

func (s *launchHookThreadOrchestration) LaunchAgent(ctx context.Context, req LaunchAgentRequest) error {
	if err := s.stubThreadOrchestration.LaunchAgent(ctx, req); err != nil {
		return err
	}
	if s.onLaunch != nil {
		s.onLaunch()
	}
	return nil
}
