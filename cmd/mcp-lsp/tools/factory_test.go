package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
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

func TestDispatchToolActionRejectsLegacyFileAliases(t *testing.T) {
	tests := []struct {
		legacy  string
		current string
	}{
		{legacy: "read", current: "read_file"},
		{legacy: "open", current: "open_file"},
	}
	for _, tc := range tests {
		t.Run(tc.legacy, func(t *testing.T) {
			got, err := dispatchToolAction(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), "file", tc.legacy, struct{}{}, map[string]actionHandler[struct{}]{
				tc.current: func(context.Context, struct{}) (any, error) { return "ok", nil },
			})
			if err == nil {
				t.Fatalf("dispatch %s alias error = nil, result = %#v; want unsupported action", tc.legacy, got)
			}
			for _, want := range []string{fmt.Sprintf(`unsupported file action %q`, tc.legacy), fmt.Sprintf(`legacy action %q is no longer accepted`, tc.legacy), fmt.Sprintf(`%q`, tc.current)} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("dispatch %s alias error = %q, want %q", tc.legacy, err.Error(), want)
				}
			}
		})
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
		"paths":                  []string{root},
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

// TestWrapperRejectsEmptyWorkDir ensures wrapper-owned work_dir cannot be silently stripped when empty.
func TestWrapperRejectsEmptyWorkDir(t *testing.T) {
	root := t.TempDir()
	handlerCalled := false
	handler := wrapToolHandler("file", time.Second, func(context.Context, json.RawMessage) (any, error) {
		handlerCalled = true
		return "ok", nil
	})
	payload := mustMarshalToolPayload(t, map[string]any{
		"work_dir": " \t ",
	})

	_, err := handler(testToolContext(root), payload)
	if err == nil || !strings.Contains(err.Error(), "work_dir") || !strings.Contains(err.Error(), "required") {
		t.Fatalf("handler error = %v, want empty work_dir rejection", err)
	}
	if handlerCalled {
		t.Fatalf("handler should not run when work_dir is empty")
	}
}

// TestWrapperRejectsLegacyCWDInArguments ensures cwd is not silently stripped from tool arguments.
func TestWrapperRejectsLegacyCWDInArguments(t *testing.T) {
	assertWrapperRejectsLegacyArgument(t, "cwd")
}

// TestWrapperRejectsLegacyAgentIDInArguments ensures agent_id is not silently stripped from tool arguments.
func TestWrapperRejectsLegacyAgentIDInArguments(t *testing.T) {
	assertWrapperRejectsLegacyArgument(t, "agent_id")
}

func TestCursorErrorIncludesOneBasedHint(t *testing.T) {
	envelope := newToolErrorEnvelope("patch_edit", "go", errors.New("line must be >= 1"))
	if envelope.Success {
		t.Fatalf("envelope success = true, want false")
	}
	if envelope.Code != "position_invalid" {
		t.Fatalf("envelope code = %q, want position_invalid", envelope.Code)
	}
	if !strings.Contains(strings.ToLower(envelope.Hint), "1-based") {
		t.Fatalf("envelope hint = %q, want one-based cursor guidance", envelope.Hint)
	}

	replaceEnvelope := newToolErrorEnvelope("patch_edit", "go", errors.New("column is out of range"))
	if !strings.Contains(strings.ToLower(replaceEnvelope.Hint), "patch") {
		t.Fatalf("replace_range-style cursor hint = %q, want patch guidance", replaceEnvelope.Hint)
	}
}

func TestFileLSPBootstrapActionsDisableOuterTimeout(t *testing.T) {
	if _, ok := fileToolDeadlineForAction(t, "diagnostics"); ok {
		t.Fatal("file diagnostics received an outer tool deadline")
	}
	openParams := json.RawMessage(`{"action":"open_file"}`)
	if got := fileToolTimeoutTierForOS(openParams, "windows"); got != toolTimeoutDisabled {
		t.Fatalf("Windows open_file timeout tier = %s, want disabled", got)
	}
	if got := fileToolTimeoutTierForOS(openParams, "linux"); got != middleware.TierNormal {
		t.Fatalf("Linux open_file timeout tier = %s, want unchanged normal tier", got)
	}
}

func TestFileReadFirstCallHasWindowsColdInstallWindow(t *testing.T) {
	params := json.RawMessage(`{"action":"read_file"}`)
	if got := fileToolTimeoutTierForOS(params, "windows"); got != toolTimeoutDisabled {
		t.Fatalf("Windows first read_file timeout tier = %s, want disabled for cold LSP install", got)
	}
	if got := fileToolTimeoutTierForOS(params, "linux"); got != middleware.TierNormal {
		t.Fatalf("Linux first read_file timeout tier = %s, want unchanged normal tier", got)
	}
}

func TestManagerLSPToolsDisableSharedOuterTimeout(t *testing.T) {
	for _, toolName := range []string{"completion", "inspect", "structure", "xref"} {
		t.Run(toolName, func(t *testing.T) {
			deadlinePresent := false
			handler := newManagerToolWithoutOuterTimeout(
				toolName,
				middleware.TierNormal,
				lspmanager.NewRegistry(nil),
				decodeLenient,
				func(ctx context.Context, _ lspmanager.Registry, _ struct{}) (any, error) {
					_, deadlinePresent = ctx.Deadline()
					return "ok", nil
				},
			)
			if _, err := handler(testToolContext(t.TempDir()), json.RawMessage("{}")); err != nil {
				t.Fatalf("%s handler returned error: %v", toolName, err)
			}
			if deadlinePresent {
				t.Fatalf("%s received a shared tool deadline, want independent per-LSP-step deadlines", toolName)
			}
		})
	}
}

func TestWindowsColdInstallTimeoutPolicyDoesNotAffectOtherPlatforms(t *testing.T) {
	if !windowsColdInstallOuterTimeoutDisabled("windows") {
		t.Fatal("Windows cold install timeout policy is not enabled")
	}
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		if windowsColdInstallOuterTimeoutDisabled(goos) {
			t.Fatalf("Windows cold install timeout policy leaked to %s", goos)
		}
	}
}

func TestPatchEditActionsDisableOuterTimeout(t *testing.T) {
	for _, action := range []string{"replace_range", "rename", "code_action", "format"} {
		params := json.RawMessage(`{"action":"` + action + `"}`)
		if got := patchEditTimeoutTierForOS(params, "windows"); got != toolTimeoutDisabled {
			t.Fatalf("Windows patch_edit %s timeout tier = %s, want disabled", action, got)
		}
		wantOther := middleware.TierNormal
		if action == "replace_range" {
			wantOther = toolTimeoutDisabled
		}
		if got := patchEditTimeoutTierForOS(params, "linux"); got != wantOther {
			t.Fatalf("Linux patch_edit %s timeout tier = %s, want unchanged %s", action, got, wantOther)
		}
	}
}

func patchEditDeadlineForAction(t *testing.T, action string) (time.Time, bool) {
	t.Helper()
	root := t.TempDir()
	var deadline time.Time
	deadlineOK := false
	handler := wrapToolHandlerWithTimeoutResolver("patch_edit", middleware.TierNormal, patchEditTimeoutTier, func(ctx context.Context, _ json.RawMessage) (any, error) {
		deadline, deadlineOK = ctx.Deadline()
		return "ok", nil
	})
	payload := mustMarshalToolPayload(t, map[string]any{"action": action})

	if _, err := handler(testToolContext(root), payload); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	return deadline, deadlineOK
}

func fileToolDeadlineForAction(t *testing.T, action string) (time.Time, bool) {
	t.Helper()
	root := t.TempDir()
	var deadline time.Time
	deadlineOK := false
	handler := wrapToolHandlerWithTimeoutResolver("file", middleware.TierNormal, fileToolTimeoutTier, func(ctx context.Context, _ json.RawMessage) (any, error) {
		deadline, deadlineOK = ctx.Deadline()
		return "ok", nil
	})
	payload := mustMarshalToolPayload(t, map[string]any{"action": action})

	if _, err := handler(testToolContext(root), payload); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	return deadline, deadlineOK
}

func assertDeadlineNear(t *testing.T, deadline time.Time, want time.Duration, action string) {
	t.Helper()
	remaining := time.Until(deadline)
	if remaining < want-5*time.Second || remaining > want {
		t.Fatalf("file %s timeout = %s, want near %s", action, remaining.Round(time.Second), want)
	}
}

func assertWrapperRejectsLegacyArgument(t *testing.T, field string) {
	t.Helper()
	root := t.TempDir()
	decoded := false
	handler := wrapToolHandler("file", time.Second, func(_ context.Context, params json.RawMessage) (any, error) {
		var input struct {
			Action string `json:"action"`
		}
		if err := decodeStrictToolParams(params, &input); err != nil {
			return nil, err
		}
		decoded = true
		return input, nil
	})
	payload := mustMarshalToolPayload(t, map[string]any{
		"action": "read_file",
		field:    root,
	})

	_, err := handler(testToolContext(root), payload)
	if err == nil {
		t.Fatalf("handler accepted legacy %s argument, want migration error", field)
	}
	for _, want := range []string{field, "_cwd", "_agentId"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("handler error = %q, want %q", err.Error(), want)
		}
	}
	if decoded {
		t.Fatalf("handler decoded params with legacy %s argument, want fail-fast rejection", field)
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

func TestNormalizePlatformWorkDirConvertsWindowsAbsolutePathForWSL(t *testing.T) {
	got := normalizeWSLWorkDir(`C:\Users\ai06\Desktop\Super-Dolphin`)
	want := "/mnt/c/Users/ai06/Desktop/Super-Dolphin"
	if got != want {
		t.Fatalf("normalizeWSLWorkDir() = %q, want %q", got, want)
	}
}

func TestNormalizePlatformWorkDirPreservesLinuxAndRelativePaths(t *testing.T) {
	for _, path := range []string{"/workspace/project", "relative/project", `not:a\path`} {
		if got := normalizeWSLWorkDir(path); got != path {
			t.Fatalf("normalizeWSLWorkDir(%q) = %q, want unchanged", path, got)
		}
	}
}
