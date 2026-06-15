package thread

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestPersistStartedSessionUsesRuntimeCodexIdentityWhenStartConfigIsPartial(t *testing.T) {
	t.Parallel()

	const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f700"
	codexHome := t.TempDir()
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	svc := &service{threadStore: threads, bindingStore: bindings, logger: silentLogger()}
	session := &stubSession{
		threadID: providerUUID,
		runtimeConfig: map[string]any{
			"codexHome":          codexHome,
			"codexInstanceKey":   "default",
			"codexModelProvider": "openai",
		},
	}
	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:      "SQLite resume identity",
		BaseInstructions: "base",
	}, "codex")

	_, err := svc.persistStartedSession(context.Background(), StartRequest{
		Provider: "codex",
		AgentID:  "agent-runtime-identity",
		CWD:      t.TempDir(),
		Config: map[string]any{
			"codexHome":        codexHome,
			"codexInstanceKey": "default",
		},
	}, contract.StartInput{
		Provider: "codex",
		CWD:      t.TempDir(),
	}, assembly, "agent-runtime-identity", "SQLite resume identity", session)
	if err != nil {
		t.Fatalf("persistStartedSession() error = %v, want runtime codex identity to complete partial start config", err)
	}
	assertPersistedCodexIdentity(t, bindings.upsert, codexHome, "default", "openai")
	assertStoredRuntimeCodexIdentity(t, threads.upsert.ConfigOverride, codexHome, "default", "openai")
}

func TestResumeUsesStoredRuntimeCodexIdentityWhenBindingIdentityIsEmpty(t *testing.T) {
	t.Parallel()

	const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f702"
	codexHome := t.TempDir()
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID: "thread-runtime-resume",
		AgentID:  "agent-runtime-resume",
		Prompt:   "SQLite resume identity",
		Model:    "gpt-5.5",
		Cwd:      t.TempDir(),
		Status:   statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Runtime: map[string]any{
				"codexHome":          codexHome,
				"codexInstanceKey":   "default",
				"codexModelProvider": "openai",
			},
		}),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:       "agent-runtime-resume",
		Provider:      "codex",
		CodexThreadID: "thread-runtime-resume",
		SessionUUID:   providerUUID,
		Cwd:           threads.thread.Cwd,
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.CodexHome != codexHome ||
			req.CodexInstanceKey != "default" ||
			req.CodexModelProvider != "openai" {
			t.Fatalf("resume codex identity = (%q,%q,%q), want stored runtime identity",
				req.CodexHome,
				req.CodexInstanceKey,
				req.CodexModelProvider)
		}
		session := &stubSession{
			threadID: providerUUID,
			runtimeConfig: map[string]any{
				"codexHome":          codexHome,
				"codexInstanceKey":   "default",
				"codexModelProvider": "openai",
			},
		}
		sessions.session = session
		return session, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)

	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-runtime-resume"}); err != nil {
		t.Fatalf("Resume() error = %v, want stored runtime codex identity to repair empty binding identity", err)
	}
	assertPersistedCodexIdentity(t, bindings.upsert, codexHome, "default", "openai")
}

func assertPersistedCodexIdentity(t *testing.T, got bindingstore.UpsertParams, home, instanceKey, modelProvider string) {
	t.Helper()
	if got.CodexHome != home ||
		got.CodexInstanceKey != instanceKey ||
		got.CodexModelProvider != modelProvider {
		t.Fatalf("persisted codex identity = (%q,%q,%q), want (%q,%q,%q)",
			got.CodexHome,
			got.CodexInstanceKey,
			got.CodexModelProvider,
			home,
			instanceKey,
			modelProvider)
	}
}

func assertStoredRuntimeCodexIdentity(t *testing.T, raw json.RawMessage, home, instanceKey, modelProvider string) {
	t.Helper()
	stored, err := decodeStoredThreadConfig(raw)
	if err != nil {
		t.Fatalf("decodeStoredThreadConfig() error = %v", err)
	}
	runtime := stored.Runtime
	if runtime["codexHome"] != home ||
		runtime["codexInstanceKey"] != instanceKey ||
		runtime["codexModelProvider"] != modelProvider {
		t.Fatalf("stored runtime codex identity = (%#v,%#v,%#v), want (%q,%q,%q)",
			runtime["codexHome"],
			runtime["codexInstanceKey"],
			runtime["codexModelProvider"],
			home,
			instanceKey,
			modelProvider)
	}
}
