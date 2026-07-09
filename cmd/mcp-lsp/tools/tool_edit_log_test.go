package tools

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestReplaceRangeEmitsStageLogsWithoutPatchContent(t *testing.T) {
	var logs bytes.Buffer
	pkglogger.SetForTest(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() {
		pkglogger.SetForTest(slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})))
	})

	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("alpha\nold SECRET_PATCH_TOKEN\nomega\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch: strings.Join([]string{
			"@@",
			"-old SECRET_PATCH_TOKEN",
			"+new safe value",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("marshal edit input: %v", err)
	}

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("edit returned error: %v", err)
	}
	result, ok := got.(replaceRangeResult)
	if !ok {
		t.Fatalf("edit result type = %T, want replaceRangeResult", got)
	}
	if result.Status != "applied" {
		t.Fatalf("edit status = %q, want applied", result.Status)
	}

	text := logs.String()
	for _, want := range []string{
		`"msg":"mcp-lsp patch_edit stage started"`,
		`"msg":"mcp-lsp patch_edit stage completed"`,
		`"stage":"workspace_roots"`,
		`"stage":"file_lock"`,
		`"stage":"read_file"`,
		`"stage":"manager_lookup"`,
		`"stage":"build_plan"`,
		`"stage":"write_file"`,
		`"stage":"lsp_sync"`,
		`"status":"skipped"`,
		`"reason":"manager_nil"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("logs missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, "SECRET_PATCH_TOKEN") {
		t.Fatalf("stage logs leaked patch content:\n%s", text)
	}
}
