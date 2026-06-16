package sharedfilecleanup

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestPreviewProtectsPinnedFinalOutputAndActiveRunFiles verifies cleanup planning respects guards.
func TestPreviewProtectsPinnedFinalOutputAndActiveRunFiles(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	files := []contract.SharedFile{
		{Path: "old.txt", UpdatedAt: now.AddDate(0, 0, -45)},
		{Path: "recent.txt", UpdatedAt: now.AddDate(0, 0, -2)},
		{Path: "_internal/cache.json", UpdatedAt: now.AddDate(0, 0, -60)},
		{Path: "pinned.txt", UpdatedAt: now.AddDate(0, 0, -60)},
		{Path: "final.txt", UpdatedAt: now.AddDate(0, 0, -60)},
		{Path: "active.txt", UpdatedAt: now.AddDate(0, 0, -60)},
	}
	deps := Deps{
		Reader: &fakeSharedFileStore{files: files},
		Now:    func() time.Time { return now },
		DAGRuntime: &fakeCleanupDAGRuntime{
			dags: []contract.DAGSummary{{DagKey: "dag-1"}},
			runs: map[string][]contract.Run{
				"dag-1": {
					{
						RunKey:   "run-final",
						Status:   "succeeded",
						Metadata: json.RawMessage(`{"final_output":{"kind":"file","path":"final.txt"}}`),
					},
					{
						RunKey: "run-active",
						Status: "running",
					},
				},
			},
			runDetails: map[string]contract.GetRunResponse{
				"run-active": {
					Nodes: []contract.DAGNode{
						{Config: json.RawMessage(`{"outputs":{"to_sharedfile":{"path":"active.txt"}}}`)},
					},
				},
			},
		},
	}

	result, err := Preview(context.Background(), deps, Params{WorkTTLDays: 30, Limit: 100, PinnedPaths: []string{"pinned.txt"}})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	if !result.DryRun {
		t.Fatal("Preview() should be dry-run")
	}
	if result.CandidateCount != 1 {
		t.Fatalf("CandidateCount = %d, want 1", result.CandidateCount)
	}
	if result.ProtectedCount != 4 {
		t.Fatalf("ProtectedCount = %d, want 4", result.ProtectedCount)
	}

	byPath := cleanupItemsByPath(result.Items)
	assertCleanupItem(t, byPath["old.txt"], true, false, "older_than_work_ttl")
	assertCleanupItem(t, byPath["recent.txt"], false, false, "recent")
	assertCleanupItem(t, byPath["_internal/cache.json"], false, true, "internal")
	assertCleanupItem(t, byPath["pinned.txt"], false, true, "pinned")
	assertCleanupItem(t, byPath["final.txt"], false, true, "final_output")
	assertCleanupItem(t, byPath["active.txt"], false, true, "active_run_output")
}

// TestApplyDeletesOnlyCleanupCandidates verifies Apply does not delete protected rows.
func TestApplyDeletesOnlyCleanupCandidates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := &fakeSharedFileStore{
		files: []contract.SharedFile{
			{Path: "old.txt", UpdatedAt: now.AddDate(0, 0, -45)},
			{Path: "pinned.txt", UpdatedAt: now.AddDate(0, 0, -45)},
		},
	}
	result, err := Apply(context.Background(), Deps{
		Reader:     store,
		Deleter:    store,
		Now:        func() time.Time { return now },
		DAGRuntime: &fakeCleanupDAGRuntime{},
	}, Params{WorkTTLDays: 30, Limit: 100, PinnedPaths: []string{"pinned.txt"}})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.DryRun {
		t.Fatal("Apply() should not be dry-run")
	}
	if result.DeletedCount != 1 || len(result.DeletedPaths) != 1 || result.DeletedPaths[0] != "old.txt" {
		t.Fatalf("deleted = count %d paths %#v, want old.txt only", result.DeletedCount, result.DeletedPaths)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "old.txt" {
		t.Fatalf("store.deleted = %#v, want old.txt only", store.deleted)
	}
}

func cleanupItemsByPath(items []Item) map[string]Item {
	out := make(map[string]Item, len(items))
	for _, item := range items {
		out[item.Path] = item
	}
	return out
}

func assertCleanupItem(t *testing.T, item Item, candidate, protected bool, reason string) {
	t.Helper()
	if item.CleanupCandidate != candidate || item.Protected != protected || item.Reason != reason {
		t.Fatalf("%s item = candidate %v protected %v reason %q, want candidate %v protected %v reason %q",
			item.Path, item.CleanupCandidate, item.Protected, item.Reason, candidate, protected, reason)
	}
}

type fakeSharedFileStore struct {
	files   []contract.SharedFile
	deleted []string
}

// Get returns no row because cleanup tests only list files.
func (s *fakeSharedFileStore) Get(context.Context, string) (*contract.SharedFile, error) {
	return nil, nil
}

// List returns the seeded shared files for cleanup tests.
func (s *fakeSharedFileStore) List(context.Context, contract.SharedFileListFilter) ([]contract.SharedFile, error) {
	return append([]contract.SharedFile(nil), s.files...), nil
}

// Delete records the deleted path for cleanup tests.
func (s *fakeSharedFileStore) Delete(_ context.Context, path string) (int64, error) {
	s.deleted = append(s.deleted, path)
	return 1, nil
}

type fakeCleanupDAGRuntime struct {
	dags       []contract.DAGSummary
	runs       map[string][]contract.Run
	runDetails map[string]contract.GetRunResponse
}

// GetDAG returns no detail because cleanup tests only scan runs.
func (r *fakeCleanupDAGRuntime) GetDAG(context.Context, string) (contract.DAGDetail, error) {
	return contract.DAGDetail{}, nil
}

// ListDAGs returns the seeded DAG summaries for cleanup tests.
func (r *fakeCleanupDAGRuntime) ListDAGs(context.Context, contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	return append([]contract.DAGSummary(nil), r.dags...), nil
}

// StartDAG is unused by cleanup tests.
func (r *fakeCleanupDAGRuntime) StartDAG(context.Context, contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	return contract.StartDAGResponse{}, nil
}

// TerminateDAG is unused by cleanup tests.
func (r *fakeCleanupDAGRuntime) TerminateDAG(context.Context, contract.TerminateDAGRequest) error {
	return nil
}

// ListRuns returns the seeded runs for one DAG.
func (r *fakeCleanupDAGRuntime) ListRuns(_ context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	return contract.ListRunsResponse{Runs: append([]contract.Run(nil), r.runs[req.DagKey]...)}, nil
}

// GetRun returns the seeded run detail for active-run output guards.
func (r *fakeCleanupDAGRuntime) GetRun(_ context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	return r.runDetails[req.RunKey], nil
}

// ApplyOps is unused by cleanup tests.
func (r *fakeCleanupDAGRuntime) ApplyOps(context.Context, contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	return contract.ApplyOpsResponse{}, nil
}
