package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// TestMcpLSPToolPlainTextContractB2A 锁定 hierarchy 和 semantic tokens 的紧凑纯文本契约。
func TestMcpLSPToolPlainTextContractB2A(t *testing.T) {
	t.Run("call hierarchy limits nested edges and call sites", testCallHierarchyNestedLimit)
	t.Run("call hierarchy both direction allocates fairly", testCallHierarchyBothFairness)
	t.Run("hierarchy final text budget synchronizes showing", testHierarchyFinalTextBudget)
	t.Run("type hierarchy limits nested edges", testTypeHierarchyNestedLimit)
	runSemanticTokensPlainTextContract(t)
}

func runSemanticTokensPlainTextContract(t *testing.T) {
	t.Helper()
	t.Run("semantic tokens decode advertised legend", testSemanticTokensLegendDecode)
	t.Run("semantic tokens reject missing legend", testSemanticTokensMissingLegend)
	t.Run("semantic tokens reject malformed tuple", testSemanticTokensMalformedTuple)
	t.Run("semantic tokens reject legend index overflow", testSemanticTokensLegendIndexOverflow)
}

func testHierarchyFinalTextBudget(t *testing.T) {
	longValue := strings.Repeat("hierarchy-budget-segment-", 24)
	rows := make([]hierarchyEdgeRow, 5)
	for i := range rows {
		rows[i] = hierarchyEdgeRow{
			Direction: "outgoing",
			Item: compactHierarchyItem{
				Name: fmt.Sprintf("edge_%d_%s", i, longValue), Kind: int(protocol.SymbolKindFunction),
				File: "targets/" + longValue + ".go", Line: i + 1, Col: 1,
			},
			Sites:      []compactHierarchyLocation{{File: "sites/" + longValue + ".go", Line: i + 1, Col: 1}},
			SitesTotal: 5, HasCallSites: true,
		}
	}
	response := hierarchyEdgeListResponse{Rows: rows, Total: 5, Unit: "edge"}
	if size := len([]byte(response.renderPlainText())); size <= hierarchyContentMaxBytes {
		t.Fatalf("uncapped hierarchy text = %d bytes, want >%d", size, hierarchyContentMaxBytes)
	}

	text := response.ToPlainText()
	if size := len([]byte(text)); size > hierarchyContentMaxBytes {
		t.Fatalf("capped hierarchy text = %d bytes, want <=%d", size, hierarchyContentMaxBytes)
	}
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse capped hierarchy text: %v", err)
	}
	rowCount := countProtocolRecords(doc.Records, "ROW")
	assertCappedHierarchyHeader(t, doc.Header, rowCount)
}

func countProtocolRecords(records []lineprotocol.Record, kind string) int {
	rowCount := 0
	for _, record := range records {
		if record.Kind == kind {
			rowCount++
		}
	}
	return rowCount
}

func assertCappedHierarchyHeader(t *testing.T, header lineprotocol.Header, rowCount int) {
	t.Helper()
	if header.Total != 5 {
		t.Errorf("capped hierarchy total = %d, want 5", header.Total)
	}
	if header.Showing != rowCount {
		t.Errorf("capped hierarchy showing = %d, ROW count = %d", header.Showing, rowCount)
	}
	if !header.Truncated {
		t.Error("capped hierarchy truncated = 0, want 1")
	}
	if rowCount >= 5 {
		t.Errorf("capped hierarchy ROW count = %d, want <5 to prove budget trimming", rowCount)
	}
}

func testCallHierarchyNestedLimit(t *testing.T) {
	root, target := writeWorkspaceSelectorFixture(t)
	manager := &structureTestManager{callHierarchy: makeNestedCallHierarchy(target, 0, 41, 10)}
	result, err := runCallHierarchy(plainTextToolScope(root), manager, target, protocol.Position{}, xrefParams{
		Direction: "outgoing", MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("runCallHierarchy() error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	assertProtocolHeaderAndRows(t, text, "OK total=41 showing=5 truncated=1 unit=edge", 5)
	if len([]byte(text)) > 4*1024 {
		t.Errorf("call hierarchy text = %d bytes, want <=4096", len([]byte(text)))
	}
	for _, line := range protocolRows(text) {
		for _, want := range []string{"sites_total=10", "sites_showing=1", "sites_truncated=1"} {
			if !strings.Contains(line, want) {
				t.Errorf("call hierarchy row missing %q: %q", want, line)
			}
		}
	}
}

func testCallHierarchyBothFairness(t *testing.T) {
	root, target := writeWorkspaceSelectorFixture(t)
	manager := &structureTestManager{callHierarchy: makeNestedCallHierarchy(target, 6, 6, 1)}
	result, err := runCallHierarchy(plainTextToolScope(root), manager, target, protocol.Position{}, xrefParams{
		Direction: "both", MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("runCallHierarchy(both) error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	assertProtocolHeaderAndRows(t, text, "OK total=12 showing=5 truncated=1 unit=edge", 5)
	wantDirections := []string{"incoming", "outgoing", "incoming", "outgoing", "incoming"}
	for i, line := range protocolRows(text) {
		if !strings.Contains(line, "direction="+wantDirections[i]) {
			t.Errorf("both direction row %d = %q, want %s", i, line, wantDirections[i])
		}
	}
}

func testTypeHierarchyNestedLimit(t *testing.T) {
	root, target := writeWorkspaceSelectorFixture(t)
	manager := &hierarchyContractManager{structureTestManager: &structureTestManager{}, types: makeNestedTypeHierarchy(target, 41)}
	result, err := runTypeHierarchy(plainTextToolScope(root), manager, target, protocol.Position{}, xrefParams{
		Direction: "subtypes", MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("runTypeHierarchy() error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	assertProtocolHeaderAndRows(t, text, "OK total=41 showing=5 truncated=1 unit=type_edge", 5)
}

func testSemanticTokensLegendDecode(t *testing.T) {
	manager := semanticContractManager([]int{
		0, 0, 4, 2, 1,
		1, 0, 5, 3, 2,
		1, 2, 3, 0, 0,
		1, 0, 4, 1, 1,
		1, 0, 4, 2, 1,
		1, 0, 4, 3, 0,
	})
	result, err := runSemanticContract(t, manager, 5)
	if err != nil {
		t.Fatalf("runSemanticTokens(valid) error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	assertProtocolHeaderAndRows(t, text, "OK total=6 showing=5 truncated=1 unit=token", 5)
	for _, want := range []string{"LEGEND\t", "type=function", "modifiers=declaration"} {
		if !strings.Contains(text, want) {
			t.Errorf("semantic tokens text missing %q: %q", want, text)
		}
	}
	rows := protocolRows(text)
	if len(rows) < 3 {
		t.Fatalf("semantic token rows = %d, want at least 3; text=%q", len(rows), text)
	}
	first := decodeProtocolRowForTest(t, rows[0])
	third := decodeProtocolRowForTest(t, rows[2])
	for key, want := range map[string]string{"line": "1", "col": "1", "length": "4", "type": "function", "modifiers": "declaration"} {
		if first[key] != want {
			t.Errorf("first semantic token %s = %q, want %q", key, first[key], want)
		}
	}
	if third["line"] != "3" || third["col"] != "3" {
		t.Errorf("third semantic token position = %s:%s, want 3:3", third["line"], third["col"])
	}
}

func testSemanticTokensMissingLegend(t *testing.T) {
	manager := semanticContractManager([]int{0, 0, 4, 0, 0})
	manager.tokenTypes = nil
	_, err := runSemanticContract(t, manager, 5)
	assertCodedToolError(t, err, "lsp_protocol_error", false)
}

func testSemanticTokensMalformedTuple(t *testing.T) {
	manager := semanticContractManager([]int{0, 0, 4, 0})
	_, err := runSemanticContract(t, manager, 5)
	assertCodedToolError(t, err, "lsp_protocol_error", false)
}

func testSemanticTokensLegendIndexOverflow(t *testing.T) {
	for name, data := range map[string][]int{
		"token type":      {0, 0, 4, 99, 0},
		"modifier bitset": {0, 0, 4, 0, 4},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runSemanticContract(t, semanticContractManager(data), 5)
			assertCodedToolError(t, err, "lsp_protocol_error", false)
		})
	}
}

type hierarchyContractManager struct {
	*structureTestManager
	types []protocol.TypeHierarchyResult
}

func (m *hierarchyContractManager) TypeHierarchy(context.Context, string, protocol.Position, string) ([]protocol.TypeHierarchyResult, error) {
	return m.types, nil
}

type semanticLegendContractManager struct {
	*structureTestManager
	result         *protocol.SemanticTokensResult
	tokenTypes     []string
	tokenModifiers []string
}

func semanticContractManager(data []int) *semanticLegendContractManager {
	return &semanticLegendContractManager{
		structureTestManager: &structureTestManager{},
		result:               &protocol.SemanticTokensResult{Data: data},
		tokenTypes:           []string{"namespace", "type", "function", "variable"},
		tokenModifiers:       []string{"declaration", "readonly"},
	}
}

func (m *semanticLegendContractManager) SemanticTokens(context.Context, string) (*protocol.SemanticTokensResult, error) {
	return m.result, nil
}

func (m *semanticLegendContractManager) SemanticTokensLegend(context.Context, string) ([]string, []string, error) {
	return m.tokenTypes, m.tokenModifiers, nil
}

func runSemanticContract(t *testing.T, manager *semanticLegendContractManager, limit int) (any, error) {
	t.Helper()
	root, target := writeWorkspaceSelectorFixture(t)
	return runSemanticTokens(plainTextToolScope(root), manager, structureParams{FilePath: target, MaxResults: limit})
}

func makeNestedCallHierarchy(filePath string, incomingCount, outgoingCount, sites int) []protocol.CallHierarchyResult {
	result := protocol.CallHierarchyResult{Item: makeCallHierarchyItem("root", filePath, 1, 1)}
	for i := range incomingCount {
		result.Incoming = append(result.Incoming, protocol.CallHierarchyIncomingCall{
			From: makeCallHierarchyItem(fmt.Sprintf("incoming_%02d", i), filePath, i+2, 1), FromRanges: makeContractRanges(sites),
		})
	}
	for i := range outgoingCount {
		result.Outgoing = append(result.Outgoing, protocol.CallHierarchyOutgoingCall{
			To: makeCallHierarchyItem(fmt.Sprintf("outgoing_%02d", i), filePath, i+2, 1), FromRanges: makeContractRanges(sites),
		})
	}
	return []protocol.CallHierarchyResult{result}
}

func makeContractRanges(count int) []protocol.Range {
	ranges := make([]protocol.Range, count)
	for i := range ranges {
		ranges[i] = makeRange(i+1, 1)
	}
	return ranges
}

func makeNestedTypeHierarchy(filePath string, count int) []protocol.TypeHierarchyResult {
	result := protocol.TypeHierarchyResult{Item: makeTypeHierarchyItem("root", filePath, 1)}
	for i := range count {
		result.Subtypes = append(result.Subtypes, makeTypeHierarchyItem(fmt.Sprintf("subtype_%02d", i), filePath, i+2))
	}
	return []protocol.TypeHierarchyResult{result}
}

func makeTypeHierarchyItem(name, filePath string, line int) protocol.TypeHierarchyItem {
	return protocol.TypeHierarchyItem{
		Name: name, Kind: int(protocol.SymbolKindClass), URI: fileURI(filePath),
		Range: makeRange(line, 1), SelectionRange: makeRange(line, 1),
	}
}
