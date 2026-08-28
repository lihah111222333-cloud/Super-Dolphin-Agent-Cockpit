package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

func TestStructureDocumentSymbolSkeletonRetentionAndFiltering(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "types.ts", "export const MAX_RETRY = 3;\nexport class OrderService {}\n")
	manager := &structureTestManager{documentSymbols: skeletonDocumentSymbols()}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action: "document_symbol", FilePath: target, MaxResults: 50,
	}))
	if err != nil {
		t.Fatalf("document_symbol returned error: %v", err)
	}
	assertSkeletonDocumentResponse(t, got)
}

func skeletonDocumentSymbols() []protocol.DocumentSymbol {
	return []protocol.DocumentSymbol{
		{Name: "MAX_RETRY", Kind: protocol.SymbolKindConstant, Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 27}}},
		{Name: "globalState", Kind: protocol.SymbolKindVariable, Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 1, Character: 25}}},
		{
			Name: "OrderService", Kind: protocol.SymbolKindClass,
			Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 0}, End: protocol.Position{Line: 30, Character: 1}},
			Children: []protocol.DocumentSymbol{
				{Name: "orderId", Kind: protocol.SymbolKindField, Range: protocol.Range{Start: protocol.Position{Line: 3, Character: 2}, End: protocol.Position{Line: 3, Character: 18}}},
				{Name: "status", Kind: protocol.SymbolKindProperty, Range: protocol.Range{Start: protocol.Position{Line: 4, Character: 2}, End: protocol.Position{Line: 4, Character: 17}}},
				{Name: "ActiveStatus", Kind: protocol.SymbolKindEnumMember, Range: protocol.Range{Start: protocol.Position{Line: 5, Character: 2}, End: protocol.Position{Line: 5, Character: 20}}},
				{Name: "SERVICE_TIMEOUT", Kind: protocol.SymbolKindConstant, Range: protocol.Range{Start: protocol.Position{Line: 6, Character: 2}, End: protocol.Position{Line: 6, Character: 30}}},
				{
					Name: "processOrder", Kind: protocol.SymbolKindMethod,
					Range: protocol.Range{Start: protocol.Position{Line: 8, Character: 2}, End: protocol.Position{Line: 20, Character: 3}},
					Children: []protocol.DocumentSymbol{{
						Name: "tempBuffer", Kind: protocol.SymbolKindVariable,
						Range: protocol.Range{Start: protocol.Position{Line: 9, Character: 4}, End: protocol.Position{Line: 9, Character: 25}},
					}},
				},
			},
		},
	}
}

func assertSkeletonDocumentResponse(t *testing.T, got any) {
	t.Helper()
	resp := got.(documentSymbolListResponse)
	plainText := resp.ToPlainText()
	if resp.Total != 8 || resp.Showing != 8 || resp.Truncated {
		t.Fatalf("filtered document symbol response = total:%d showing:%d truncated:%v, want 8/8/false", resp.Total, resp.Showing, resp.Truncated)
	}
	if count := countDocumentSymbolNodes(resp.Data); count != resp.Showing {
		t.Fatalf("structured document symbol node count = %d, showing = %d", count, resp.Showing)
	}
	assertSkeletonStructuredData(t, resp)
	assertSkeletonPlainText(t, resp, plainText)
}

func assertSkeletonStructuredData(t *testing.T, resp documentSymbolListResponse) {
	t.Helper()
	structuredData, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal structured document symbols: %v", err)
	}
	if strings.Contains(string(structuredData), "tempBuffer") {
		t.Fatalf("structured response unexpectedly contains local block variable 'tempBuffer': %s", structuredData)
	}
}

func assertSkeletonPlainText(t *testing.T, resp documentSymbolListResponse, plainText string) {
	t.Helper()
	document, err := lineprotocol.Parse(plainText)
	if err != nil {
		t.Fatalf("parse filtered document symbol text: %v", err)
	}
	rowCount := 0
	for _, record := range document.Records {
		if record.Kind == "ROW" {
			rowCount++
		}
	}
	if document.Header.Total != resp.Total || document.Header.Showing != resp.Showing || rowCount != resp.Showing {
		t.Fatalf("plain-text symbol counts = total:%d showing:%d rows:%d, response = total:%d showing:%d; text=%q", document.Header.Total, document.Header.Showing, rowCount, resp.Total, resp.Showing, plainText)
	}
	for _, want := range []string{"MAX_RETRY", "globalState", "OrderService", "orderId", "status", "ActiveStatus", "SERVICE_TIMEOUT", "processOrder"} {
		if !strings.Contains(plainText, want) {
			t.Errorf("plain text missing expected skeleton symbol %q; output:\n%s", want, plainText)
		}
	}
	if strings.Contains(plainText, "tempBuffer") {
		t.Fatalf("plain text unexpectedly contains local block variable 'tempBuffer'; output:\n%s", plainText)
	}
}

func TestStructureDocumentSymbolFiltersBeforePagination(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "pagination.go", "package sample\n")
	manager := &structureTestManager{documentSymbols: paginationDocumentSymbols()}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action: "document_symbol", FilePath: target, MaxResults: 3,
	}))
	if err != nil {
		t.Fatalf("document_symbol returned error: %v", err)
	}
	assertPaginationDocumentResponse(t, got)
}

func paginationDocumentSymbols() []protocol.DocumentSymbol {
	return []protocol.DocumentSymbol{{
		Name: "Service", Kind: protocol.SymbolKindClass,
		Children: []protocol.DocumentSymbol{
			{Name: "run", Kind: protocol.SymbolKindMethod, Children: []protocol.DocumentSymbol{{Name: "localState", Kind: protocol.SymbolKindVariable}}},
			{Name: "Status", Kind: protocol.SymbolKindProperty},
		},
	}}
}

func assertPaginationDocumentResponse(t *testing.T, got any) {
	t.Helper()
	resp := got.(documentSymbolListResponse)
	if resp.Total != 3 || resp.Showing != 3 || resp.Truncated {
		t.Fatalf("filtered pagination response = total:%d showing:%d truncated:%v, want 3/3/false", resp.Total, resp.Showing, resp.Truncated)
	}
	if count := countDocumentSymbolNodes(resp.Data); count != 3 {
		t.Fatalf("structured filtered node count = %d, want 3", count)
	}
	plainText := resp.ToPlainText()
	assertPaginationPlainText(t, plainText)
}

func assertPaginationPlainText(t *testing.T, plainText string) {
	t.Helper()
	document, err := lineprotocol.Parse(plainText)
	if err != nil {
		t.Fatalf("parse filtered pagination text: %v", err)
	}
	rowCount := 0
	for _, record := range document.Records {
		if record.Kind == "ROW" {
			rowCount++
		}
	}
	if document.Header.Total != 3 || document.Header.Showing != 3 || rowCount != 3 {
		t.Fatalf("plain-text filtered pagination = total:%d showing:%d rows:%d, want 3/3/3; text=%q", document.Header.Total, document.Header.Showing, rowCount, plainText)
	}
	if strings.Contains(plainText, "localState") {
		t.Fatalf("plain text unexpectedly contains filtered localState: %s", plainText)
	}
	for _, want := range []string{"Service", "run", "Status"} {
		if !strings.Contains(plainText, want) {
			t.Fatalf("plain text missing visible symbol %q: %s", want, plainText)
		}
	}
}
