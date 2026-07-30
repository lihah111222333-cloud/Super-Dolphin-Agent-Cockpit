package thread

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
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

func TestPersistStartedSessionRejectsPartialRuntimeCodexIdentity(t *testing.T) {
	t.Parallel()

	const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f701"
	explicitHome := t.TempDir()
	runtimeHome := t.TempDir()
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	svc := &service{threadStore: threads, bindingStore: bindings, logger: silentLogger()}
	session := &stubSession{
		threadID: providerUUID,
		runtimeConfig: map[string]any{
			"codexHome": runtimeHome,
		},
	}
	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:      "SQLite resume identity",
		BaseInstructions: "base",
	}, "codex")

	_, err := svc.persistStartedSession(context.Background(), StartRequest{
		Provider: "codex",
		AgentID:  "agent-partial-runtime-identity",
		CWD:      t.TempDir(),
		Config: map[string]any{
			"codexHome":          explicitHome,
			"codexInstanceKey":   "default",
			"codexModelProvider": "openai",
		},
	}, contract.StartInput{
		Provider: "codex",
		CWD:      t.TempDir(),
	}, assembly, "agent-partial-runtime-identity", "SQLite resume identity", session)
	if !errors.Is(err, contract.ErrCodexInstanceKeyRequired) {
		t.Fatalf("persistStartedSession() error = %v, want %v", err, contract.ErrCodexInstanceKeyRequired)
	}
	if threads.upsertCount != 0 || len(bindings.upserts) != 0 {
		t.Fatalf("partial runtime identity persisted thread upserts=%d binding upserts=%d, want none", threads.upsertCount, len(bindings.upserts))
	}
}

func TestPersistStartedSessionRejectsInvalidRuntimeCodexIdentity(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		value     any
		wantError error
	}{
		{name: "non-string", value: 123, wantError: contract.ErrCodexIdentityInvalidType},
		{name: "empty-string", value: "", wantError: contract.ErrCodexHomeRequired},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f704"
			explicitHome := t.TempDir()
			threads := &stubThreadStore{}
			bindings := &stubBindingStore{}
			svc := &service{threadStore: threads, bindingStore: bindings, logger: silentLogger()}
			session := &stubSession{
				threadID: providerUUID,
				runtimeConfig: map[string]any{
					"codexHome": tc.value,
				},
			}
			assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
				DisplayName:      "SQLite resume identity",
				BaseInstructions: "base",
			}, "codex")

			_, err := svc.persistStartedSession(context.Background(), StartRequest{
				Provider: "codex",
				AgentID:  "agent-invalid-runtime-identity",
				CWD:      t.TempDir(),
				Config: map[string]any{
					"codexHome":          explicitHome,
					"codexInstanceKey":   "default",
					"codexModelProvider": "openai",
				},
			}, contract.StartInput{
				Provider: "codex",
				CWD:      t.TempDir(),
			}, assembly, "agent-invalid-runtime-identity", "SQLite resume identity", session)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("persistStartedSession() error = %v, want %v", err, tc.wantError)
			}
			if threads.upsertCount != 0 || len(bindings.upserts) != 0 {
				t.Fatalf("invalid runtime identity persisted thread upserts=%d binding upserts=%d, want none", threads.upsertCount, len(bindings.upserts))
			}
		})
	}
}

func TestResumeUsesStoredRuntimeCodexIdentityWhenBindingIdentityIsEmpty(t *testing.T) {
	t.Parallel()

	const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f702"
	codexHome := t.TempDir()
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID: "thread-runtime-resume",
		AgentID:  "agent-runtime-resume",
		Prompt:   "SQLite resume identity",
		Model:    "gpt-5.5",
		Cwd:      t.TempDir(),
		Status:   statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Runtime: map[string]any{
				"codexHome":                     codexHome,
				"codexInstanceKey":              "default",
				"codexModelProvider":            "openai",
				"legacyPromptSnapshotMigration": true,
			},
		}),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:       "agent-runtime-resume",
		Provider:      "codex",
		CodexThreadID: "thread-runtime-resume",
		RolloutPath:   writeExistingProviderHistoryFile(t, providerUUID, "codex", codexHome),
		SessionUUID:   providerUUID,
		Cwd:           threads.thread.Cwd,
	}}
	wantCodexHome := canonicalCodexHomeForTest(t, codexHome)
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.CodexHome != wantCodexHome ||
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

func TestResumeRejectsHydratedPartialCodexIdentityBeforeProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		runtime   map[string]any
		binding   BindingRecord
		wantError error
	}{
		{
			name: "state-home-only",
			binding: BindingRecord{
				CodexHome: t.TempDir(),
			},
			wantError: contract.ErrCodexInstanceKeyRequired,
		},
		{
			name: "stored-runtime-home-only",
			runtime: map[string]any{
				contract.CodexHomeKey: t.TempDir(),
			},
			wantError: contract.ErrCodexInstanceKeyRequired,
		},
		{
			name: "stored-runtime-invalid-type",
			runtime: map[string]any{
				contract.CodexHomeKey: 123,
			},
			wantError: contract.ErrCodexIdentityInvalidType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			threadID := "thread-hydrated-partial-" + tc.name
			threads, bindings := resumeCodexIdentityStores(t, threadID, tc.runtime, tc.binding)
			starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
				t.Fatalf("ResumeSession should not be called when hydrated codex identity is partial")
				return nil, nil
			}}
			svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, &stubThreadOrchestration{}, nil).(*service)

			_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: threadID})
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("Resume() error = %v, want %v", err, tc.wantError)
			}
			if threads.upsertCount != 0 || len(bindings.upserts) != 0 {
				t.Fatalf("hydrated partial identity persisted thread upserts=%d binding upserts=%d, want none", threads.upsertCount, len(bindings.upserts))
			}
		})
	}
}

func TestPersistResumedSessionRejectsRuntimeCodexIdentityBeforeReqOrStateFallback(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		runtime   map[string]any
		wantError error
	}{
		{name: "invalid-home-type", runtime: map[string]any{"codexHome": 123}, wantError: contract.ErrCodexIdentityInvalidType},
		{name: "empty-home", runtime: map[string]any{"codexHome": ""}, wantError: contract.ErrCodexHomeRequired},
		{name: "partial-missing-provider", runtime: map[string]any{
			"codexHome":        t.TempDir(),
			"codexInstanceKey": "runtime-instance",
		}, wantError: contract.ErrCodexModelProviderRequired},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f705"
			requestHome := t.TempDir()
			stateHome := t.TempDir()
			threads := &stubThreadStore{}
			bindings := &stubBindingStore{}
			svc := &service{threadStore: threads, bindingStore: bindings, logger: silentLogger()}
			rawConfig := mustStoredThreadConfigRaw(t, storedThreadConfig{
				Runtime: map[string]any{
					"codexHome":          stateHome,
					"codexInstanceKey":   "state-instance",
					"codexModelProvider": "state-provider",
				},
			})
			session := &stubSession{
				threadID:      providerUUID,
				runtimeConfig: tc.runtime,
			}

			_, err := svc.persistResumedSession(context.Background(), ResumeRequest{
				Provider:           "codex",
				AgentID:            "agent-resume-invalid-runtime",
				ThreadID:           "thread-resume-invalid-runtime",
				CWD:                t.TempDir(),
				CodexHome:          requestHome,
				CodexInstanceKey:   "request-instance",
				CodexModelProvider: "request-provider",
			}, resumeState{
				AgentID:            "agent-resume-invalid-runtime",
				PublicThreadID:     "thread-resume-invalid-runtime",
				Provider:           "codex",
				ProviderThreadID:   providerUUID,
				SessionUUID:        providerUUID,
				ConfigOverrideRaw:  rawConfig,
				CWD:                t.TempDir(),
				CodexHome:          stateHome,
				CodexInstanceKey:   "state-instance",
				CodexModelProvider: "state-provider",
			}, "SQLite resume identity", session)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("persistResumedSession() error = %v, want %v", err, tc.wantError)
			}
			if threads.upsertCount != 0 || len(bindings.upserts) != 0 {
				t.Fatalf("invalid runtime identity persisted thread upserts=%d binding upserts=%d, want none", threads.upsertCount, len(bindings.upserts))
			}
		})
	}
}

func TestPersistResumedSessionCanonicalizesStoredCodexIdentity(t *testing.T) {
	t.Parallel()

	const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f703"
	realHome, aliasHome := createCodexHomeSymlinkAlias(t)
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	svc := &service{threadStore: threads, bindingStore: bindings, logger: silentLogger()}
	rawConfig := mustStoredThreadConfigRaw(t, storedThreadConfig{
		Runtime: map[string]any{
			"codexHome":          aliasHome,
			"codexInstanceKey":   "default",
			"codexModelProvider": "openai",
		},
	})
	session := &stubSession{
		threadID: providerUUID,
		runtimeConfig: map[string]any{
			"codexHome":          aliasHome,
			"codexInstanceKey":   "default",
			"codexModelProvider": "openai",
		},
	}

	_, err := svc.persistResumedSession(context.Background(), ResumeRequest{
		Provider: "codex",
		AgentID:  "agent-resume-canonical",
		ThreadID: "thread-resume-canonical",
		CWD:      t.TempDir(),
	}, resumeState{
		AgentID:            "agent-resume-canonical",
		PublicThreadID:     "thread-resume-canonical",
		Provider:           "codex",
		ProviderThreadID:   providerUUID,
		SessionUUID:        providerUUID,
		ConfigOverrideRaw:  rawConfig,
		CWD:                t.TempDir(),
		CodexHome:          aliasHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}, "SQLite resume identity", session)
	if err != nil {
		t.Fatalf("persistResumedSession() error = %v, want canonicalized codex identity", err)
	}
	assertPersistedCodexIdentity(t, bindings.upsert, realHome, "default", "openai")
	assertStoredRuntimeCodexIdentity(t, threads.upsert.ConfigOverride, realHome, "default", "openai")
}

func assertPersistedCodexIdentity(t *testing.T, got BindingUpsert, home, instanceKey, modelProvider string) {
	t.Helper()
	home = canonicalCodexHomeForTest(t, home)
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
	home = canonicalCodexHomeForTest(t, home)
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

func canonicalCodexHomeForTest(t *testing.T, home string) string {
	t.Helper()
	canonical, err := contract.CanonicalizeCodexHome(home)
	if err != nil {
		t.Fatalf("CanonicalizeCodexHome(%q) error = %v", home, err)
	}
	return canonical
}
