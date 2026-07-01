package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestResolveFilePositionRequestAllowsEndOfLineColumn(t *testing.T) {
	dir := t.TempDir()
	writePositionFixture(t, dir, "sample.go", "package sample\n\nfunc demo() {\n\tvalue.\n}\n")

	_, position, err := resolveFilePositionRequest(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), filePositionParams{
		Pos: "sample.go:4:8",
	})
	if err != nil {
		t.Fatalf("resolveFilePositionRequest returned error: %v", err)
	}
	want := protocol.Position{Line: 3, Character: 7}
	if position != want {
		t.Fatalf("position = %#v, want %#v", position, want)
	}
}

func TestResolveLSPPositionConvertsEmojiColumn(t *testing.T) {
	dir := t.TempDir()
	writePositionFixture(t, dir, "emoji.go", "ab😀cd\n")

	_, position, err := resolveFilePositionRequest(testToolContext(dir), filePositionParams{
		Pos: "emoji.go:1:4",
	})
	if err != nil {
		t.Fatalf("resolveFilePositionRequest returned error: %v", err)
	}
	want := protocol.Position{Line: 0, Character: 4}
	if position != want {
		t.Fatalf("position = %#v, want UTF-16 position %#v", position, want)
	}
}

func TestIdentifierCompletionRetryPositionsIncludeUnderscoreSuffixStart(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "constant.py")
	writePositionFixture(t, dir, "constant.py", "REG_CRYPTO = \"crypto\"\n")

	positions, err := identifierCompletionRetryPositions(target, protocol.Position{Line: 0, Character: 6})
	if err != nil {
		t.Fatalf("identifierCompletionRetryPositions returned error: %v", err)
	}
	got := make([]int, 0, len(positions))
	for _, position := range positions {
		got = append(got, position.Character)
	}
	want := []int{10, 9, 4, 0}
	if len(got) != len(want) {
		t.Fatalf("retry characters = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("retry characters = %#v, want %#v", got, want)
		}
	}
}

func TestCompletionRetryUsesUTF16PositionAfterEmoji(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "constant.py")
	writePositionFixture(t, dir, "constant.py", "😀REG_CRYPTO = \"crypto\"\n")
	manager := &utf16CompletionRetryManager{}

	result, err := completionWithIdentifierEndRetry(context.Background(), manager, target, protocol.Position{Line: 0, Character: 6})
	if err != nil {
		t.Fatalf("completionWithIdentifierEndRetry returned error: %v", err)
	}
	if result == nil || len(result.Items) != 1 {
		t.Fatalf("completion result = %#v, want one retry item", result)
	}
	if len(manager.positions) < 2 {
		t.Fatalf("completion positions = %#v, want original plus retry positions", manager.positions)
	}
	if got := manager.positions[1]; got != (protocol.Position{Line: 0, Character: 12}) {
		t.Fatalf("first retry position = %#v, want UTF-16 identifier end", got)
	}
}

func TestResolveFilePositionRequestRejectsColumnBeyondLineWithLLMHint(t *testing.T) {
	dir := t.TempDir()
	writePositionFixture(t, dir, "sample.go", "package sample\n\nfunc demo() {\n\tvalue := compute(input)\n}\n")

	_, _, err := resolveFilePositionRequest(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), filePositionParams{
		Pos: "sample.go:4:80",
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
	if !strings.Contains(coded.Hint, "next: retry with pos=") {
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
		Pos: "sample.go:12:1",
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
	if !strings.Contains(coded.Hint, "next: file action=read_file") {
		t.Fatalf("hint = %q, want read_file guidance", coded.Hint)
	}
}

type utf16CompletionRetryManager struct {
	structureTestManager
	positions []protocol.Position
}

func (m *utf16CompletionRetryManager) Completion(_ context.Context, _ string, position protocol.Position) (*protocol.CompletionList, error) {
	m.positions = append(m.positions, position)
	if position.Line == 0 && position.Character == 12 {
		return &protocol.CompletionList{Items: []protocol.CompletionItem{{Label: "REG_CRYPTO"}}}, nil
	}
	return &protocol.CompletionList{}, nil
}

func writePositionFixture(t *testing.T, dir string, name string, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}
