package toolbridge

import "testing"

func TestRevokeExpectedCodexToolSurfaceRemovesNonOverlappingOldKeys(t *testing.T) {
	oldClient := &ownershipMCPClient{}
	failedClient := &ownershipMCPClient{}
	oldSurface := &codexToolSurface{
		keys:    []string{"agent-1", "thread-old"},
		clients: []mcpClient{oldClient},
	}
	failedSurface := &codexToolSurface{
		keys:     []string{"agent-1", "thread-new"},
		expected: map[string]*codexToolSurface{"agent-1": oldSurface},
		clients:  []mcpClient{failedClient},
	}
	newerSurface := &codexToolSurface{keys: []string{"thread-new"}}
	h := &Handler{surfaces: map[string]*codexToolSurface{
		"agent-1":    oldSurface,
		"thread-old": oldSurface,
		"thread-new": newerSurface,
	}}

	if err := h.revokeExpectedCodexToolSurface(failedSurface); err != nil {
		t.Fatalf("revokeExpectedCodexToolSurface() error = %v", err)
	}
	if h.surfaces["agent-1"] != nil || h.surfaces["thread-old"] != nil {
		t.Fatalf("closed old surface remains indexed: %#v", h.surfaces)
	}
	if got := h.surfaces["thread-new"]; got != newerSurface {
		t.Fatalf("newer surface = %p, want %p", got, newerSurface)
	}
	for key, surface := range h.surfaces {
		if surface == oldSurface || surface == failedSurface {
			t.Fatalf("key %q retains revoked surface %p", key, surface)
		}
	}
	if oldClient.closeCall != 1 || failedClient.closeCall != 1 {
		t.Fatalf("close counts = old:%d failed:%d, want 1 each", oldClient.closeCall, failedClient.closeCall)
	}
}
