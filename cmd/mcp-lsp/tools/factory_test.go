package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func TestDirectToolInputRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	input, err := json.Marshal(map[string]any{
		"action":                 "text_search",
		"query":                  "needle",
		"path":                   root,
		"schema_forbidden_field": true,
	})
	if err != nil {
		t.Fatalf("marshal grep input: %v", err)
	}

	_, err = handler(testToolContext(root), input)
	if err == nil {
		t.Fatal("direct handler accepted unknown field, want schema rejection")
	}
	if !strings.Contains(err.Error(), `unknown field "schema_forbidden_field"`) {
		t.Fatalf("direct handler error = %v, want unknown field rejection", err)
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

func TestWrapToolHandlerRejectsExplicitAbsoluteWorkDirOutsideWorkspaceRoots(t *testing.T) {
	staleRoot := t.TempDir()
	explicitRoot := t.TempDir()
	handlerCalled := false
	handler := wrapToolHandler("file", time.Second, func(ctx context.Context, _ json.RawMessage) (any, error) {
		handlerCalled = true
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

	if _, err := handler(ctx, payload); err == nil || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("handler error = %v, want outside workspace roots rejection", err)
	}
	if handlerCalled {
		t.Fatalf("handler should not run when work_dir is outside workspace roots")
	}
}

func TestWrapToolHandlerResolvesRelativeClaudeWorktreeWorkDir(t *testing.T) {
	root := t.TempDir()
	worktreeRoot := filepath.Join(root, ".claude", "worktrees", "feature")
	mustMkdirAll(t, worktreeRoot)
	var gotScope common.ToolScope
	gotExplicitWorkDir := false
	handler := wrapToolHandler("file", time.Second, func(ctx context.Context, _ json.RawMessage) (any, error) {
		gotScope = mustToolScopeFromContext(t, ctx)
		gotExplicitWorkDir = explicitToolWorkDirFromContext(ctx)
		return "ok", nil
	})
	payload := mustMarshalToolPayload(t, map[string]any{
		"work_dir": filepath.Join(".claude", "worktrees", "feature"),
	})
	ctx := testToolContext(root)

	if _, err := handler(ctx, payload); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	want := mustEvalCleanSymlinks(t, worktreeRoot)
	requireExplicitWorkDirScope(t, gotScope, gotExplicitWorkDir, want, root)
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir worktree root: %v", err)
	}
}

func mustToolScopeFromContext(t *testing.T, ctx context.Context) common.ToolScope {
	t.Helper()
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok {
		t.Fatal("ToolScopeFromContext() missing scope")
	}
	return scope
}

func mustMarshalToolPayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return payload
}

func mustEvalCleanSymlinks(t *testing.T, path string) string {
	t.Helper()
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval worktree root: %v", err)
	}
	return filepath.Clean(want)
}

func requireExplicitWorkDirScope(t *testing.T, gotScope common.ToolScope, gotExplicitWorkDir bool, want string, root string) {
	t.Helper()
	if gotScope.CWD != want {
		t.Fatalf("scope CWD = %q, want %q", gotScope.CWD, want)
	}
	if !gotExplicitWorkDir {
		t.Fatalf("explicit work_dir marker = false, want true")
	}
	if len(gotScope.WorkspaceRoots) == 0 || gotScope.WorkspaceRoots[0] != want {
		t.Fatalf("workspace roots = %#v, want explicit work_dir first %q", gotScope.WorkspaceRoots, want)
	}
	if !slices.Contains(gotScope.WorkspaceRoots, filepath.Clean(root)) {
		t.Fatalf("workspace roots = %#v, want original root %q preserved", gotScope.WorkspaceRoots, filepath.Clean(root))
	}
}
