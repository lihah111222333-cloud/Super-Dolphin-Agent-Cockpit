package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestDeleteDAG_DeletesThroughDAGDeleteStore(t *testing.T) {
	t.Parallel()

	store := &stubDeleteDAGStore{rows: 1}
	svc := newDAGTestService(dagControllerParams{DAGStore: store})

	err := svc.DeleteDAG(context.Background(), contract.DeleteDAGRequest{DagKey: " dag-1 "})
	if err != nil {
		t.Fatalf("DeleteDAG() error = %v", err)
	}
	if store.deletedKey != "dag-1" {
		t.Fatalf("DeleteDAG() key = %q, want dag-1", store.deletedKey)
	}
}

func TestDeleteDAG_MapsMissingAndActiveRunErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		rows int64
		err  error
		want error
	}{
		{name: "missing", rows: 0, want: ErrDAGNotFound},
		{name: "active run", err: taskdag.ErrDAGDeleteActiveRun, want: ErrDAGAlreadyRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newDAGTestService(dagControllerParams{DAGStore: &stubDeleteDAGStore{rows: tc.rows, err: tc.err}})
			err := svc.DeleteDAG(context.Background(), contract.DeleteDAGRequest{DagKey: "dag-1"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("DeleteDAG() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDeleteDAG_RequiresDAGKey(t *testing.T) {
	t.Parallel()

	err := newDAGTestService(dagControllerParams{DAGStore: &stubDeleteDAGStore{rows: 1}}).DeleteDAG(context.Background(), contract.DeleteDAGRequest{DagKey: " "})
	if err == nil || !strings.Contains(err.Error(), "dag key is required") {
		t.Fatalf("DeleteDAG() error = %v, want dag key required", err)
	}
}

type stubDeleteDAGStore struct {
	taskdag.OrchestrationStore
	rows       int64
	err        error
	deletedKey string
}

func (s *stubDeleteDAGStore) DeleteDAG(_ context.Context, dagKey string) (int64, error) {
	s.deletedKey = dagKey
	return s.rows, s.err
}
