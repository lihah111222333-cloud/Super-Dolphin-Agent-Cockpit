package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestOpenFileResultUnifiedProtocol(t *testing.T) {
	result := openFileResult{
		Status:   "opened",
		FilePath: "dir\\tab\tline\n雪.go",
		Bytes:    17,
	}
	want := "OK total=1 showing=1 truncated=0 unit=file\n" +
		"ROW\tfile=dir\\\\tab\\tline\\n雪.go\tbytes=17\tstatus=opened"
	if got := result.ToPlainText(); got != want {
		t.Fatalf("openFileResult.ToPlainText() = %q, want %q", got, want)
	}
	assertOpenFileResultLegacyGuard(t)
	assertOpenFileFailureReturnsNilResult(t)
}

func assertOpenFileResultLegacyGuard(t *testing.T) {
	t.Helper()
	resultType := reflect.TypeFor[openFileResult]()
	fields := make([]string, 0, resultType.NumField())
	for field := range resultType.Fields() {
		fields = append(fields, field.Name)
	}
	wantFields := []string{"Status", "FilePath", "Bytes"}
	if !slices.Equal(fields, wantFields) {
		t.Fatalf("openFileResult fields = %v, want %v", fields, wantFields)
	}

	source, err := os.ReadFile("tool_file.go")
	if err != nil {
		t.Fatalf("read tool_file.go: %v", err)
	}
	for _, forbidden := range []string{"Successfully opened" + " file:", "Failed to open" + " file:"} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("tool_file.go retains legacy open_file renderer string %q", forbidden)
		}
	}
}

func assertOpenFileFailureReturnsNilResult(t *testing.T) {
	t.Helper()
	result, err := (handlerBase{}).handleFile(context.Background(), json.RawMessage(`{
		"action":"open_file","file_path":"missing.go"
	}`))
	if !errors.Is(err, errManagerUnavailable) {
		t.Fatalf("open_file error = %v, want %v", err, errManagerUnavailable)
	}
	if result != nil {
		t.Fatalf("open_file failure result = %#v, want nil for unified ERROR envelope", result)
	}
}
