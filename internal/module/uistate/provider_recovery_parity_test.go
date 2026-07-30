package uistate

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
)

func TestProviderRecoveryParityUsesLegacySnapshotUUID(t *testing.T) {
	t.Parallel()

	const providerUUID = "019e218f-b9c9-7c60-87f7-449577c795dc"
	home := t.TempDir()
	request := providerRecoveryRequestFromUIBinding(BindingEntry{
		Provider:             "codex",
		ProviderThreadID:     "agent_legacy",
		SessionUUID:          providerUUID,
		CodexThreadID:        "public-thread",
		CodexHome:            home,
		ProviderRecoveryHome: home,
	})
	result, err := providerrecovery.ResolveOptional(request)
	if err != nil {
		t.Fatalf("ResolveOptional() error = %v", err)
	}
	if result.ProviderThreadID != providerUUID ||
		result.IdentitySource != providerrecovery.IdentitySourceLegacySessionUUID ||
		result.ArtifactPolicy != providerrecovery.ArtifactPolicyOptionalMissing {
		t.Fatalf("uistate recovery result = %#v, want legacy UUID optional-missing parity", result)
	}
}

func TestProviderRecoveryMapperUsesClaudeInstanceOwner(t *testing.T) {
	t.Parallel()

	request := providerRecoveryRequestFromUIBinding(BindingEntry{
		Provider:             "claude",
		CodexHome:            "/instances/codex-other",
		ProviderRecoveryHome: "/instances/claude-b",
	})
	if request.ClaudeHome != "/instances/claude-b" || request.CodexHome != "/instances/codex-other" {
		t.Fatalf("uistate recovery homes = codex %q claude %q", request.CodexHome, request.ClaudeHome)
	}
}
