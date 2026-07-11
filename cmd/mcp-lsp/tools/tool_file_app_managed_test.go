package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestFileReadFileAllowsAppManagedPathOutsideWorkspace(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "providers", "codex", "mcp-lsp.log")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("app managed log\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	handler := NewFileHandler(Config{WorkspaceRoot: workspace})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	ctx = WithAppManagedReadCapability(ctx)
	req := marshalFileToolInput(t, fileToolInput{Action: "read_file", FilePath: appFile})

	got, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("read_file returned error: %v", err)
	}
	text, ok := got.(string)
	if !ok {
		t.Fatalf("read_file result type = %T, want string", got)
	}
	if !strings.Contains(text, "app managed log") {
		t.Fatalf("read_file result = %q, want app managed file content", text)
	}
}

func TestFileReadRejectsAppManagedRootWithoutCapability(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "providers", "codex", "mcp-lsp.log")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("app managed log\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	handler := NewFileHandler(Config{WorkspaceRoot: workspace})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	req := marshalFileToolInput(t, fileToolInput{Action: "read_file", FilePath: appFile})

	_, err := handler(ctx, req)
	if err == nil {
		t.Fatal("read_file returned nil error, want app-managed path rejected without read capability")
	}
	if !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("read_file error = %q, want path_outside_workspace rejection", err.Error())
	}
}

func TestFileReadFileRejectsOrdinaryHomePathOutsideWorkspace(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	privateFile := filepath.Join(fakeHome, "Documents", "private.txt")
	if err := os.MkdirAll(filepath.Dir(privateFile), 0o700); err != nil {
		t.Fatalf("mkdir ordinary home parent: %v", err)
	}
	if err := os.WriteFile(privateFile, []byte("ordinary home\n"), 0o600); err != nil {
		t.Fatalf("write ordinary home file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	handler := NewFileHandler(Config{WorkspaceRoot: workspace})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	req := marshalFileToolInput(t, fileToolInput{Action: "read_file", FilePath: privateFile})

	_, err := handler(ctx, req)
	if err == nil {
		t.Fatal("read_file returned nil error, want ordinary HOME path rejected")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Fatalf("read_file error = %q, want outside-scope rejection", err.Error())
	}
}

func TestEditRejectsAppManagedPathWithoutWriteCapability(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "skills", "personal", "agent", "note.md")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	handler := NewEditHandlerWithRoot(workspace, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	req, err := json.Marshal(EditRequest{Action: "replace_range", FilePath: appFile, Patch: "@@\n-old\n+new\n"})
	if err != nil {
		t.Fatalf("marshal edit request: %v", err)
	}

	_, err = handler(ctx, req)
	if err == nil {
		t.Fatal("edit returned nil error, want app-managed write capability rejection")
	}
	if !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("edit error = %v, want path_outside_workspace rejection", err)
	}
	raw, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read app managed file: %v", err)
	}
	if string(raw) != "old\n" {
		t.Fatalf("app managed file content = %q, want unchanged old content", raw)
	}
}

func TestEditAllowsAppManagedPathWithWriteCapability(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "skills", "personal", "agent", "note.md")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	handler := NewEditHandlerWithRoot(workspace, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	ctx = WithAppManagedWriteCapability(ctx)
	req, err := json.Marshal(EditRequest{Action: "replace_range", FilePath: appFile, Patch: "@@\n-old\n+new\n"})
	if err != nil {
		t.Fatalf("marshal edit request: %v", err)
	}

	if _, err := handler(ctx, req); err != nil {
		t.Fatalf("edit returned error: %v", err)
	}
	raw, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatalf("read app managed file: %v", err)
	}
	if string(raw) != "new\n" {
		t.Fatalf("app managed file content = %q, want updated content", raw)
	}
}

func marshalFileToolInput(t *testing.T, input fileToolInput) json.RawMessage {
	t.Helper()
	req, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal file tool input: %v", err)
	}
	return req
}
