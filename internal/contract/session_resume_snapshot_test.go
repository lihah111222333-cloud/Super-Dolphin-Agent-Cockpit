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

func TestValidateResumePromptSnapshotRejectsAnyNonCurrentVersion(t *testing.T) {
	t.Parallel()

	for _, version := range []int{0, PromptAssemblySnapshotVersion - 1, PromptAssemblySnapshotVersion + 1} {
		snapshot := dto.PromptAssemblySnapshot{
			BaseInstructions: "system prompt",
			Version:          version,
			Hash:             "snapshot-hash",
		}
		if version == PromptAssemblySnapshotVersion {
			continue
		}
		if err := ValidateResumePromptSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "version") {
			t.Fatalf("ValidateResumePromptSnapshot(version=%d) error = %v, want exact-version rejection", version, err)
		}
	}
}

func TestValidateResumePromptSnapshotForProviderEnforcesOwner(t *testing.T) {
	t.Parallel()

	snapshot := dto.PromptAssemblySnapshot{
		BaseInstructions: "system prompt",
		Provider:         "codex",
		Version:          PromptAssemblySnapshotVersion,
		Hash:             "snapshot-hash",
	}
	if err := ValidateResumePromptSnapshotForProvider(snapshot, "codex"); err != nil {
		t.Fatalf("ValidateResumePromptSnapshotForProvider() error = %v", err)
	}
	if err := ValidateResumePromptSnapshotForProvider(snapshot, "claude"); err == nil || !strings.Contains(err.Error(), "provider mismatch") {
		t.Fatalf("ValidateResumePromptSnapshotForProvider(mismatch) error = %v, want provider mismatch", err)
	}
	snapshot.Provider = ""
	if err := ValidateResumePromptSnapshotForProvider(snapshot, "codex"); err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("ValidateResumePromptSnapshotForProvider(missing owner) error = %v, want provider required", err)
	}
}
