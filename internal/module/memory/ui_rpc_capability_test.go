package memory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestBuildUIMemorySnapshotReportsGitOnlyWriteCapability(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if snapshot.Overview.WriteAvailable {
		t.Fatal("overview.writeAvailable = true for non-Git project")
	}
	if snapshot.Overview.UnavailableReason != memoryUnavailableGitRequired {
		t.Fatalf("overview.unavailableReason = %q, want %q", snapshot.Overview.UnavailableReason, memoryUnavailableGitRequired)
	}
	raw, err := json.Marshal(snapshot.Overview)
	if err != nil {
		t.Fatalf("Marshal(overview) error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("Unmarshal(overview) error = %v", err)
	}
	if wire["writeAvailable"] != false || wire["unavailableReason"] != memoryUnavailableGitRequired {
		t.Fatalf("overview wire capability = %#v", wire)
	}
}

func TestBuildUIMemorySnapshotFailsOnUnexpectedGitResolutionError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	projectRoot := t.TempDir()
	cfg := newUIMemorySnapshotConfig(t, projectRoot, filepath.Join(t.TempDir(), "private"))
	_, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err == nil || errors.Is(err, ErrGitRepositoryRequired) {
		t.Fatalf("buildUIMemorySnapshot() error = %v, want unexpected Git execution failure", err)
	}
}

func TestGitRepositoryRequiredFailureClassification(t *testing.T) {
	if !gitRepositoryRequiredFailure(128, "fatal: not a git repository (or any parent up to mount point)") {
		t.Fatal("explicit non-Git exit was not classified")
	}
	for _, failure := range []struct {
		code   int
		stderr string
	}{{128, "permission denied"}, {1, "not a git repository"}, {128, ""}} {
		if gitRepositoryRequiredFailure(failure.code, failure.stderr) {
			t.Fatalf("unexpected Git failure misclassified: %#v", failure)
		}
	}
}
