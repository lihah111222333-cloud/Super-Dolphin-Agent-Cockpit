package difftracker

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubResolver map[string]string

func (r stubResolver) ResolveAgentCWD(_ context.Context, agentID string) (string, error) {
	return r[agentID], nil
}

func TestDiffAggregator_AgentIsolation(t *testing.T) {
	aggregator := NewDiffAggregator(withSweepInterval(time.Hour))
	aggregator.Start()
	defer aggregator.Stop()

	resultA, changedA := mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-a",
		ToolName: "lsp_edit",
		RepoRoot: "/repo",
		DiffText: buildUnifiedDiffBlock("a.txt", "old\n", "new\n"),
	})
	resultB, changedB := mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-b",
		ThreadID: "thread-1",
		CallID:   "call-b",
		ToolName: "lsp_edit",
		RepoRoot: "/repo",
		DiffText: buildUnifiedDiffBlock("b.txt", "base\n", "beta\n"),
	})

	if !changedA || !changedB {
		t.Fatalf("changed flags = (%v, %v), want both true", changedA, changedB)
	}
	if strings.Contains(resultB.DiffText, "a.txt") {
		t.Fatalf("agent-b diff leaked agent-a content: %q", resultB.DiffText)
	}
	if !strings.Contains(resultA.DiffText, "a.txt") || !strings.Contains(resultB.DiffText, "b.txt") {
		t.Fatalf("unexpected diff texts: a=%q b=%q", resultA.DiffText, resultB.DiffText)
	}
}

func TestDiffAggregator_CallIDDedup(t *testing.T) {
	aggregator := NewDiffAggregator(withSweepInterval(time.Hour))
	aggregator.Start()
	defer aggregator.Stop()

	first, changed := mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-1",
		ToolName: "lsp_edit",
		RepoRoot: "/repo",
		DiffText: buildUnifiedDiffBlock("a.txt", "old\n", "new\n"),
	})
	if !changed {
		t.Fatal("first merge changed = false, want true")
	}
	second, changed := mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-1",
		ToolName: "lsp_edit",
		RepoRoot: "/repo",
		DiffText: buildUnifiedDiffBlock("a.txt", "new\n", "newer\n"),
	})

	if changed {
		t.Fatal("second merge changed = true, want false for duplicate call ID")
	}
	if second.Revision != first.Revision {
		t.Fatalf("revision = %d, want %d", second.Revision, first.Revision)
	}
	if strings.Contains(second.DiffText, "newer") {
		t.Fatalf("deduped call should not replace diff: %q", second.DiffText)
	}
}

func TestDiffAggregator_CleanupAgent(t *testing.T) {
	aggregator := NewDiffAggregator(withSweepInterval(time.Hour))
	aggregator.Start()
	defer aggregator.Stop()

	mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-1",
		ToolName: "lsp_edit",
		RepoRoot: "/repo",
		DiffText: buildUnifiedDiffBlock("a.txt", "old\n", "new\n"),
	})
	aggregator.CleanupAgent("agent-a")

	if hasSessionForAgent(aggregator, "agent-a") {
		t.Fatal("session still exists after cleanup")
	}
}

func TestDiffAggregator_TTLCleanup(t *testing.T) {
	aggregator := NewDiffAggregator(
		withSessionTTL(15*time.Millisecond),
		withSweepInterval(5*time.Millisecond),
	)
	aggregator.Start()
	defer aggregator.Stop()

	mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-1",
		ToolName: "lsp_edit",
		RepoRoot: "/repo",
		DiffText: buildUnifiedDiffBlock("a.txt", "old\n", "new\n"),
	})

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !hasSessionForAgent(aggregator, "agent-a") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session was not cleaned by sweeper")
}

func TestDiffAggregator_TTLSweeperSkipsInUseSession(t *testing.T) {
	current := time.Unix(1_700_000_000, 0)
	aggregator := NewDiffAggregator(withSessionTTL(time.Minute), withSweepInterval(time.Hour))
	aggregator.now = func() time.Time { return current }

	session := aggregator.sessionFor(MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		RepoRoot: "/repo",
	})
	t.Cleanup(func() {
		if session != nil {
			aggregator.releaseSession(session)
		}
	})

	aggregator.mu.Lock()
	session.lastActivity = current.Add(-2 * time.Minute)
	aggregator.mu.Unlock()

	aggregator.sweepExpired()

	ok := hasSessionForAgent(aggregator, "agent-a")
	aggregator.mu.Lock()
	refCount := session.refCount
	aggregator.mu.Unlock()
	if !ok {
		t.Fatal("sweeper removed in-use session")
	}
	if refCount != 1 {
		t.Fatalf("refCount = %d, want 1 while session is in use", refCount)
	}

	aggregator.releaseSession(session)
	session = nil
	current = current.Add(2 * time.Minute)
	aggregator.sweepExpired()

	if hasSessionForAgent(aggregator, "agent-a") {
		t.Fatal("session still exists after release and ttl expiry")
	}
}

func TestDiffAggregator_RepoRootReset(t *testing.T) {
	aggregator := NewDiffAggregator(withSweepInterval(time.Hour))
	aggregator.Start()
	defer aggregator.Stop()

	mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-1",
		ToolName: "lsp_edit",
		RepoRoot: "/repo-a",
		DiffText: buildUnifiedDiffBlock("a.txt", "old\n", "new\n"),
	})
	result, changed := mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-2",
		ToolName: "lsp_edit",
		RepoRoot: "/repo-b",
		DiffText: buildUnifiedDiffBlock("b.txt", "base\n", "beta\n"),
	})

	if !changed {
		t.Fatal("second merge changed = false, want true")
	}
	if result.Revision != 1 {
		t.Fatalf("revision = %d, want 1 after repo reset", result.Revision)
	}
	if strings.Contains(result.DiffText, "a.txt") || !strings.Contains(result.DiffText, "b.txt") {
		t.Fatalf("unexpected cumulative diff after reset: %q", result.DiffText)
	}
}

func TestDiffAggregator_CWDIsolationWithoutRepoRoot(t *testing.T) {
	aggregator := NewDiffAggregator(withSweepInterval(time.Hour))
	aggregator.Start()
	defer aggregator.Stop()

	mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-1",
		ToolName: "lsp_edit",
		CWD:      "/tmp/agent-a/one",
		DiffText: buildUnifiedDiffBlock("a.txt", "old\n", "new\n"),
	})
	result, changed := mustMergeRequest(t, aggregator, MergeRequest{
		AgentID:  "agent-a",
		ThreadID: "thread-1",
		CallID:   "call-2",
		ToolName: "lsp_edit",
		CWD:      "/tmp/agent-a/two",
		DiffText: buildUnifiedDiffBlock("b.txt", "base\n", "beta\n"),
	})

	if !changed {
		t.Fatal("second merge changed = false, want true")
	}
	if result.Revision != 1 {
		t.Fatalf("revision = %d, want 1 after cwd reset", result.Revision)
	}
	if strings.Contains(result.DiffText, "a.txt") || !strings.Contains(result.DiffText, "b.txt") {
		t.Fatalf("unexpected cumulative diff after cwd reset: %q", result.DiffText)
	}
	if got := sessionCountForAgent(aggregator, "agent-a"); got != 2 {
		t.Fatalf("session count = %d, want 2 isolated cwd sessions", got)
	}
}

func TestDiffAggregator_ConcurrentMerge(t *testing.T) {
	aggregator := NewDiffAggregator(withSweepInterval(time.Hour))
	aggregator.Start()
	defer aggregator.Stop()
	resolver := stubResolver{"agent-a": t.TempDir()}

	var wg sync.WaitGroup
	errCh := make(chan error, 16)
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := mergeContext("thread-1", fmt.Sprintf("f-%02d.txt", i))
			errCh <- aggregator.Merge(
				ctx,
				"agent-a",
				fmt.Sprintf("call-%02d", i),
				"lsp_edit",
				replaceRangeToolResult("old\n", fmt.Sprintf("new-%02d\n", i)),
				resolver,
			)
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("Merge returned error: %v", err)
		}
	}
	session := sessionForTest(t, aggregator, "agent-a")
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.revision != 16 {
		t.Fatalf("revision = %d, want 16", session.revision)
	}
	if len(session.files) != 16 {
		t.Fatalf("len(files) = %d, want 16", len(session.files))
	}
}

func TestDiffAggregator_MergeFallbacksToGitDiffWhenHookPatchMissing(t *testing.T) {
	repo := initGitRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "old\n")
	runGitCommand(t, repo, "add", "tracked.txt")
	runGitCommand(t, repo, "commit", "-m", "init")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "new\n")

	aggregator := NewDiffAggregator(withSweepInterval(time.Hour))
	aggregator.Start()
	defer aggregator.Stop()

	resolver := stubResolver{"agent-a": repo}
	ctx := mergeContext("thread-1", "tracked.txt")
	if err := aggregator.Merge(ctx, "agent-a", "call-1", "lsp_edit", invalidReplaceRangeToolResult("not a patch"), resolver); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	session := sessionForTest(t, aggregator, "agent-a")
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.revision != 1 {
		t.Fatalf("revision = %d, want 1", session.revision)
	}
	diff := session.files["tracked.txt"]
	if diff == nil {
		t.Fatal("tracked.txt diff missing after fallback")
	}
	if !strings.Contains(diff.Diff, "tracked.txt") || !strings.Contains(diff.Diff, "+new") {
		t.Fatalf("unexpected fallback diff: %q", diff.Diff)
	}
}

func mustMergeRequest(t *testing.T, aggregator *DiffAggregator, req MergeRequest) (*DiffResult, bool) {
	t.Helper()
	result, changed, err := aggregator.mergeRequest(req)
	if err != nil {
		t.Fatalf("mergeRequest() error = %v", err)
	}
	return result, changed
}

func mergeContext(threadID, filePath string) context.Context {
	args := json.RawMessage(fmt.Sprintf(`{"action":"replace_range","file_path":%q}`, filePath))
	return WithToolCallContext(context.Background(), threadID, args, nil)
}

func replaceRangeToolResult(before, after string) json.RawMessage {
	payload := fmt.Sprintf(`{"action":"replace_range","replaced":%q,"replacement":%q}`, before, after)
	return json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, payload))
}

func invalidReplaceRangeToolResult(text string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, text))
}

func hasSessionForAgent(aggregator *DiffAggregator, agentID string) bool {
	aggregator.mu.Lock()
	defer aggregator.mu.Unlock()
	for _, session := range aggregator.sessions {
		if session != nil && session.agentID == agentID {
			return true
		}
	}
	return false
}

func sessionCountForAgent(aggregator *DiffAggregator, agentID string) int {
	aggregator.mu.Lock()
	defer aggregator.mu.Unlock()
	count := 0
	for _, session := range aggregator.sessions {
		if session != nil && session.agentID == agentID {
			count++
		}
	}
	return count
}

func sessionForTest(t *testing.T, aggregator *DiffAggregator, agentID string) *agentDiffSession {
	t.Helper()
	aggregator.mu.Lock()
	defer aggregator.mu.Unlock()
	for _, session := range aggregator.sessions {
		if session != nil && session.agentID == agentID {
			return session
		}
	}
	t.Fatalf("session %q missing", agentID)
	return nil
}
