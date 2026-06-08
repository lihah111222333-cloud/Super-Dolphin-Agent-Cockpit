package orchestration

import (
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/sharedfileowner"
)

func TestRenderSharedfilePath(t *testing.T) {
	now := time.Date(2026, 6, 8, 5, 0, 0, 0, time.UTC)
	owner := sharedfileowner.Owner{
		DagKey:   "dag1",
		NodeKey:  "node1",
		RunID:    42,
		ThreadID: "thread-abc",
		TurnID:   "turn-xyz",
	}
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{"no variables passthrough", "dag/essay.md", "dag/essay.md", false},
		{"date variable", "dag/{{date}}/essay.md", "dag/2026-06-08/essay.md", false},
		{"datetime variable", "dag/essay-{{datetime}}.md", "dag/essay-20260608T050000Z.md", false},
		{"run_id variable", "dag/run-{{run_id}}.md", "dag/run-42.md", false},
		{"turn_id variable", "dag/{{turn_id}}.md", "dag/turn-xyz.md", false},
		{"thread_id variable", "dag/{{thread_id}}/out.md", "dag/thread-abc/out.md", false},
		{"multiple variables", "dag/{{date}}/{{run_id}}.md", "dag/2026-06-08/42.md", false},
		{"path traversal rejected", "../{{date}}/escape.md", "", true},
		{"invalid prefix rejected", "essays/{{date}}.md", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sharedfileowner.RenderPath(tt.path, owner, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RenderPath(%q) err=%v wantErr=%v", tt.path, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("RenderPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
