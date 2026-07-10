package thread

import (
	"context"
	"testing"
)

func TestThreadAgents_CleanupOnStop(t *testing.T) {
	svc := &service{
		bindingStore:  &stubThreadBindingStore{binding: &BindingRecord{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "provider-thread-1", CodexThreadID: "thread-1"}},
		threadStore:   &stubThreadStore{thread: &ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Status: statusCreated}},
		sessions:      &stubThreadSessions{agentID: "agent-1", session: &stubThreadSession{threadID: "thread-1"}},
		orchestration: &stubThreadOrchestration{},
		threadAgents:  map[string]string{"agent-1": "agent-1", "thread-1": "agent-1", "provider-thread-1": "agent-1"},
	}
	if err := svc.Stop(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	for _, id := range []string{"agent-1", "thread-1", "provider-thread-1"} {
		if got := svc.lookupThreadAgent(id); got != "" {
			t.Fatalf("lookupThreadAgent(%q) = %q, want empty", id, got)
		}
	}
}
