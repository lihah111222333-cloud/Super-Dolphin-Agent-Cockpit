package contract

import (
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestValidateResumePromptSnapshotRejectsEmptySnapshot(t *testing.T) {
	t.Parallel()

	err := ValidateResumePromptSnapshot(dto.PromptAssemblySnapshot{})
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("ValidateResumePromptSnapshot() error = %v, want prompt snapshot error", err)
	}
}

func TestValidateResumePromptSnapshotAcceptsStableSnapshot(t *testing.T) {
	t.Parallel()

	err := ValidateResumePromptSnapshot(dto.PromptAssemblySnapshot{
		BaseInstructions: "system prompt",
		Version:          2,
		Hash:             "snapshot-hash",
	})
	if err != nil {
		t.Fatalf("ValidateResumePromptSnapshot() error = %v", err)
	}
}
