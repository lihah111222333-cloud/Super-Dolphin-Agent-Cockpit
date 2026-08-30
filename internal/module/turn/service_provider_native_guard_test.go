package turn

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// TestStartTurnAppliesProviderNativeSkillGate locks the direct StartTurn path
// so callers that bypass PrepareTurn cannot reintroduce skill body injection.
func TestStartTurnAppliesProviderNativeSkillGate(t *testing.T) {
	t.Parallel()

	var captured dto.TurnRequest
	session := &stubSession{
		threadID: "thread-provider-native-skill",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			captured = req
			return newStubTurnHandle(req.LocalID, "provider-turn"), nil
		},
	}
	svc := NewService(silentLogger(), NewToolResultRuntime()).(*service)
	t.Cleanup(svc.Shutdown)

	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-provider-native-skill",
		ThreadID: "thread-provider-native-skill",
		Skills: []dto.SkillRef{{
			Name:    "debug",
			Prompt:  "legacy body must not cross the boundary",
			Summary: "debug summary",
			Version: "v1",
		}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if len(captured.Skills) != 1 || captured.Skills[0].Prompt != "" {
		t.Fatalf("provider skills = %#v, want metadata-only ref", captured.Skills)
	}
}

// TestStartTurnAllowsIdentityOnlyProviderNativeSkillMetadata ensures a direct
// caller can pass the identity fields supplied by the provider-native UI even
// when optional summary/version metadata is unavailable.
func TestStartTurnAllowsIdentityOnlyProviderNativeSkillMetadata(t *testing.T) {
	t.Parallel()

	var captured dto.TurnRequest
	session := &stubSession{
		threadID: "thread-provider-native-identity-only",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			captured = req
			return newStubTurnHandle(req.LocalID, "provider-turn"), nil
		},
	}
	svc := NewService(silentLogger(), NewToolResultRuntime()).(*service)
	t.Cleanup(svc.Shutdown)

	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-provider-native-identity-only",
		ThreadID: "thread-provider-native-identity-only",
		Skills: []dto.SkillRef{{
			Name:   "debug",
			Scope:  "project",
			Path:   "/repo/.agents/skills/debug",
			Prompt: "legacy body must be stripped",
		}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if len(captured.Skills) != 1 || captured.Skills[0].Name != "debug" {
		t.Fatalf("provider skills = %#v, want identity-only ref", captured.Skills)
	}
	if captured.Skills[0].Prompt != "" || captured.Skills[0].Summary != "" || captured.Skills[0].Version != "" {
		t.Fatalf("provider skills = %#v, want body cleared and optional metadata absent", captured.Skills)
	}
}
