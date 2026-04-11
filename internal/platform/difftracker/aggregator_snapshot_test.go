package difftracker

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestDiffAggregator_MergeFallbackUsesSnapshotToExcludePreexistingDirty(t *testing.T) {
	repo := initGitRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "old\n")
	writeFile(t, filepath.Join(repo, "dirty.txt"), "base\n")
	runGitCommand(t, repo, "add", "tracked.txt", "dirty.txt")
	runGitCommand(t, repo, "commit", "-m", "init")
	writeFile(t, filepath.Join(repo, "dirty.txt"), "dirty-before\n")
	snapshot, err := BeginSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatalf("BeginSnapshot() error = %v", err)
	}
	writeFile(t, filepath.Join(repo, "tracked.txt"), "new\n")
	aggregator := NewDiffAggregator(withSweepInterval(time.Hour))
	aggregator.Start()
	defer aggregator.Stop()
	ctx := WithToolCallContext(context.Background(), "thread-1", json.RawMessage(`{"action":"replace_range","file_path":"tracked.txt"}`), snapshot)
	if err := aggregator.Merge(ctx, "agent-a", "call-1", "lsp_edit", invalidReplaceRangeToolResult("not a patch"), stubResolver{"agent-a": repo}); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	session := sessionForTest(t, aggregator, "agent-a")
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.files["tracked.txt"] == nil {
		t.Fatal("tracked.txt diff missing after snapshot fallback")
	}
	if session.files["dirty.txt"] != nil {
		t.Fatalf("dirty.txt should be excluded, got %#v", session.files["dirty.txt"])
	}
}
