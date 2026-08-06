package turn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestCleanupToolResultLifecycleKeepsMostRecent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	threadID := "thread-lifecycle-keep"
	toolResults := NewToolResultRuntime()
	toolResults.ResetThread(threadID)
	t.Cleanup(func() { toolResults.ResetThread(threadID) })

	raw := strings.Repeat("x", toolResultPersistThresholdChars+16)
	first := toolResults.Capture(ToolResultMeta{ThreadID: threadID, TurnID: "turn-1", Timestamp: time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)}, raw)
	second := toolResults.Capture(ToolResultMeta{ThreadID: threadID, TurnID: "turn-2", Timestamp: time.Date(2026, 4, 15, 10, 1, 0, 0, time.UTC)}, raw)
	if first.PersistedPath == "" || second.PersistedPath == "" {
		t.Fatalf("persisted paths = (%q, %q), want stored files", first.PersistedPath, second.PersistedPath)
	}
	result := toolResults.Cleanup(threadID, "gpt-5.5", &contract.FRCConfig{Enabled: true, SupportedModels: []string{"gpt-5.5"}, KeepRecent: 1})
	if result.Cleared != 1 || result.Kept != 1 || result.DeletedFiles != 1 {
		t.Fatalf("cleanup result = %+v, want cleared=1 kept=1 deleted=1", result)
	}
	if _, err := os.Stat(first.PersistedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want not-exist", first.PersistedPath, err)
	}
	if _, err := os.Stat(second.PersistedPath); err != nil {
		t.Fatalf("Stat(%q) error = %v, want existing recent file", second.PersistedPath, err)
	}
}

func TestPrepareTurnCleansStaleToolResultsWhenFRCEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	threadID := "thread-lifecycle-prepare"
	toolResults := NewToolResultRuntime()
	toolResults.ResetThread(threadID)
	t.Cleanup(func() { toolResults.ResetThread(threadID) })

	raw := strings.Repeat("y", toolResultPersistThresholdChars+32)
	first := toolResults.Capture(ToolResultMeta{ThreadID: threadID, TurnID: "turn-a", Timestamp: time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC)}, raw)
	second := toolResults.Capture(ToolResultMeta{ThreadID: threadID, TurnID: "turn-b", Timestamp: time.Date(2026, 4, 15, 11, 1, 0, 0, time.UTC)}, raw)
	if first.PersistedPath == "" || second.PersistedPath == "" {
		t.Fatalf("persisted paths = (%q, %q), want stored files", first.PersistedPath, second.PersistedPath)
	}

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, toolResults)
	session := &stubSession{
		threadID: threadID,
		runtimeConfig: map[string]any{
			"model":     "gpt-5.5",
			"frcConfig": map[string]any{"enabled": true, "supportedModels": []string{"gpt-5.5"}, "keepRecent": 1},
		},
	}
	if _, err := svc.PrepareTurn(context.Background(), session, PrepareInput{Prompt: "summarize the state"}); err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if _, err := os.Stat(first.PersistedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want stale file removed", first.PersistedPath, err)
	}
	if _, err := os.Stat(second.PersistedPath); err != nil {
		t.Fatalf("Stat(%q) error = %v, want recent file kept", second.PersistedPath, err)
	}
}
