package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestDispatchToolActionReportsValidActionsAndClosestMatch(t *testing.T) {
	_, err := dispatchToolAction(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), "file", "read_fiel", struct{}{}, map[string]actionHandler[struct{}]{
		"read_file": func(context.Context, struct{}) (any, error) { return nil, nil },
	})
	if err == nil {
		t.Fatalf("dispatch error = nil, want unsupported action")
	}
	for _, want := range []string{"valid actions:", "read_file", `did you mean "read_file"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("dispatch error = %q, want %q", err.Error(), want)
		}
	}
}

func TestDispatchToolActionAcceptsLegacyFileReadAlias(t *testing.T) {
	got, err := dispatchToolAction(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), "file", "read", struct{}{}, map[string]actionHandler[struct{}]{
		"read_file": func(context.Context, struct{}) (any, error) { return "ok", nil },
	})
	if err != nil {
		t.Fatalf("dispatch read alias error = %v", err)
	}
	if got != "ok" {
		t.Fatalf("dispatch read alias result = %#v, want ok", got)
	}
}

func TestDecodeToolParamsAddsAIFriendlyHint(t *testing.T) {
	_, err := decodeToolParams[struct {
		Line int `json:"line"`
	}](json.RawMessage(`{"line":"1"}`), decodeStrict)
	if err == nil {
		t.Fatalf("decode error = nil, want numeric type error")
	}
	if !strings.Contains(err.Error(), "numeric fields as JSON numbers") {
		t.Fatalf("decode error = %q, want numeric hint", err.Error())
	}
}

func TestCursorErrorIncludesOneBasedHint(t *testing.T) {
	envelope := newToolErrorEnvelope("lsp_edit", "go", errors.New("line must be >= 1"))
	if envelope.Success {
		t.Fatalf("envelope success = true, want false")
	}
	if envelope.Code != "position_invalid" {
		t.Fatalf("envelope code = %q, want position_invalid", envelope.Code)
	}
	if !strings.Contains(strings.ToLower(envelope.Hint), "1-based") {
		t.Fatalf("envelope hint = %q, want one-based cursor guidance", envelope.Hint)
	}

	replaceEnvelope := newToolErrorEnvelope("lsp_edit", "go", errors.New("column is out of range"))
	if !strings.Contains(strings.ToLower(replaceEnvelope.Hint), "patch") {
		t.Fatalf("replace_range-style cursor hint = %q, want patch guidance", replaceEnvelope.Hint)
	}
}

func TestRenderListResultEmptyEnvelope(t *testing.T) {
	got, err := renderListResult([]string{}, 10, "no symbols found", func(items []string, total int) any {
		return map[string]any{"items": items, "total": total}
	})
	if err != nil {
		t.Fatalf("renderListResult() error = %v", err)
	}
	payload, ok := got.(emptyListEnvelope)
	if !ok {
		t.Fatalf("empty render result type = %T (%#v), want emptyListEnvelope", got, got)
	}
	if !payload.Success {
		t.Fatalf("empty envelope success = false, want true")
	}
	if len(payload.Data) != 0 || payload.Meta.Count != 0 {
		t.Fatalf("empty envelope = %#v, want empty data/count=0", payload)
	}
	if payload.Meta.Message != "no symbols found" {
		t.Fatalf("empty envelope message = %q", payload.Meta.Message)
	}
}

func TestWrapToolHandlerAllowsExplicitAbsoluteWorkDirOutsideWorkspaceRoots(t *testing.T) {
	staleRoot := t.TempDir()
	explicitRoot := t.TempDir()
	var gotScope common.ToolScope
	handler := wrapToolHandler("file", time.Second, func(ctx context.Context, _ json.RawMessage) (any, error) {
		var ok bool
		gotScope, ok = common.ToolScopeFromContext(ctx)
		if !ok {
			t.Fatal("ToolScopeFromContext() missing scope")
		}
		return "ok", nil
	})
	payload, err := json.Marshal(map[string]any{
		"work_dir": explicitRoot,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            staleRoot,
		WorkspaceRoots: []string{staleRoot},
		Family:         "lsp",
	})

	if _, err := handler(ctx, payload); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	want, err := filepath.EvalSymlinks(explicitRoot)
	if err != nil {
		t.Fatalf("eval explicit root: %v", err)
	}
	found := false
	for _, root := range gotScope.WorkspaceRoots {
		if root == filepath.Clean(want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("workspace roots = %#v, want explicit root %q", gotScope.WorkspaceRoots, want)
	}
}

func TestWrapToolHandlerResolvesRelativeClaudeWorktreeWorkDir(t *testing.T) {
	root := t.TempDir()
	worktreeRoot := filepath.Join(root, ".claude", "worktrees", "feature")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatalf("mkdir worktree root: %v", err)
	}
	var gotScope common.ToolScope
	handler := wrapToolHandler("file", time.Second, func(ctx context.Context, _ json.RawMessage) (any, error) {
		var ok bool
		gotScope, ok = common.ToolScopeFromContext(ctx)
		if !ok {
			t.Fatal("ToolScopeFromContext() missing scope")
		}
		return "ok", nil
	})
	payload, err := json.Marshal(map[string]any{
		"work_dir": filepath.Join(".claude", "worktrees", "feature"),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ctx := testToolContext(root)

	if _, err := handler(ctx, payload); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	want, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		t.Fatalf("eval worktree root: %v", err)
	}
	if gotScope.CWD != filepath.Clean(want) {
		t.Fatalf("scope CWD = %q, want %q", gotScope.CWD, want)
	}
}
