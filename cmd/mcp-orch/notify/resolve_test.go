package notify

import (
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	taskdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/task"
)

func jsonB(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestIsTerminalNodeStatus(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"done", "DONE", " failed ", "succeeded", "cancelled", "CANCELED", "error", "skipped"} {
		if !isTerminalNodeStatus(s) {
			t.Errorf("isTerminalNodeStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "running", "pending", "dispatching", "awaiting_verify"} {
		if isTerminalNodeStatus(s) {
			t.Errorf("isTerminalNodeStatus(%q) = true, want false", s)
		}
	}
}

func TestResolveNodeAliasHierarchy(t *testing.T) {
	t.Parallel()

	node := &taskdag.Node{Config: jsonB(t, map[string]any{"notify_channel": "slack.node"})}
	dag := &taskdag.DAG{Metadata: jsonB(t, map[string]any{"notify_channel": "slack.dag"})}

	// node > dag
	if got := resolveNodeAlias(node, dag); got != "slack.node" {
		t.Fatalf("node alias wins; got %q", got)
	}
	// dag fallback when node has no alias
	node2 := &taskdag.Node{Config: jsonB(t, map[string]any{"other_key": "x"})}
	if got := resolveNodeAlias(node2, dag); got != "slack.dag" {
		t.Fatalf("dag fallback; got %q", got)
	}
	// both empty -> drop
	node3 := &taskdag.Node{}
	dag3 := &taskdag.DAG{}
	if got := resolveNodeAlias(node3, dag3); got != "" {
		t.Fatalf("empty must drop; got %q", got)
	}
	// node alias with surrounding whitespace is trimmed
	node4 := &taskdag.Node{Config: jsonB(t, map[string]any{"notify_channel": "  slack.trim  "})}
	if got := resolveNodeAlias(node4, nil); got != "slack.trim" {
		t.Fatalf("trim alias; got %q", got)
	}
}

func TestResolveNodeAliasIgnoresMalformedJSON(t *testing.T) {
	t.Parallel()
	node := &taskdag.Node{Config: json.RawMessage([]byte(`not json`))}
	if got := resolveNodeAlias(node, nil); got != "" {
		t.Fatalf("malformed JSON must yield empty; got %q", got)
	}
}

func TestResolveNodeAliasCaseInsensitiveKey(t *testing.T) {
	t.Parallel()
	node := &taskdag.Node{Config: jsonB(t, map[string]any{"Notify_Channel": "case.ok"})}
	if got := resolveNodeAlias(node, nil); got != "case.ok" {
		t.Fatalf("case-insensitive key; got %q", got)
	}
}

func TestNodeTerminalTitleFormat(t *testing.T) {
	t.Parallel()
	got := nodeTerminalTitle(taskdto.TaskNodeStatusChanged{NewStatus: "FAILED"})
	want := "DAG node failed: (unnamed node)"
	if got != want {
		t.Fatalf("nodeTerminalTitle = %q, want %q", got, want)
	}
}
