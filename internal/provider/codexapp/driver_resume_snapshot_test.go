package codexapp

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func validResumePromptSnapshotForTest() dto.PromptAssemblySnapshot {
	return dto.PromptAssemblySnapshot{
		DisplayName:      "resume",
		BaseInstructions: "resume system prompt",
		Provider:         "codex",
		Version:          contract.PromptAssemblySnapshotVersion,
		Hash:             "snapshot-hash",
	}
}

func TestResumeSessionRejectsEmptyPromptSnapshot(t *testing.T) {
	t.Parallel()

	_, err := (&driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager()}).ResumeSession(context.Background(), dto.ResumeSessionRequest{
		ProviderThreadID: "11111111-2222-3333-4444-555555555555",
		ThreadID:         "thread-1",
		AgentID:          "agent-1",
		CWD:              t.TempDir(),
		Config: map[string]any{
			"provider": "codex",
			"cwd":      t.TempDir(),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("ResumeSession() error = %v, want prompt snapshot error", err)
	}
}
