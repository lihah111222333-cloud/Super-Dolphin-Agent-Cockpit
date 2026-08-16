package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// TestMcpLSPToolPlainTextContractB2B 锁定非原生 MQL 归因和 rename 回执契约。
func TestMcpLSPToolPlainTextContractB2B(t *testing.T) {
	runB2BToolResultContracts(t)
	runB2BBoundaryContracts(t)
}

func runB2BToolResultContracts(t *testing.T) {
	t.Helper()
	t.Run("mqh completion declares non-native clangd attribution", testMQHCompletionAttribution)
	t.Run("edit receipts use one line protocol channel", testEditReceiptLineProtocol)
	t.Run("empty quickfix suggests retry without only", testEmptyQuickfixHint)
	t.Run("invalid rename parameters are typed", testInvalidRenameParameters)
}

func runB2BBoundaryContracts(t *testing.T) {
	t.Helper()
	t.Run("compact list budget measures final text", testCompactListFinalTextBudget)
	t.Run("unknown formatter fails without JSON fallback", testUnknownFormatterFailsStrictly)
}

func testMQHCompletionAttribution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Include", "common.mqh")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create .mqh fixture directory: %v", err)
	}
	if err := os.WriteFile(target, []byte("int common_value;\n"), 0o600); err != nil {
		t.Fatalf("write .mqh fixture: %v", err)
	}
	manager := &structureTestManager{completionItems: []protocol.CompletionItem{{Label: "MACOS_ONLY_CPP_MACRO"}}}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewCompletionHandler(registry)
	result, err := callPlainTextContractHandler(t, handler, root, completionParams{Pos: target + ":1:1", MaxResults: 5})
	if err != nil {
		t.Fatalf("real completion handler (.mqh) error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	for _, want := range []string{
		"ATTR\tlanguage_id=cpp\tserver=clangd\tnative=0\tcompatibility=mql-via-clangd",
		"WARNING\t",
		"not-native-MetaEditor-MQL5-semantics",
	} {
		if !strings.Contains(text, want) {
			t.Errorf(".mqh content missing %q: %q", want, text)
		}
	}
	for _, forbidden := range []string{"pid=", "executable=", "arguments="} {
		if strings.Contains(text, forbidden) {
			t.Errorf(".mqh attribution exposed %q: %q", forbidden, text)
		}
	}
}

func testEditReceiptLineProtocol(t *testing.T) {
	applied := editEnvelope{Status: "applied", AppliedCount: 2, Persisted: true, FilePath: "dir/main.go"}.ToPlainText()
	doc := parseB2BLineProtocol(t, applied)
	if doc.Header != (lineprotocol.Header{Total: 2, Showing: 2, Unit: "edit"}) {
		t.Errorf("applied edit header = %+v, want total=2 showing=2 truncated=0 unit=edit", doc.Header)
	}
	if len(doc.Records) != 1 || doc.Records[0].Kind != "FILE" || doc.Records[0].Fields["path"] != "dir/main.go" {
		t.Errorf("applied edit records = %#v, want one FILE path record", doc.Records)
	}

	noChange := (replaceRangeResult{
		editEnvelope: editEnvelope{Status: "no_change", Message: "replacement already present", FilePath: "dir/main.go"},
	}).ToPlainText()
	noChangeDoc := parseB2BLineProtocol(t, noChange)
	if noChangeDoc.Header != (lineprotocol.Header{Total: 0, Showing: 0, Unit: "edit"}) {
		t.Errorf("no-change edit header = %+v, want empty edit receipt", noChangeDoc.Header)
	}
	for _, forbidden := range []string{"patch matched", "Matched by", "matched_by"} {
		if strings.Contains(noChange, forbidden) {
			t.Errorf("no-change receipt contains obsolete match claim %q: %q", forbidden, noChange)
		}
	}
}

func testEmptyQuickfixHint(t *testing.T) {
	result := emptyCodeActionResult(EditRequest{Action: "code_action", Pos: "main.go:3:1", Only: []string{"quickfix"}})
	doc := parseB2BLineProtocol(t, plainTextForContractResult(t, result))
	if doc.Header != (lineprotocol.Header{Total: 0, Showing: 0, Unit: "edit"}) {
		t.Errorf("empty code-action header = %+v, want empty edit receipt", doc.Header)
	}
	for _, record := range doc.Records {
		if record.Kind == "HINT" && strings.Contains(record.Value, "without only") {
			return
		}
	}
	t.Errorf("empty code-action records = %#v, want actionable HINT without only", doc.Records)
}

func testInvalidRenameParameters(t *testing.T) {
	root, target := writeWorkspaceSelectorFixture(t)
	handler := newEditHandler(root, &structureTestRegistry{fileManager: &structureTestManager{}})
	_, err := callPlainTextContractHandler(t, handler.Handle, root, EditRequest{Action: "rename", Pos: target + ":1:1"})
	assertCodedToolError(t, err, "invalid_params", false)
}

func testCompactListFinalTextBudget(t *testing.T) {
	items := make([]format.CompactCompletionItem, 8)
	for i := range items {
		items[i] = format.CompactCompletionItem{Label: "Println", Kind: 3, Detail: strings.Repeat("x", 256)}
	}
	list := format.NewCompactList(items, len(items))
	text, handled := FormatToPlainText(list)
	if !handled {
		t.Fatal("compact completion fixture has no plain-text renderer")
	}
	handler := middleware.WithOutputBudget("completion", func(context.Context, json.RawMessage) (any, error) {
		return list, nil
	}, middleware.Budget{MaxBytes: len([]byte(text))})
	got, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("compact-list budget returned error: %v", err)
	}
	if _, ok := got.(format.CompactList[format.CompactCompletionItem]); !ok {
		t.Fatalf("compact-list budget result = %T, want original list because final text fits", got)
	}
}

func testUnknownFormatterFailsStrictly(t *testing.T) {
	type unknownResult struct{ Value string }
	_, err := common.BuildToolCallResultWithPolicy(
		unknownResult{Value: "must not become JSON"},
		common.NewTextOnlyToolCallResultPolicy(FormatToPlainText),
	)
	if err == nil || !strings.Contains(err.Error(), "no renderer") || !strings.Contains(err.Error(), "unknownResult") {
		t.Fatalf("unknown formatter error = %v, want diagnostic type and no-renderer failure", err)
	}
}

func parseB2BLineProtocol(t *testing.T, text string) lineprotocol.Document {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse B2b line protocol %q: %v", text, err)
	}
	return doc
}
