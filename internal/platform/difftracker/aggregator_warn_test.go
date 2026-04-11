package difftracker

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestDiffAggregator_MergeWarnsWhenEmitGitDiffFails(t *testing.T) {
	output := captureDifftrackerWarnOutput(t)
	missingRoot := filepath.Join(t.TempDir(), "missing")
	ctx := WithToolCallContext(context.Background(), "thread-1", json.RawMessage(`{"action":"noop"}`), &Snapshot{RepoRoot: missingRoot, root: missingRoot})
	if err := NewDiffAggregator().Merge(ctx, "agent-a", "call-1", "shell", json.RawMessage(`{}`), nil); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	warn := output.String()
	if !strings.Contains(warn, "difftracker: emit git diff failed") || !strings.Contains(warn, "agent-a") || !strings.Contains(warn, "shell") {
		t.Fatalf("warn output = %q, want emit git diff warning with agent/tool context", warn)
	}
}

func TestDiffAggregator_MergeFallbackWarnsWhenEmitGitDiffFails(t *testing.T) {
	output := captureDifftrackerWarnOutput(t)
	missingRoot := filepath.Join(t.TempDir(), "missing")
	ctx := WithToolCallContext(context.Background(), "thread-1", json.RawMessage(`{"action":"replace_range","file_path":"tracked.txt"}`), &Snapshot{RepoRoot: missingRoot, root: missingRoot})
	if err := NewDiffAggregator().Merge(ctx, "agent-a", "call-2", "lsp_edit", invalidReplaceRangeToolResult("not a patch"), nil); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	warn := output.String()
	if !strings.Contains(warn, "falling back to git diff") || !strings.Contains(warn, "difftracker: emit git diff failed") {
		t.Fatalf("warn output = %q, want fallback warning and emit git diff warning", warn)
	}
}

func captureDifftrackerWarnOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })
	return &buf
}
