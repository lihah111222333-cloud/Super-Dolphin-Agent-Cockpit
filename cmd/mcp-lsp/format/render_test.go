package format

import (
	"reflect"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestNormalizeForDisplayRegistryParity(t *testing.T) {
	location := protocol.Location{URI: "file:///tmp/main.go"}
	hover := protocol.HoverResult{}
	workspaceEdit := &protocol.WorkspaceEdit{}
	semanticTokens := &protocol.SemanticTokensResult{}
	values := []any{
		protocol.Location{}, &location, protocol.HoverResult{}, &hover,
		[]protocol.LocationResult{}, []protocol.DocumentSymbol{}, workspaceEdit,
		[]protocol.TextEdit{}, []protocol.Diagnostic{}, []protocol.CodeActionResult{},
		[]protocol.WorkspaceSymbolResult{}, []protocol.CallHierarchyResult{},
		[]protocol.TypeHierarchyResult{}, semanticTokens, protocol.FoldingRange{},
		[]protocol.FoldingRange{},
	}

	dispatch := newDisplayNormalizerDispatch()
	for _, value := range values {
		normalized, ok := dispatch.normalize(value)
		if !ok {
			t.Fatalf("normalize(%T) was not registered", value)
		}
		if reflect.TypeOf(normalized) != reflect.TypeOf(value) {
			t.Fatalf("normalize(%T) returned %T", value, normalized)
		}
	}
}

func TestNormalizeForDisplayUnknownPassthrough(t *testing.T) {
	type unknown struct{ Value int }
	input := &unknown{Value: 7}
	if got := NormalizeForDisplay(input); got != input {
		t.Fatalf("unknown pointer was not passed through: got %p want %p", got, input)
	}
}

func TestNormalizeForDisplayPointerAndSliceNormalizers(t *testing.T) {
	location := &protocol.Location{URI: "file:///tmp/main.go", Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 1}}}
	gotLocation := NormalizeForDisplay(location)
	if gotLocation == location {
		t.Fatal("location normalizer reused the input pointer")
	}
	if gotLocation.Range.Start.Line != 1 || gotLocation.Range.Start.Character != 2 {
		t.Fatalf("location coordinates = %+v, want 1-based coordinates", gotLocation.Range.Start)
	}

	inputSymbols := []protocol.DocumentSymbol{{Name: "main", Range: protocol.Range{Start: protocol.Position{Line: 0}}, SelectionRange: protocol.Range{Start: protocol.Position{Line: 1}}}}
	gotSymbols := NormalizeForDisplay(inputSymbols)
	if &gotSymbols[0] == &inputSymbols[0] {
		t.Fatal("slice normalizer reused the input backing array")
	}
	if gotSymbols[0].Range.Start.Line != 1 || gotSymbols[0].SelectionRange.Start.Line != 2 {
		t.Fatalf("document symbol coordinates = %+v/%+v, want 1-based coordinates", gotSymbols[0].Range.Start, gotSymbols[0].SelectionRange.Start)
	}
}

func TestNormalizeForDisplayConcurrent(t *testing.T) {
	const workers = 16
	const iterations = 200
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Go(func() {
			for j := 0; j < iterations; j++ {
				location := protocol.Location{Range: protocol.Range{Start: protocol.Position{Line: j}}}
				_ = NormalizeForDisplay(location)
				_ = NormalizeForDisplay([]protocol.FoldingRange{{StartLine: j}})
			}
		})
	}
	wg.Wait()
}
