package thread

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestPhase45BaseInstructionsStayOutOfPromptStorage(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		if req.Instructions != "system prompt" {
			t.Fatalf("instructions = %q, want system prompt", req.Instructions)
		}
		session := &stubSession{threadID: "provider-thread-base"}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:          "agent-base",
		Provider:         "codex",
		BaseInstructions: "system prompt",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "" {
		t.Fatalf("launch name = %q, want empty", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "" {
		t.Fatalf("persisted prompt = %q, want empty", threads.upsert.Prompt)
	}
}

func TestPhase45ResumeForwardsPromptSnapshot(t *testing.T) {
	t.Parallel()

	snapshot := contract.PromptAssemblySnapshot{
		DisplayName:           "resume name",
		BaseInstructions:      "snapshot base",
		DeveloperInstructions: "snapshot dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  "snapshot-hash",
	}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume name",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		if req.PromptSnapshot.DisplayName != snapshot.DisplayName ||
			req.PromptSnapshot.BaseInstructions != snapshot.BaseInstructions ||
			req.PromptSnapshot.DeveloperInstructions != snapshot.DeveloperInstructions ||
			req.PromptSnapshot.Provider != snapshot.Provider ||
			req.PromptSnapshot.Version != snapshot.Version ||
			req.PromptSnapshot.Hash != snapshot.Hash {
			t.Fatalf("PromptSnapshot = %#v, want %#v", req.PromptSnapshot, snapshot)
		}
		session := &stubSession{threadID: "provider-thread-1"}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID:       "thread-1",
		PromptSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.SessionID != "provider-thread-1" || result.Status != "resumed" {
		t.Fatalf("Resume() result = %#v, want provider-thread-1/resumed", result)
	}
}

func TestPhase45ExplicitNameWinsOverLegacyPromptFallback(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		if req.Instructions != "assembled system" {
			t.Fatalf("instructions = %q, want assembled system", req.Instructions)
		}
		if req.StartAssembly.DisplayName != "clean name" {
			t.Fatalf("start assembly display name = %q, want clean name", req.StartAssembly.DisplayName)
		}
		session := &stubSession{threadID: "provider-thread-name"}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-name",
		Provider: "codex",
		Name:     "clean name",
		Prompt:   "legacy prompt",
		PromptAssemblyRef: promptAssemblyStub{startAssembly: contract.StartAssembly{
			BaseInstructions:      "assembled system",
			DeveloperInstructions: "assembled dev",
		}},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "clean name" {
		t.Fatalf("launch name = %q, want clean name", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "clean name" {
		t.Fatalf("persisted prompt = %q, want clean name", threads.upsert.Prompt)
	}
}
