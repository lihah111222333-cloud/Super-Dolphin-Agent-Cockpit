package tools

import (
	"errors"
	"testing"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func TestToolErrorClassifierClassifiesTaskCreateDAGConflict(t *testing.T) {
	classification, ok := ToolErrorClassifier("task_create_dag", platformdb.ErrConflict)
	if !ok {
		t.Fatal("ToolErrorClassifier() ok = false, want true")
	}
	if classification.Code != "invalid_input" {
		t.Fatalf("Code = %q, want invalid_input", classification.Code)
	}
	if classification.Retryable {
		t.Fatal("Retryable = true, want false")
	}
}

func TestToolErrorClassifierIgnoresOtherErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool string
		err  error
	}{
		{name: "other tool", tool: "task_update_node", err: platformdb.ErrConflict},
		{name: "other error", tool: "task_create_dag", err: errors.New("ordinary failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ToolErrorClassifier(tc.tool, tc.err); ok {
				t.Fatal("ToolErrorClassifier() ok = true, want false")
			}
		})
	}
}
