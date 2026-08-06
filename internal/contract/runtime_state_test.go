package contract

import "testing"

func TestRuntimeStateSnapshotsAreIndependent(t *testing.T) {
	tests := []struct {
		name string
		get  func() []string
	}{
		{name: "required manifest environment", get: mcpRequiredEnvKeys},
		{name: "passthrough manifest environment", get: mcpPassthroughEnvKeys},
		{name: "orchestration tools", get: orchestrationToolCanonicalNames},
		{name: "preferred user context", get: preferredUserContextKeys},
		{name: "read-only non-orchestration tools", get: readOnlyNonOrchestrationDeniedTools},
		{name: "known codex native tools", get: knownCodexNativeToolIDs},
		{name: "multi-agent codex native tools", get: codexMultiAgentNativeToolIDs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := tt.get()
			first[0] = "mutated"
			if again := tt.get(); again[0] == "mutated" {
				t.Fatalf("%s returned shared mutable backing storage", tt.name)
			}
		})
	}

	aliases := mcpLegacyEnvAliases()
	aliases["GO_AGENT_CTL_RPC_ADDR"][0] = "mutated"
	if got := mcpLegacyEnvAliases()["GO_AGENT_CTL_RPC_ADDR"][0]; got == "mutated" {
		t.Fatal("legacy manifest aliases returned shared mutable backing storage")
	}
}

func TestRuntimeStatePredicatesFailClosed(t *testing.T) {
	if _, ok := recoveryFailureSpecForCode("UNKNOWN"); ok {
		t.Fatal("recoveryFailureSpecForCode accepted an unknown code")
	}
	if IsForbiddenDatabaseEnvKey("not-a-database-key") {
		t.Fatal("IsForbiddenDatabaseEnvKey accepted an unknown key")
	}
}
