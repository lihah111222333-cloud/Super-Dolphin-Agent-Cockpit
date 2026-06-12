package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
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
	envelope := newToolErrorEnvelope("edit", "go", errors.Join(multilsp.ErrTransportClosed, manager.didChangeErr))
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
	manager := &editFailureManager{
		didChangeErr: errors.New("lsp sync boom"),
		didChangeHook: func(uri string) {
			if err := os.Remove(uri); err != nil {
				t.Fatalf("remove updated file before rollback: %v", err)
			}
			if err := os.Mkdir(uri, 0o700); err != nil {
				t.Fatalf("replace file with directory before rollback: %v", err)
			}
		},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
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

type editBlockingSyncManager struct {
	structureTestManager
	started chan struct{}
}

func (m *editBlockingSyncManager) DidChange(ctx context.Context, _ string, _ int, _ []protocol.TextDocumentContentChangeEvent) error {
	close(m.started)
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

func TestReplaceRangeReturnsAppliedWhenFunctionLookupBlocksAfterSync(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc f() { old() }\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editBlockingFunctionLookupManager{started: make(chan struct{})}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
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
		t.Fatalf("edit elapsed = %s, want bounded best-effort function lookup", elapsed)
	}
	result := requireSyncedReplaceRangeResult(t, got)
	if result.FuncStart != 0 || result.FuncEnd != 0 || result.FuncBody != "" {
		t.Fatalf("function context = %#v, want empty when lookup times out", result)
	}
	assertFileContent(t, path, "package main\n\nfunc f() { new() }\n")
	select {
	case <-manager.started:
	default:
		t.Fatalf("DocumentSymbol was not called; test did not exercise blocking function lookup")
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
	text := string(raw)
	if !strings.Contains(text, path) || !strings.Contains(text, "git_diff_confirmed") {
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
