package toolbridge

import (
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestOrchestrationToolAliasDenylistIncludesLegacyAndShortNames(t *testing.T) {
	want := contract.OrchestrationToolAliasDenylist()
	if got := OrchestrationToolAliasDenylist(); !slices.Equal(got, want) {
		t.Fatalf("OrchestrationToolAliasDenylist() = %#v, want %#v", got, want)
	}
}

func TestOrchestrationToolAliasRegistryDrivesCanonicalAndLegacyMapping(t *testing.T) {
	for _, alias := range contract.OrchestrationToolAliases() {
		if got := legacyOrchPeerRealName(alias.Canonical); got != alias.LegacyPeerRealName {
			t.Fatalf("legacyOrchPeerRealName(%q) = %q, want %q", alias.Canonical, got, alias.LegacyPeerRealName)
		}
		if got := canonicalOrchSurfaceName(alias.LegacyPeerRealName); got != alias.Canonical {
			t.Fatalf("canonicalOrchSurfaceName(%q) = %q, want %q", alias.LegacyPeerRealName, got, alias.Canonical)
		}
		if !isLegacyOrchPeerRealName(alias.LegacyPeerRealName) {
			t.Fatalf("isLegacyOrchPeerRealName(%q) = false, want true", alias.LegacyPeerRealName)
		}
	}
}

func TestRequiresLegacyOrchSurfaceNameCoversUnknownLegacyPrefix(t *testing.T) {
	for _, name := range []string{"orchestration_launch_agent", "orchestration_unknown"} {
		if !requiresLegacyOrchSurfaceName(name) {
			t.Fatalf("requiresLegacyOrchSurfaceName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"launch_agent", "mcp__orch__launch_agent", "unknown"} {
		if requiresLegacyOrchSurfaceName(name) {
			t.Fatalf("requiresLegacyOrchSurfaceName(%q) = true, want false", name)
		}
	}
}
