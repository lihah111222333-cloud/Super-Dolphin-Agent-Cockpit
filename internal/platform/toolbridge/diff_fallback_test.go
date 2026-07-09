package toolbridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
)

func TestDiffFallbackTracker_SkipsSeen(t *testing.T) {
	repo := initGitRepo(t, map[string]string{"tracked.txt": "before\n"})
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "after\n")

	var emitted []difftracker.DiffResult
	tracker := newDiffFallbackTracker(func(_ context.Context, diff difftracker.DiffResult) error {
		emitted = append(emitted, diff)
		return nil
	}, resolverFunc(func(context.Context, string) (string, error) { return repo, nil }), nil)
	tracker.MarkSeen("call-seen")

	tracker.handleToolCallEnd(diffFallbackEvent("agent-1", "thread-1", "call-seen", "patch_edit"))
	if len(emitted) != 0 {
		t.Fatalf("emitted fallback diff count = %d, want 0", len(emitted))
	}
}

func TestDiffFallbackTracker_EmitsDiff(t *testing.T) {
	repo := initGitRepo(t, map[string]string{"tracked.txt": "before\n"})
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "after\n")

	var emitted []difftracker.DiffResult
	tracker := newDiffFallbackTracker(func(_ context.Context, diff difftracker.DiffResult) error {
		emitted = append(emitted, diff)
		return nil
	}, resolverFunc(func(context.Context, string) (string, error) { return repo, nil }), nil)

	tracker.handleToolCallEnd(diffFallbackEvent("agent-2", "thread-2", "call-new", "patch_edit"))
	if len(emitted) != 1 {
		t.Fatalf("emitted fallback diff count = %d, want 1", len(emitted))
	}
	got := emitted[0]
	if got.AgentID != "agent-2" || got.ThreadID != "thread-2" || got.CallID != "call-new" {
		t.Fatalf("emitted metadata = %+v, want agent/thread/call ids", got)
	}
	if got.ToolName != "patch_edit" {
		t.Fatalf("emitted ToolName = %q, want patch_edit", got.ToolName)
	}
	if len(got.Files) != 1 || got.Files[0] != "tracked.txt" {
		t.Fatalf("emitted Files = %#v, want [tracked.txt]", got.Files)
	}
	if !strings.Contains(got.DiffText, "-before") || !strings.Contains(got.DiffText, "+after") {
		t.Fatalf("emitted DiffText = %q, want before/after lines", got.DiffText)
	}
}

func diffFallbackEvent(agentID, threadID, callID, toolName string) tooldto.ToolCallEnd {
	return tooldto.ToolCallEnd{
		ToolCallHeader: shareddto.ToolCallHeader{
			TurnHeader: shareddto.TurnHeader{
				AgentHeader: shareddto.AgentHeader{
					ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID},
					AgentID:      agentID,
				},
			},
			CallID:   callID,
			ToolName: toolName,
		},
		Success: true,
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
