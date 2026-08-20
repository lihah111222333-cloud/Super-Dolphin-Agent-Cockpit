package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestReadFileWithModeNormalizesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	raw := "first\r\nsecond\r\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	file, err := readFileWithMode(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if file.content != "first\nsecond\n" {
		t.Fatalf("content mismatch: %q", file.content)
	}
	if file.raw != raw {
		t.Fatalf("raw mismatch: %q", file.raw)
	}
	if file.lineEnding != lineEndingCRLF {
		t.Fatalf("line ending mismatch: %q", file.lineEnding)
	}
	if restored := file.diskContent(file.content); restored != raw {
		t.Fatalf("restored mismatch: %q", restored)
	}
}

func TestRestoreLineEndingsEliminatesMixedCRLFAndLF(t *testing.T) {
	mixed := "first\r\nsecond\nthird\r\n"
	if got, want := restoreLineEndings(mixed, lineEndingCRLF), "first\r\nsecond\r\nthird\r\n"; got != want {
		t.Fatalf("CRLF restoration = %q, want %q", got, want)
	}
	if got, want := restoreLineEndings(mixed, lineEndingLF), "first\nsecond\nthird\n"; got != want {
		t.Fatalf("LF restoration = %q, want %q", got, want)
	}
}

func TestReadFileUsesMetaCWDForExternalAbsolutePath(t *testing.T) {
	mainRoot := t.TempDir()
	externalRoot := t.TempDir()
	externalFile := filepath.Join(externalRoot, "go.mod")
	if err := os.WriteFile(externalFile, []byte("module external.test\n"), 0o600); err != nil {
		t.Fatalf("write external fixture: %v", err)
	}
	handler := NewFileHandler(Config{WorkspaceRoot: mainRoot})
	ctx := context.WithValue(context.Background(), common.CwdContextKey, externalRoot)
	req, err := json.Marshal(fileToolInput{
		Action:   "read_file",
		FilePath: externalFile,
	})
	if err != nil {
		t.Fatalf("marshal read_file input: %v", err)
	}

	got, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("read_file returned error: %v", err)
	}
	text, ok := got.(string)
	if !ok {
		t.Fatalf("read_file result type = %T, want string", got)
	}
	if !strings.Contains(text, "module external.test") {
		t.Fatalf("read_file result = %q, want external file content", text)
	}
}

func TestBuildPatchReplacePlanRestoresCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	raw := "a\r\nb\r\nc\r\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	file, err := readFileWithMode(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	plan, err := buildReplacePlan(file.content, EditRequest{
		Patch: strings.Join([]string{
			"@@",
			"-b",
			"+x",
			"+y",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("build patch plan: %v", err)
	}
	if plan.updatedContent != "a\nx\ny\nc\n" {
		t.Fatalf("updated content mismatch: %q", plan.updatedContent)
	}
	if restored := file.diskContent(plan.updatedContent); restored != "a\r\nx\r\ny\r\nc\r\n" {
		t.Fatalf("restored mismatch: %q", restored)
	}
}

func TestReplaceRangeAppliesUnsupportedTextFilesWithoutLSPManager(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		old     string
		new     string
		want    string
	}{
		{
			name:    "markdown",
			file:    "plan.md",
			content: "# Title\n\nold markdown line\n",
			old:     "old markdown line",
			new:     "new markdown line",
			want:    "# Title\n\nnew markdown line\n",
		},
		{
			name:    "plaintext",
			file:    "notes.txt",
			content: "first\nold text\nlast\n",
			old:     "old text",
			new:     "new text",
			want:    "first\nnew text\nlast\n",
		},
		{
			name:    "json",
			file:    "config.json",
			content: "{\n  \"mode\": \"old\"\n}\n",
			old:     "  \"mode\": \"old\"",
			new:     "  \"mode\": \"new\"",
			want:    "{\n  \"mode\": \"new\"\n}\n",
		},
		{
			name:    "yaml",
			file:    "config.yaml",
			content: "server:\n  port: 8080\nfeatures:\n  dag: false\n",
			old:     "  dag: false",
			new:     "  dag: true",
			want:    "server:\n  port: 8080\nfeatures:\n  dag: true\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertUnsupportedTextReplace(t, tt.file, tt.content, tt.old, tt.new, tt.want)
		})
	}
}

func assertUnsupportedTextReplace(t *testing.T, file, content, oldText, newText, want string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, file)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewEditHandler(&structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch: strings.Join([]string{
			"@@",
			"-" + oldText,
			"+" + newText,
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("edit returned error: %v", err)
	}
	result := requireReplaceRangeResult(t, got)
	if !strings.Contains(result.Warning, "LSP sync skipped") {
		t.Fatalf("warning = %q, want LSP sync skipped", result.Warning)
	}
	assertFileContent(t, path, want)
}

func requireReplaceRangeResult(t *testing.T, got any) replaceRangeResult {
	t.Helper()
	result, ok := got.(replaceRangeResult)
	if !ok {
		t.Fatalf("result type = %T, want replaceRangeResult", got)
	}
	if result.Status != "applied" || !result.Persisted {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.LSPSync {
		t.Fatalf("LSPSync = true, want false without LSP manager")
	}
	return result
}

func TestReplaceRangeRejectsPathOutsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: outside,
		Patch:    "@@\n-old\n+new\n",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("edit error = %v, want outside workspace root", err)
	}
	raw, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("read outside fixture: %v", readErr)
	}
	if string(raw) != "old\n" {
		t.Fatalf("outside file was modified: %q", raw)
	}
}

func TestReplaceRangeRejectsSymlinkEscapingWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "target.txt")
	if err := os.WriteFile(outside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: link,
		Patch:    "@@\n-old\n+new\n",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("edit error = %v, want outside workspace root", err)
	}
	raw, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("read outside fixture: %v", readErr)
	}
	if string(raw) != "old\n" {
		t.Fatalf("outside symlink target was modified: %q", raw)
	}
}

func TestReplaceRangeReturnsErrorForInvalidPatch(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(inside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input := json.RawMessage(`{"file_path":` + strconv.Quote(inside) + `,"patch":"not a patch"}`)

	_, err := handler(testToolContext(root), input)
	if err == nil {
		t.Fatalf("edit error = nil, want invalid patch failure")
	}
}

type editFailureManager struct {
	structureTestManager
	didChangeErr   error
	didChangeHook  func(uri string)
	didChangeCalls int
}

func (m *editFailureManager) DidChange(_ context.Context, uri string, _ int, _ []protocol.TextDocumentContentChangeEvent) error {
	m.didChangeCalls++
	if m.didChangeHook != nil {
		m.didChangeHook(uri)
	}
	return m.didChangeErr
}

type canceledRollbackManager struct {
	structureTestManager
	contextErr  error
	content     string
	hasDeadline bool
	version     int
}

func (m *canceledRollbackManager) DidChange(ctx context.Context, _ string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	m.contextErr = ctx.Err()
	_, m.hasDeadline = ctx.Deadline()
	if m.contextErr != nil {
		return m.contextErr
	}
	if len(changes) != 1 {
		return fmt.Errorf("DidChange changes = %d, want 1", len(changes))
	}
	m.content = changes[0].Text
	m.version = version
	return nil
}

func TestRollbackReplaceRangeUpdateSyncsWithCanceledCallerContext(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sample.go")
	const (
		original = "package sample\n\nfunc old() {}\n"
		updated  = "package sample\n\nfunc updated() {}\n"
	)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatalf("write updated fixture: %v", err)
	}
	manager := &canceledRollbackManager{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := (EditHandler{}).rollbackReplaceRangeUpdate(
		ctx,
		manager,
		path,
		editableFile{raw: original, mode: 0o600},
		7,
		context.Canceled,
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("rollback error = %v, want original context cancellation", err)
	}
	if strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("rollback error = %v, canceled caller must not prevent rollback sync", err)
	}
	assertFileContent(t, path, original)
	if manager.contextErr != nil {
		t.Fatalf("rollback DidChange context error = %v, want independent live context", manager.contextErr)
	}
	if !manager.hasDeadline {
		t.Fatal("rollback DidChange context has no deadline, want bounded independent context")
	}
	if manager.content != original {
		t.Fatalf("rollback DidChange content = %q, want %q", manager.content, original)
	}
	if manager.version != 8 {
		t.Fatalf("rollback DidChange version = %d, want 8", manager.version)
	}
}

func TestEditFailureAfterDeadClientReturnsRetryableWithoutAutoReplay(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	original := "package main\n\nfunc f() { old() }\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editFailureManager{
		didChangeErr: errors.New("LSP client closed after transport failure"),
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch: strings.Join([]string{
			"@@",
			"-func f() { old() }",
			"+func f() { new() }",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(testToolContext(root), input)
	if err == nil || !strings.Contains(err.Error(), "LSP client closed") {
		t.Fatalf("edit error = %v, want dead-client edit sync failure", err)
	}
	if got == nil {
		t.Fatalf("result = nil, want replaceRangeFailure envelope")
	}
	failure, ok := got.(replaceRangeFailure)
	if !ok {
		t.Fatalf("result type = %T, want replaceRangeFailure", got)
	}
	if failure.Success {
		t.Fatalf("failure.Success = true, want false")
	}
	assertDeadClientRollbackState(t, path, original, manager)
	envelope := newToolErrorEnvelope("patch_edit", "go", errors.Join(multilsp.ErrTransportClosed, manager.didChangeErr))
	if !envelope.Retryable || envelope.Code != "lsp_client_closed" {
		t.Fatalf("dead-client envelope = %#v, want retryable lsp_client_closed", envelope)
	}
}

func assertDeadClientRollbackState(t *testing.T, path, original string, manager *editFailureManager) {
	t.Helper()
	if manager.didChangeCalls != 2 {
		t.Fatalf("DidChange calls = %d, want original sync plus rollback sync without auto-replaying edit", manager.didChangeCalls)
	}
	assertFileContent(t, path, original)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	if string(raw) != want {
		t.Fatalf("file content mismatch for %s:\nwant %q\ngot  %q", path, want, string(raw))
	}
}

func TestReplaceRangeSyncFailureReportsRollbackFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc f() { old() }\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var hookOnce sync.Once
	var hookErr error
	manager := &editFailureManager{
		didChangeErr: errors.New("lsp sync boom"),
		didChangeHook: func(uri string) {
			hookOnce.Do(func() {
				diskPath, err := format.AbsolutePathFromURI(uri)
				if err != nil {
					hookErr = err
					return
				}
				hookErr = replaceFileWithDirectoryForRollbackTest(diskPath)
			})
		},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch: strings.Join([]string{
			"@@",
			"-func f() { old() }",
			"+func f() { new() }",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(testToolContext(root), input)
	if hookErr != nil {
		t.Fatalf("arrange rollback failure fixture: %v", hookErr)
	}
	requireSyncRollbackFailure(t, err)
	if got == nil {
		t.Fatalf("result = nil, want replaceRangeFailure envelope")
	}
	failure, ok := got.(replaceRangeFailure)
	if !ok {
		t.Fatalf("result type = %T, want replaceRangeFailure", got)
	}
	if failure.Success {
		t.Fatalf("failure.Success = true, want false")
	}
}

func replaceFileWithDirectoryForRollbackTest(path string) (lastErr error) {
	for range 200 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			lastErr = err
		} else if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			lastErr = err
		} else {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return lastErr
}

type editBlockingSyncManager struct {
	structureTestManager
	started chan struct{}
	once    sync.Once
}

func (m *editBlockingSyncManager) DidChange(ctx context.Context, _ string, _ int, _ []protocol.TextDocumentContentChangeEvent) error {
	m.once.Do(func() { close(m.started) })
	<-ctx.Done()
	return ctx.Err()
}

type editBlockingFunctionLookupManager struct {
	structureTestManager
	started chan struct{}
	once    sync.Once
}

func (m *editBlockingFunctionLookupManager) DocumentSymbol(ctx context.Context, _ string) ([]protocol.DocumentSymbol, error) {
	m.once.Do(func() {
		close(m.started)
	})
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestReplaceRangeConfirmsDiskWriteWithGitDiffBeforeSlowLSPSync(t *testing.T) {
	root := initEditGitRepo(t, map[string]string{
		"sample.go": "package main\n\nfunc f() { old() }\n",
	})
	logDir := filepath.Join(root, "logs")
	t.Setenv("GO_AGENT_LOG_FALLBACK_DIR", logDir)
	path := filepath.Join(root, "sample.go")
	manager := &editBlockingSyncManager{started: make(chan struct{})}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch: strings.Join([]string{
			"@@",
			"-func f() { old() }",
			"+func f() { new() }",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	ctx, cancel := context.WithTimeout(testToolContext(root), 2*time.Second)
	defer cancel()

	startedAt := time.Now()
	got, err := handler(ctx, input)
	if err != nil {
		t.Fatalf("edit returned error: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("edit elapsed = %s, want fast git diff confirmation", elapsed)
	}
	result := requireReplaceRangeResult(t, got)
	if result.LSPSync {
		t.Fatalf("LSPSync = true, want git diff confirmation before slow LSP sync")
	}
	if !strings.Contains(result.Warning, "git diff") {
		t.Fatalf("warning = %q, want git diff confirmation", result.Warning)
	}
	assertFileContent(t, path, "package main\n\nfunc f() { new() }\n")
	assertEditRecoveryLog(t, logDir, path)
}

func TestReplaceRangeDoesNotRequestDeprecatedFunctionContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc f() { old() }\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editBlockingFunctionLookupManager{started: make(chan struct{})}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch: strings.Join([]string{
			"@@",
			"-func f() { old() }",
			"+func f() { new() }",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	ctx, cancel := context.WithTimeout(testToolContext(root), 2*time.Second)
	defer cancel()

	startedAt := time.Now()
	got, err := handler(ctx, input)
	if err != nil {
		t.Fatalf("edit returned error after disk write and LSP sync: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("edit elapsed = %s, want bounded replace_range completion", elapsed)
	}
	requireSyncedReplaceRangeResult(t, got)
	assertFileContent(t, path, "package main\n\nfunc f() { new() }\n")
	select {
	case <-manager.started:
		t.Fatal("replace_range requested deprecated function context")
	default:
	}
}

func requireSyncRollbackFailure(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("edit error = nil, want sync and rollback failure details")
	}
	if !strings.Contains(err.Error(), "lsp sync boom") || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("edit error = %v, want sync and rollback failure details", err)
	}
}

func initEditGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	runEditGit(t, root, "init")
	runEditGit(t, root, "config", "user.email", "test@example.invalid")
	runEditGit(t, root, "config", "user.name", "Test User")
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	runEditGit(t, root, "add", ".")
	runEditGit(t, root, "commit", "-m", "initial")
	return root
}

func runEditGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func assertEditRecoveryLog(t *testing.T, logDir string, path string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(logDir, "mcp-lsp-edit-recovery.jsonl"))
	if err != nil {
		t.Fatalf("read edit recovery log: %v", err)
	}
	path = strings.ReplaceAll(path, "\\", "\\\\")
	if text := string(raw); !strings.Contains(text, path) || !strings.Contains(text, "git_diff_confirmed") {
		t.Fatalf("edit recovery log = %q, want file path and git_diff_confirmed", text)
	}
}

func TestEditPureInsertionHunk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	content := "package main\n\nimport (\n)\n\nfunc main() {\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch:    " import (\n+\t\"fmt\"\n )\n",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("pure insertion edit error: %v", err)
	}
	result := requireReplaceRangeResult(t, got)
	if result.Status != "applied" {
		t.Fatalf("pure insertion not applied: %+v", result)
	}
	want := "package main\n\nimport (\n\t\"fmt\"\n)\n\nfunc main() {\n}\n"
	assertFileContent(t, path, want)
}

func TestEditPureInsertionEOF(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	content := "package main\n\nfunc main() {\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch:    " }\n+\n+func helper() {}\n",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("pure insertion EOF edit error: %v", err)
	}
	result := requireReplaceRangeResult(t, got)
	if result.Status != "applied" {
		t.Fatalf("pure insertion EOF not applied: %+v", result)
	}
	want := "package main\n\nfunc main() {\n}\n\nfunc helper() {}\n"
	assertFileContent(t, path, want)
}

func TestEditHandlerLegacyEntryPointRemovedAndWrapperAvailable(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	editSourcePath := filepath.Join(filepath.Dir(testFile), "tool_edit.go")
	source, err := os.ReadFile(editSourcePath)
	if err != nil {
		t.Fatalf("read edit handler source: %v", err)
	}
	if strings.Contains(string(source), "func HandleEdit(") {
		t.Fatal("legacy exported HandleEdit entry point still exists")
	}
	if handler := NewEditHandlerWithRoot(t.TempDir(), &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage}); handler == nil {
		t.Fatal("NewEditHandlerWithRoot returned nil")
	}
}

func TestEditHandlerOwnersSerializeSameCanonicalPath(t *testing.T) {
	owner := &editLockRegistry{}
	first := EditHandler{lockRegistry: owner}
	second := EditHandler{lockRegistry: owner}
	unlockFirst := lockEditFile(first.lockRegistry, "/workspace/shared.go")
	started := make(chan struct{})
	acquired := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		close(started)
		unlockSecond := lockEditFile(second.lockRegistry, "/workspace/shared.go")
		close(acquired)
		unlockSecond()
	})
	<-started
	select {
	case <-acquired:
		t.Fatal("second handler acquired shared-owner lock before first released")
	case <-time.After(100 * time.Millisecond):
	}
	unlockFirst()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second handler did not acquire shared-owner lock after release")
	}
	wg.Wait()
}

func TestEditHandlerOwnersAllowDifferentPathsConcurrently(t *testing.T) {
	owner := &editLockRegistry{}
	first := EditHandler{lockRegistry: owner}
	second := EditHandler{lockRegistry: owner}
	unlockFirst := lockEditFile(first.lockRegistry, "/workspace/one.go")
	started := make(chan struct{})
	acquired := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		close(started)
		unlockSecond := lockEditFile(second.lockRegistry, "/workspace/two.go")
		close(acquired)
		unlockSecond()
	})
	<-started
	select {
	case <-acquired:
	case <-time.After(time.Second):
		unlockFirst()
		t.Fatal("different-path lock was blocked by unrelated path")
	}
	wg.Wait()
	unlockFirst()
}

func TestEditHandlerResolvesDotDotBeforeCanonicalLock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "y.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	owner := &editLockRegistry{}
	handler := EditHandler{
		registry:     &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage},
		root:         resolveRoot(root),
		lockRegistry: owner,
	}
	canonicalPath, err := lspplatform.CanonicalExistingPath(path)
	if err != nil {
		t.Fatalf("resolve canonical fixture path: %v", err)
	}
	unlock := lockEditFile(owner, canonicalPath)
	resultCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		_, err := handler.Handle(testToolContext(root), marshalReproParams(t, EditRequest{
			Action:   "replace_range",
			FilePath: filepath.Join(root, "x", "..", "y.txt"),
			Patch:    "-old\n+new\n",
		}))
		resultCh <- err
	})
	select {
	case err := <-resultCh:
		unlock()
		if err != nil {
			t.Fatalf("replace_range returned before canonical lock release: %v", err)
		}
		t.Fatal("dot-dot path bypassed canonical owner lock")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("replace_range after canonical lock release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("replace_range did not complete after canonical lock release")
	}
	wg.Wait()
	assertFileContent(t, path, "new\n")
}
