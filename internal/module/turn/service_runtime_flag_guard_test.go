package turn

import (
	"context"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestPrepareTurnRejectsMalformedProviderNativeSkillsRuntimeFlag(t *testing.T) {
	t.Parallel()

	assembly := &stubPromptAssemblyService{}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly, NewToolResultRuntime())
	session := &stubSession{
		threadID:      "thread-runtime-flag",
		runtimeConfig: map[string]any{"providerNativeSkills": "false"},
	}
	_, err := svc.PrepareTurn(context.Background(), session, PrepareInput{Prompt: "hello"})
	if err == nil || !strings.Contains(err.Error(), "providerNativeSkills") {
		t.Fatalf("PrepareTurn() error = %v, want malformed providerNativeSkills rejection", err)
	}
	if assembly.lastTurnInput.UserText != "" {
		t.Fatalf("prompt assembly received user text %q before runtime flag validation", assembly.lastTurnInput.UserText)
	}
}

func TestStartTurnRejectsMalformedProviderNativeSkillsRuntimeFlag(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), NewToolResultRuntime())
	session := &stubSession{
		threadID:      "thread-runtime-flag-start",
		runtimeConfig: map[string]any{"disableProviderNativeSkills": "true"},
	}
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		ThreadID: "thread-runtime-flag-start",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err == nil || !strings.Contains(err.Error(), "disableProviderNativeSkills") {
		t.Fatalf("StartTurn() error = %v, want malformed disableProviderNativeSkills rejection", err)
	}
}
