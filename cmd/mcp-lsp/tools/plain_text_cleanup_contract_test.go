package tools

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
)

// TestMcpLSPPlainTextCleanupContract 锁定输出预算和废弃结构化通道的清理边界。
func TestMcpLSPPlainTextCleanupContract(t *testing.T) {
	t.Run("batch file budget measures final text", testBatchFileFinalTextBudget)
	t.Run("compact list keeps every retained row in text", testCompactListKeepsRetainedRows)
	t.Run("edit request omits structured response switch", testEditRequestOmitsResponseDetail)
	t.Run("production sources omit structured output dead chain", testProductionSourcesOmitStructuredOutputDeadChain)
}

func testBatchFileFinalTextBudget(t *testing.T) {
	response := batchReadResponse{
		Success: true, Total: 1, Showing: 1,
		Data: []batchReadItem{{FilePath: "many-lines.txt", Success: true, Content: strings.Repeat("\n", lspReadFileBatchPayloadMax/2)}},
		Meta: batchReadMeta{RequestedCount: 1, MaxBatch: lspReadFileBatchMax},
	}
	if size := len([]byte(response.ToPlainText())); size > lspReadFileBatchPayloadMax {
		t.Fatalf("fixture final text = %d bytes, want <=%d", size, lspReadFileBatchPayloadMax)
	}
	if !fitsBatchPayload(response) {
		t.Fatal("fitsBatchPayload rejected a response whose final plain text is within budget")
	}
}

func testCompactListKeepsRetainedRows(t *testing.T) {
	items := []format.CompactCompletionItem{
		{Label: "first"}, {Label: "second"}, {Label: "third"}, {Label: "fourth"},
	}
	text, handled := FormatToPlainText(format.NewCompactList(items, len(items)))
	if !handled {
		t.Fatal("compact completion list has no formatter")
	}
	if !strings.Contains(text, "fourth") {
		t.Errorf("compact text omitted a retained row: %q", text)
	}
	if strings.Contains(text, "structuredContent") {
		t.Errorf("compact text promises deleted structured output: %q", text)
	}
}

func testEditRequestOmitsResponseDetail(t *testing.T) {
	if _, present := reflect.TypeFor[EditRequest]().FieldByName("ResponseDetail"); present {
		t.Fatal("EditRequest still exposes deprecated ResponseDetail")
	}
}

func testProductionSourcesOmitStructuredOutputDeadChain(t *testing.T) {
	for file, forbidden := range map[string][]string{
		"formatter.go":         {"structuredContent", "默认 JSON 渲染"},
		"tool_edit.go":         {"ResponseDetail", "response_detail", "fullEditResponseDetail"},
		"tool_edit_replace.go": {"structuredContent"},
		"tool_file.go":         {"structuredContent", "json.Marshal(resp)"},
	} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read production source %s: %v", file, err)
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(source), fragment) {
				t.Errorf("production source %s retains %q", file, fragment)
			}
		}
	}
}
