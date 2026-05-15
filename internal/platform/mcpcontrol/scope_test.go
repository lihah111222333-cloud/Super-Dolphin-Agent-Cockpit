package mcpcontrol

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestFindActiveForScopeRoutesByAgentThread(t *testing.T) {
	registry := NewRegistry()
	alpha := addActiveScopeTestPeer(registry, "alpha", dto.ClientKindLSP, "agent-a", "thread-a")
	_ = addActiveScopeTestPeer(registry, "beta", dto.ClientKindLSP, "agent-b", "thread-b")

	got := registry.FindActiveForScope(ToolScope{
		Family:   dto.ClientKindLSP,
		AgentID:  "agent-a",
		ThreadID: "thread-a",
	})
	if len(got) != 1 || got[0] != alpha {
		t.Fatalf("FindActiveForScope() = %#v, want alpha only", got)
	}
}

func TestFindActiveForScopeRelaxesToAgent(t *testing.T) {
	registry := NewRegistry()
	alpha := addActiveScopeTestPeer(registry, "alpha", dto.ClientKindLSP, "agent-a", "thread-a")
	_ = addActiveScopeTestPeer(registry, "beta", dto.ClientKindLSP, "agent-b", "thread-b")

	got := registry.FindActiveForScope(ToolScope{
		Family:  dto.ClientKindLSP,
		AgentID: "agent-a",
	})
	if len(got) != 1 || got[0] != alpha {
		t.Fatalf("FindActiveForScope() = %#v, want agent-a only", got)
	}
}

func TestFindActiveForScopeReturnsCandidatesForAmbiguousFallback(t *testing.T) {
	registry := NewRegistry()
	_ = addActiveScopeTestPeer(registry, "alpha", dto.ClientKindLSP, "agent-a", "thread-a")
	_ = addActiveScopeTestPeer(registry, "beta", dto.ClientKindLSP, "agent-b", "thread-b")

	got := registry.FindActiveForScope(ToolScope{Family: dto.ClientKindLSP})
	if len(got) != 2 {
		t.Fatalf("FindActiveForScope() len = %d, want 2 ambiguous candidates", len(got))
	}
}

func TestFindActiveForScopeSingletonFallback(t *testing.T) {
	registry := NewRegistry()
	only := addActiveScopeTestPeer(registry, "only", dto.ClientKindLSP, "agent-a", "thread-a")

	got := registry.FindActiveForScope(ToolScope{
		Family:   dto.ClientKindLSP,
		AgentID:  "missing-agent",
		ThreadID: "missing-thread",
	})
	if len(got) != 1 || got[0] != only {
		t.Fatalf("FindActiveForScope() = %#v, want singleton fallback", got)
	}
}

func addActiveScopeTestPeer(registry *ToolRegistry, instanceID, clientKind, agentID, threadID string) *ToolInstance {
	inst := &ToolInstance{
		Lease:      LeaseKey{InstanceID: instanceID, Generation: 1},
		AgentID:    agentID,
		ThreadID:   threadID,
		ClientKind: clientKind,
		Status:     dto.StatusActive,
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.instances[inst.Lease] = inst
	registry.indexLocked(inst)
	return inst
}
