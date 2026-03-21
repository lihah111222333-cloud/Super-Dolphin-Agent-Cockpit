package thread

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestServiceForkCreatesIndependentAgentAndBinding(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "thread-fork"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "thread-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-parent",
		Prompt:    "Forked Thread",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
	}}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.Provider != "codex" {
				t.Fatalf("Provider = %q, want codex", req.Provider)
			}
			if req.AgentID != "thread-fork" {
				t.Fatalf("AgentID = %q, want thread-fork", req.AgentID)
			}
			if req.ThreadID != "thread-fork" {
				t.Fatalf("ThreadID = %q, want thread-fork", req.ThreadID)
			}
			if req.Model != "gpt-5.4" {
				t.Fatalf("Model = %q, want gpt-5.4", req.Model)
			}
			sessions.session = forkedSession
			return forkedSession, nil
		},
	}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Fork(context.Background(), "thread-parent")
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if result.NewThreadID != "thread-fork" {
		t.Fatalf("Fork() result = %#v, want thread-fork", result)
	}
	if originalSession.forkRequest.ThreadID != "thread-parent" {
		t.Fatalf("forkRequest.ThreadID = %q, want thread-parent", originalSession.forkRequest.ThreadID)
	}
	if orch.launch.AgentID != "thread-fork" {
		t.Fatalf("launch.AgentID = %q, want thread-fork", orch.launch.AgentID)
	}
	if orch.launch.Cwd != "/repo" || orch.launch.Name != "Forked Thread" {
		t.Fatalf("launch = %#v", orch.launch)
	}
	if bindings.upsert.AgentID != "thread-fork" {
		t.Fatalf("binding.AgentID = %q, want thread-fork", bindings.upsert.AgentID)
	}
	if bindings.upsert.ProviderThreadID != "thread-fork" || bindings.upsert.CodexThreadID != "thread-fork" {
		t.Fatalf("binding upsert = %#v", bindings.upsert)
	}
	if threads.upsert.ThreadID != "thread-fork" || threads.upsert.OwnerThreadID != "thread-parent" {
		t.Fatalf("thread upsert = %#v", threads.upsert)
	}
}

type forkOrchestrationStub struct {
	launch LaunchAgentRequest
}

func (s *forkOrchestrationStub) LaunchAgent(_ context.Context, req LaunchAgentRequest) error {
	s.launch = req
	return nil
}

func (s *forkOrchestrationStub) StopAgent(context.Context, string) error { return nil }

func (s *forkOrchestrationStub) Recover(context.Context, string) error { return nil }

func (s *forkOrchestrationStub) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}
