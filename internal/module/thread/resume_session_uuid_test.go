package thread

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

type recoveryTestHomeEntry struct {
	home  string
	token *byte
}

var recoveryTestHomeByPath sync.Map

func writeExistingProviderHistoryFile(t *testing.T, args ...string) string {
	t.Helper()
	identity := "00000000-0000-4000-8000-000000000001"
	provider := "codex"
	if len(args) > 0 {
		identity = args[0]
	}
	if len(args) > 1 {
		provider = args[1]
	}
	home := t.TempDir()
	if len(args) > 2 {
		home = args[2]
	}
	root := filepath.Join(home, "sessions", "2026", "07", "29")
	name := "rollout-test-" + identity + ".jsonl"
	content := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":%q}}\n", identity)
	if provider == "claude" {
		root = filepath.Join(home, "projects", "test-project")
		name = identity + ".jsonl"
		content = fmt.Sprintf("{\"sessionId\":%q,\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"test\"}]}}\n", identity)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create provider history root: %v", err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write provider history file: %v", err)
	}
	recoveryHome := home
	if strings.EqualFold(provider, "codex") {
		canonical, err := contract.CanonicalizeCodexHome(home)
		if err != nil {
			t.Fatalf("canonicalize recovery test Codex home: %v", err)
		}
		recoveryHome = canonical
	}
	entry := recoveryTestHomeEntry{home: recoveryHome, token: new(byte)}
	recoveryTestHomeByPath.Store(path, entry)
	t.Cleanup(func() {
		recoveryTestHomeByPath.CompareAndDelete(path, entry)
	})
	return path
}

func authorizeRecoveryTestBinding(binding *BindingRecord) {
	if binding == nil {
		return
	}
	raw, ok := recoveryTestHomeByPath.Load(binding.RolloutPath)
	if !ok {
		return
	}
	entry, ok := raw.(recoveryTestHomeEntry)
	if !ok {
		panic("recovery test home entry has invalid type")
	}
	if binding.ProviderRecoveryHome == "" {
		binding.ProviderRecoveryHome = entry.home
	}
}

func TestRecoveryTestHomeEntryLifecycleScopesFixtureAuthorization(t *testing.T) {
	var cleanPath string
	t.Run("writer registers canonical codex home and cleanup removes it", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "nested", "..", "codex-home")
		cleanPath = writeExistingProviderHistoryFile(t, "00000000-0000-4000-8000-000000000010", "codex", home)
		raw, ok := recoveryTestHomeByPath.Load(cleanPath)
		if !ok {
			t.Fatal("writer did not register recovery test home")
		}
		entry, ok := raw.(recoveryTestHomeEntry)
		if !ok || entry.token == nil {
			t.Fatalf("recovery test home entry = %#v, want tokenized entry", raw)
		}
		wantHome, err := contract.CanonicalizeCodexHome(home)
		if err != nil {
			t.Fatalf("canonicalize expected Codex home: %v", err)
		}
		if entry.home != wantHome {
			t.Fatalf("writer recovery home = %q, want %q", entry.home, wantHome)
		}
		binding := &BindingRecord{Provider: "codex", RolloutPath: cleanPath}
		authorizeRecoveryTestBinding(binding)
		if binding.ProviderRecoveryHome != wantHome {
			t.Fatalf("authorized recovery home = %q, want %q", binding.ProviderRecoveryHome, wantHome)
		}
	})
	if _, ok := recoveryTestHomeByPath.Load(cleanPath); ok {
		t.Fatal("writer cleanup retained recovery test home entry")
	}

	var replacementPath string
	replacement := recoveryTestHomeEntry{home: "replacement-home", token: new(byte)}
	t.Run("writer cleanup does not delete newer entry", func(t *testing.T) {
		replacementPath = writeExistingProviderHistoryFile(t, "00000000-0000-4000-8000-000000000011")
		recoveryTestHomeByPath.Store(replacementPath, replacement)
	})
	raw, ok := recoveryTestHomeByPath.Load(replacementPath)
	if !ok || raw != replacement {
		t.Fatalf("replacement recovery test home entry = %#v, want %#v", raw, replacement)
	}
	if !recoveryTestHomeByPath.CompareAndDelete(replacementPath, replacement) {
		t.Fatal("cleanup replacement recovery test home entry")
	}
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
	rolloutPath := writeExistingProviderHistoryFile(t, realUUID, "claude")
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
	rolloutPath := writeExistingProviderHistoryFile(t, realUUID)
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
