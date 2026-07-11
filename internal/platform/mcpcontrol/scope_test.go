package mcpcontrol

import (
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
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

func TestFindActiveForScopeRejectsUnrelatedAgentPeer(t *testing.T) {
	registry := NewRegistry()
	_ = addActiveScopeTestPeer(registry, "alpha", dto.ClientKindLSP, "agent-a", "thread-a")
	_ = addActiveScopeTestPeer(registry, "beta", dto.ClientKindLSP, "agent-b", "thread-b")

	got := registry.FindActiveForScope(ToolScope{
		Family:   dto.ClientKindLSP,
		AgentID:  "agent-missing",
		ThreadID: "thread-missing",
	})
	if len(got) != 0 {
		t.Fatalf("FindActiveForScope() = %#v, want fail-closed no unrelated agent fallback", got)
	}
}

func TestPeerFallbackRejectsUnrelatedAgentPeer(t *testing.T) {
	registry := NewRegistry()
	_ = addActiveScopeTestPeer(registry, "agent-a-peer", dto.ClientKindLSP, "agent-a", "thread-a")
	_ = addActiveScopeTestPeer(registry, "agent-b-peer", dto.ClientKindLSP, "agent-b", "thread-b")

	got := registry.FindActiveForScope(ToolScope{
		Family:   dto.ClientKindLSP,
		AgentID:  "agent-c",
		ThreadID: "thread-c",
	})
	if len(got) != 0 {
		t.Fatalf("FindActiveForScope() = %#v, want no unrelated agent peer fallback", got)
	}
}

func TestFindActiveForScopeRejectsSingletonUnrelatedFallback(t *testing.T) {
	registry := NewRegistry()
	_ = addActiveScopeTestPeer(registry, "only", dto.ClientKindLSP, "agent-a", "thread-a")

	got := registry.FindActiveForScope(ToolScope{
		Family:   dto.ClientKindLSP,
		AgentID:  "missing-agent",
		ThreadID: "missing-thread",
	})
	if len(got) != 0 {
		t.Fatalf("FindActiveForScope() = %#v, want fail-closed no singleton unrelated fallback", got)
	}
}

func TestPeerFallbackAllowsExplicitSharedPeerOnly(t *testing.T) {
	registry := NewRegistry()
	shared := addActiveScopeTestPeer(registry, "shared", dto.ClientKindLSP, "", "")
	shared.PeerKind = dto.PeerKindSharedService
	shared.Shared = true
	_ = addActiveScopeTestPeer(registry, "agent-b", dto.ClientKindLSP, "agent-b", "thread-b")

	got := registry.FindActiveForScope(ToolScope{
		Family:   dto.ClientKindLSP,
		AgentID:  "missing-agent",
		ThreadID: "missing-thread",
	})
	if len(got) != 1 || got[0] != shared {
		t.Fatalf("FindActiveForScope() = %#v, want explicit shared peer only", got)
	}
}

func TestSharedPeerRequiresRegistrySharedFlagAndPeerKind(t *testing.T) {
	registry := NewRegistry()
	kindOnly := addActiveScopeTestPeer(registry, "kind-only", dto.ClientKindLSP, "", "")
	kindOnly.PeerKind = dto.PeerKindSharedService
	sharedFlagOnly := addActiveScopeTestPeer(registry, "shared-flag-only", dto.ClientKindLSP, "", "")
	sharedFlagOnly.Shared = true
	valid := addActiveScopeTestPeer(registry, "valid", dto.ClientKindLSP, "", "")
	valid.PeerKind = dto.PeerKindSharedService
	valid.Shared = true

	got := registry.FindActiveForScope(ToolScope{
		Family:   dto.ClientKindLSP,
		AgentID:  "missing-agent",
		ThreadID: "missing-thread",
	})
	if len(got) != 1 || got[0] != valid {
		t.Fatalf("FindActiveForScope() = %#v, want only peer with shared flag and shared-service kind", got)
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
