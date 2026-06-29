package claudecli

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func validResumePromptSnapshotForTest() dto.PromptAssemblySnapshot {
	return dto.PromptAssemblySnapshot{
		DisplayName:      "resume",
		BaseInstructions: "resume system prompt",
		Provider:         "claude",
		Version:          contract.PromptAssemblySnapshotVersion,
		Hash:             "snapshot-hash",
	}
}

func TestResumeSessionRejectsEmptyPromptSnapshot(t *testing.T) {
	t.Parallel()

	_, err := (&driver{}).ResumeSession(context.Background(), dto.ResumeSessionRequest{
		ProviderThreadID: "provider-thread-1",
		ThreadID:         "thread-1",
		AgentID:          "agent-1",
		CWD:              t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("ResumeSession() error = %v, want prompt snapshot error", err)
	}
}
