package toolbridge

import (
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestOrchestrationToolAliasDenylistIncludesLegacyAndShortNames(t *testing.T) {
	want := contract.OrchestrationToolAliasDenylist()
	if got := OrchestrationToolAliasDenylist(); !slices.Equal(got, want) {
		t.Fatalf("OrchestrationToolAliasDenylist() = %#v, want %#v", got, want)
	}
}

func TestOrchestrationToolAliasRegistryDrivesCanonicalAndLegacyMapping(t *testing.T) {
	for _, alias := range orchestrationToolAliasesForTest(t) {
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

func TestLegacyOrchestrationAliasesAreDenyHiddenOnlyNotCallable(t *testing.T) {
	contractHiddenAliases := contract.OrchestrationToolHiddenAliases()
	for _, alias := range orchestrationToolAliasesForTest(t) {
		if got := callableLegacyCodexToolAliases(mcpdto.ClientKindOrch, alias.Canonical); len(got) != 0 {
			t.Fatalf("callableLegacyCodexToolAliases(orch, %q) = %#v, want none", alias.Canonical, got)
		}
		denyHidden := mcpSurfaceDenyAndHiddenAliases(mcpdto.ClientKindOrch, alias.Canonical)
		hiddenAlias := wrappedMCPToolName(mcpdto.ClientKindOrch, alias.LegacyPeerRealName)
		if !slices.Contains(contractHiddenAliases, hiddenAlias) {
			t.Fatalf("OrchestrationToolHiddenAliases() = %#v, missing %q", contractHiddenAliases, hiddenAlias)
		}
		for _, want := range []string{alias.LegacyPeerRealName, hiddenAlias} {
			if !slices.Contains(denyHidden, want) {
				t.Fatalf("mcpSurfaceDenyAndHiddenAliases(orch, %q) = %#v, missing %q", alias.Canonical, denyHidden, want)
			}
		}
		if slices.Contains(denyHidden, alias.Canonical) || slices.Contains(denyHidden, wrappedMCPToolName(mcpdto.ClientKindOrch, alias.Canonical)) {
			t.Fatalf("mcpSurfaceDenyAndHiddenAliases(orch, %q) = %#v, canonical callable names must stay out of legacy deny-hidden aliases", alias.Canonical, denyHidden)
		}
	}
}

func TestRequiresLegacyOrchSurfaceNameCoversUnknownLegacyPrefix(t *testing.T) {
	for _, legacy := range contract.OrchestrationToolLegacyPeerRealNames() {
		if !requiresLegacyOrchSurfaceName(legacy) {
			t.Fatalf("requiresLegacyOrchSurfaceName(%q) = false, want true", legacy)
		}
	}
	if !requiresLegacyOrchSurfaceName("orchestration_unknown") {
		t.Fatalf("requiresLegacyOrchSurfaceName(orchestration_unknown) = false, want true")
	}
	for _, name := range []string{"launch_agent", "mcp__orch__launch_agent", "unknown"} {
		if requiresLegacyOrchSurfaceName(name) {
			t.Fatalf("requiresLegacyOrchSurfaceName(%q) = true, want false", name)
		}
	}
}
