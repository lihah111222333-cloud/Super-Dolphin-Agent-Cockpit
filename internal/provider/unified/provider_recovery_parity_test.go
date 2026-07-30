package unified

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
)

func TestProviderRecoveryParityUsesLegacySnapshotUUID(t *testing.T) {
	t.Parallel()

	const providerUUID = "019e218f-b9c9-7c60-87f7-449577c795dc"
	home := t.TempDir()
	request := providerRecoveryRequestFromSessionBinding(&contract.SessionBinding{
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
		t.Fatalf("unified recovery result = %#v, want legacy UUID optional-missing parity", result)
	}
}

func TestProviderRecoveryMapperUsesClaudeInstanceOwner(t *testing.T) {
	t.Parallel()

	request := providerRecoveryRequestFromSessionBinding(&contract.SessionBinding{
		Provider:             "claude",
		CodexHome:            "/instances/codex-other",
		ProviderRecoveryHome: "/instances/claude-c",
	})
	if request.ClaudeHome != "/instances/claude-c" || request.CodexHome != "/instances/codex-other" {
		t.Fatalf("unified recovery homes = codex %q claude %q", request.CodexHome, request.ClaudeHome)
	}
}
