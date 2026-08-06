package notify

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
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

func TestResolveHasNoPackageGlobalTerminalStatusState(t *testing.T) {
	t.Parallel()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate resolve test source")
	}
	resolvedFile, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(testFile), "resolve.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse resolve source: %v", err)
	}
	for _, declaration := range resolvedFile.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				if name.Name == "terminalNodeStatuses" {
					t.Fatal("terminal status resolution must not retain package-global mutable state")
				}
			}
		}
	}
}

func TestTerminalNodeStatusResolutionConcurrent(t *testing.T) {
	t.Parallel()
	statuses := []struct {
		status string
		want   bool
	}{
		{status: " done ", want: true},
		{status: "FAILED", want: true},
		{status: "running", want: false},
		{status: "", want: false},
	}
	results := make(chan error, len(statuses)*64)
	for range 64 {
		for _, tc := range statuses {
			tc := tc
			go func() {
				if got := isTerminalNodeStatus(tc.status); got != tc.want {
					results <- fmt.Errorf("isTerminalNodeStatus(%q) = %v, want %v", tc.status, got, tc.want)
					return
				}
				results <- nil
			}()
		}
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Fatal(err)
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
