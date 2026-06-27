package thread

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestServiceForkPreservesSourceCodexIdentity(t *testing.T) {
	t.Parallel()

	const (
		instanceKey   = "glm"
		modelProvider = "openai-compatible-glm"
	)
	codexHome := t.TempDir()
	wantCodexHome := canonicalCodexHomeForTest(t, codexHome)
	originalSession := &stubSession{threadID: "thread-parent", forkResult: dto.ForkResult{NewThreadID: "thread-fork"}}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	bindings.binding.CodexHome = codexHome
	bindings.binding.CodexInstanceKey = instanceKey
	bindings.binding.CodexModelProvider = modelProvider
	threads := forkParentThreadStore()
	threads.thread.ConfigOverride = mustStoredThreadConfigRaw(t, storedThreadConfig{Runtime: map[string]any{
		contract.CodexHomeKey:          codexHome,
		contract.CodexInstanceKeyKey:   instanceKey,
		contract.CodexModelProviderKey: modelProvider,
	}})
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.CodexHome != wantCodexHome || req.CodexInstanceKey != instanceKey || req.CodexModelProvider != modelProvider {
			t.Fatalf("fork resume codex identity = (%q,%q,%q), want source identity", req.CodexHome, req.CodexInstanceKey, req.CodexModelProvider)
		}
		if req.Config[contract.CodexHomeKey] != wantCodexHome || req.Config[contract.CodexInstanceKeyKey] != instanceKey || req.Config[contract.CodexModelProviderKey] != modelProvider {
			t.Fatalf("fork resume config identity = %#v, want source identity", req.Config)
		}
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &forkOrchestrationStub{}, nil).(*service)

	result, err := svc.Fork(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if result.KickoffState != "created_only" {
		t.Fatalf("KickoffState = %q, want created_only", result.KickoffState)
	}
	assertPersistedCodexIdentity(t, bindings.upsert, codexHome, instanceKey, modelProvider)
	assertStoredRuntimeCodexIdentity(t, threads.upsert.ConfigOverride, codexHome, instanceKey, modelProvider)
}

func TestServiceForkRejectsPartialSourceCodexIdentity(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{threadID: "thread-parent", forkResult: dto.ForkResult{NewThreadID: "thread-fork"}}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	bindings.binding.CodexHome = t.TempDir()
	threads := forkParentThreadStore()
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when source codex identity is partial")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Fork(context.Background(), "thread-parent")
	if !errors.Is(err, contract.ErrCodexInstanceKeyRequired) {
		t.Fatalf("Fork() error = %v, want %v", err, contract.ErrCodexInstanceKeyRequired)
	}
	if originalSession.forkRequest.ThreadID != "" {
		t.Fatalf("ForkThread request = %#v, want no provider fork before identity validation", originalSession.forkRequest)
	}
	if orch.launch.AgentID != "" || threads.upsertCount != 0 || len(bindings.upserts) != 0 {
		t.Fatalf("side effects launch=%#v upserts=%d bindings=%d, want none", orch.launch, threads.upsertCount, len(bindings.upserts))
	}
}
