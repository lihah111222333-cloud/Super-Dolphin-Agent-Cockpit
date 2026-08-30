package claudecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
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

func TestStartSessionRejectsPartialPromptSnapshotBeforePersistence(t *testing.T) {
	requestedHome := filepath.Join(t.TempDir(), "claude-home")
	launchCalled := false
	d := &driver{
		authStatus: loggedInClaudeAuthStatus,
		launchCLI: func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
			launchCalled = true
			return nil, nil, errors.New("launch must not be reached")
		},
	}
	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-partial-snapshot",
		CWD:      t.TempDir(),
		Config:   map[string]any{"claudeHome": requestedHome},
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "base instructions",
			Snapshot: dto.PromptAssemblySnapshot{
				BaseInstructions: "partial snapshot",
				Hash:             "partial-hash",
				Version:          contract.PromptAssemblySnapshotVersion,
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "start prompt snapshot") {
		t.Fatalf("StartSession() error = %v, want partial prompt snapshot rejection", err)
	}
	if launchCalled {
		t.Fatal("launchCLI called for partial prompt snapshot")
	}
	if _, statErr := os.Stat(requestedHome); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Claude home stat = %v, want no persistence before snapshot validation", statErr)
	}
}
