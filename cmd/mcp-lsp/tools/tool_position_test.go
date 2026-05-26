package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestResolveFilePositionRequestRejectsColumnBeyondLineWithLLMHint(t *testing.T) {
	dir := t.TempDir()
	writePositionFixture(t, dir, "sample.go", "package sample\n\nfunc demo() {\n\tvalue := compute(input)\n}\n")

	_, _, err := resolveFilePositionRequest(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), filePositionParams{
		FilePath: "sample.go",
		Line:     4,
		Column:   80,
	})
	if err == nil {
		t.Fatalf("resolveFilePositionRequest returned nil error, want position_out_of_range")
	}

	var coded *common.CodedToolError
	if !errors.As(err, &coded) {
		t.Fatalf("error type = %T, want *common.CodedToolError", err)
	}
	if coded.Code != "position_out_of_range" {
		t.Fatalf("code = %q, want position_out_of_range", coded.Code)
	}
	if coded.Retryable {
		t.Fatalf("retryable = true, want false")
	}
	if !strings.Contains(coded.Hint, "Retry with a column inside the target identifier") {
		t.Fatalf("hint = %q, want retry guidance", coded.Hint)
	}
	if got := coded.Meta["line_text"]; got != "\tvalue := compute(input)" {
		t.Fatalf("line_text = %#v, want source line", got)
	}
	if got := coded.Meta["line_length"]; got != 24 {
		t.Fatalf("line_length = %#v, want 24", got)
	}
	if got := coded.Meta["requested_column"]; got != 80 {
		t.Fatalf("requested_column = %#v, want 80", got)
	}
	if got := coded.Meta["suggested_columns"]; got == nil {
		t.Fatalf("suggested_columns missing from meta")
	}
}

func TestResolveFilePositionRequestRejectsLineBeyondFileWithLLMHint(t *testing.T) {
	dir := t.TempDir()
	writePositionFixture(t, dir, "sample.go", "package sample\n")

	_, _, err := resolveFilePositionRequest(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), filePositionParams{
		FilePath: "sample.go",
		Line:     12,
		Column:   1,
	})
	if err == nil {
		t.Fatalf("resolveFilePositionRequest returned nil error, want line_out_of_range")
	}

	var coded *common.CodedToolError
	if !errors.As(err, &coded) {
		t.Fatalf("error type = %T, want *common.CodedToolError", err)
	}
	if coded.Code != "line_out_of_range" {
		t.Fatalf("code = %q, want line_out_of_range", coded.Code)
	}
	if got := coded.Meta["line_count"]; got != 2 {
		t.Fatalf("line_count = %#v, want 2", got)
	}
	if !strings.Contains(coded.Hint, "Read the target file") {
		t.Fatalf("hint = %q, want read_file guidance", coded.Hint)
	}
}

func writePositionFixture(t *testing.T, dir string, name string, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}
