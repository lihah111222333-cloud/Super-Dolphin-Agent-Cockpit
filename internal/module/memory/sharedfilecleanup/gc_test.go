package sharedfilecleanup

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

func TestPreviewProtectsFinalOutputsPinnedAndRecentFiles(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	reader := &stubSharedFiles{files: []sharedfilestore.SharedFile{
		{Path: "reports/final.md", UpdatedAt: now.Add(-90 * 24 * time.Hour)},
		{Path: "handoff/work-old.md", UpdatedAt: now.Add(-45 * 24 * time.Hour)},
		{Path: "handoff/work-recent.md", UpdatedAt: now.Add(-5 * 24 * time.Hour)},
		{Path: "reports/pinned.md", UpdatedAt: now.Add(-90 * 24 * time.Hour)},
	}}
	runtime := &stubDAGRuntime{
		dags: []contract.DAGSummary{{DagKey: "daily"}},
		runsByDag: map[string][]contract.Run{
			"daily": {{
				RunKey:   "run-final",
				DagKey:   "daily",
				Status:   "succeeded",
				Metadata: finalOutputMetadata(t, "reports/final.md"),
			}},
		},
	}

	got, err := Preview(context.Background(), Deps{
		Reader:     reader,
		DAGRuntime: runtime,
		Now:        func() time.Time { return now },
	}, Params{
		WorkTTLDays: 30,
		Limit:       20,
		PinnedPaths: []string{"reports/pinned.md"},
	})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if !got.DryRun {
		t.Fatal("Preview() DryRun = false, want true")
	}
	if got.CandidateCount != 1 {
		t.Fatalf("CandidateCount = %d, want 1", got.CandidateCount)
	}
	items := itemsByPath(got.Items)
	assertCleanupItem(t, items["handoff/work-old.md"], true, false, "older_than_work_ttl")
	assertCleanupItem(t, items["reports/final.md"], false, true, "final_output")
	assertCleanupItem(t, items["reports/pinned.md"], false, true, "pinned")
	assertCleanupItem(t, items["handoff/work-recent.md"], false, false, "recent")
}

func TestApplyDeletesOnlyOldUnreferencedWorkFiles(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := &stubSharedFiles{files: []sharedfilestore.SharedFile{
		{Path: "reports/final.md", UpdatedAt: now.Add(-120 * 24 * time.Hour)},
		{Path: "reports/running.log", UpdatedAt: now.Add(-120 * 24 * time.Hour)},
		{Path: "handoff/work-old.md", UpdatedAt: now.Add(-120 * 24 * time.Hour)},
	}}
	runtime := &stubDAGRuntime{
		dags: []contract.DAGSummary{{DagKey: "daily"}},
		runsByDag: map[string][]contract.Run{
			"daily": {
				{
					RunKey:   "run-final",
					DagKey:   "daily",
					Status:   "succeeded",
					Metadata: finalOutputMetadata(t, "reports/final.md"),
				},
				{RunKey: "run-active", DagKey: "daily", Status: "running"},
			},
		},
		runDetails: map[string]contract.GetRunResponse{
			"run-active": {
				Run: contract.Run{RunKey: "run-active", DagKey: "daily", Status: "running"},
				Nodes: []contract.DAGNode{{
					NodeKey: "build",
					Config:  json.RawMessage(`{"outputs":{"to_sharedfile":{"path":"reports/running.log","lock_mode":"append"}}}`),
				}},
			},
		},
	}

	got, err := Apply(context.Background(), Deps{
		Reader:     store,
		Deleter:    store,
		DAGRuntime: runtime,
		Now:        func() time.Time { return now },
	}, Params{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.DryRun {
		t.Fatal("Apply() DryRun = true, want false")
	}
	if !reflect.DeepEqual(got.DeletedPaths, []string{"handoff/work-old.md"}) {
		t.Fatalf("DeletedPaths = %v, want [handoff/work-old.md]", got.DeletedPaths)
	}
	if !reflect.DeepEqual(store.deleted, []string{"handoff/work-old.md"}) {
		t.Fatalf("Delete calls = %v, want [handoff/work-old.md]", store.deleted)
	}
	items := itemsByPath(got.Items)
	assertCleanupItem(t, items["reports/final.md"], false, true, "final_output")
	assertCleanupItem(t, items["reports/running.log"], false, true, "active_run_output")
	assertCleanupItem(t, items["handoff/work-old.md"], true, false, "older_than_work_ttl")
}

func TestPreviewRejectsMalformedFinalOutputMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	reader := &stubSharedFiles{files: []sharedfilestore.SharedFile{
		{Path: "handoff/work-old.md", UpdatedAt: now.Add(-45 * 24 * time.Hour)},
	}}
	runtime := &stubDAGRuntime{
		dags: []contract.DAGSummary{{DagKey: "daily"}},
		runsByDag: map[string][]contract.Run{
			"daily": {{
				RunKey:   "run-final",
				DagKey:   "daily",
				Status:   "succeeded",
				Metadata: json.RawMessage(`{"final_output":{"sharedfile":"reports/final.md"}}`),
			}},
		},
	}

	_, err := Preview(context.Background(), Deps{
		Reader:     reader,
		DAGRuntime: runtime,
		Now:        func() time.Time { return now },
	}, Params{WorkTTLDays: 30, Limit: 20})
	if err == nil || !strings.Contains(err.Error(), "final_output") {
		t.Fatalf("Preview() error = %v, want malformed final_output metadata error", err)
	}
}

func finalOutputMetadata(t *testing.T, path string) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"final_output": map[string]any{
			"kind": "file",
			"path": path,
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

func itemsByPath(items []Item) map[string]Item {
	out := make(map[string]Item, len(items))
	for _, item := range items {
		out[item.Path] = item
	}
	return out
}

func assertCleanupItem(t *testing.T, item Item, candidate, protected bool, reason string) {
	t.Helper()

	if item.Path == "" {
		t.Fatal("cleanup item missing")
	}
	if item.CleanupCandidate != candidate || item.Protected != protected || item.Reason != reason {
		t.Fatalf("item[%s] candidate=%v protected=%v reason=%q, want candidate=%v protected=%v reason=%q",
			item.Path, item.CleanupCandidate, item.Protected, item.Reason, candidate, protected, reason)
	}
}

type stubSharedFiles struct {
	files   []sharedfilestore.SharedFile
	deleted []string
}

func (s *stubSharedFiles) Get(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
	for _, file := range s.files {
		if file.Path == path {
			fileCopy := file
			return &fileCopy, nil
		}
	}
	return nil, errors.New("shared file not found")
}

func (s *stubSharedFiles) List(_ context.Context, filter sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	out := make([]sharedfilestore.SharedFile, 0, len(s.files))
	for _, file := range s.files {
		if filter.Prefix != "" && !strings.HasPrefix(file.Path, filter.Prefix) {
			continue
		}
		out = append(out, file)
		if filter.Limit > 0 && int32(len(out)) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (s *stubSharedFiles) Delete(_ context.Context, path string) (int64, error) {
	s.deleted = append(s.deleted, path)
	return 1, nil
}

type stubDAGRuntime struct {
	contract.DAGRuntime

	dags       []contract.DAGSummary
	runsByDag  map[string][]contract.Run
	runDetails map[string]contract.GetRunResponse
}

func (s *stubDAGRuntime) ListDAGs(context.Context, contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	return s.dags, nil
}

func (s *stubDAGRuntime) ListRuns(_ context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	runs := s.runsByDag[req.DagKey]
	out := make([]contract.Run, 0, len(runs))
	for _, run := range runs {
		if req.Status != "" && run.Status != req.Status {
			continue
		}
		out = append(out, run)
		if req.Limit > 0 && int32(len(out)) >= req.Limit {
			break
		}
	}
	return contract.ListRunsResponse{Runs: out}, nil
}

func (s *stubDAGRuntime) GetRun(_ context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	run, ok := s.runDetails[req.RunKey]
	if !ok {
		return contract.GetRunResponse{}, errors.New("run not found")
	}
	return run, nil
}
