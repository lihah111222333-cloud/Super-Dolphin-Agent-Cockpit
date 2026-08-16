package format

import (
	"reflect"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

func TestRenderGroupedLocationsUsesLineProtocol(t *testing.T) {
	result := protocol.GroupedLocationResult{
		Data: map[string][]protocol.CompactLocation{
			"zeta.go":  {{Line: 7, Col: 3, FuncStart: 5, FuncEnd: 9}},
			"alpha.go": {{Line: 2, Col: 1}},
		},
		Total: 4,
		Hint:  "narrow the symbol scope",
	}
	text := RenderGroupedLocations(result)
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse grouped locations: %v; text=%q", err, text)
	}
	wantHeader := lineprotocol.Header{Total: 4, Showing: 2, Truncated: true, Unit: "location"}
	if doc.Header != wantHeader {
		t.Fatalf("header = %+v, want %+v", doc.Header, wantHeader)
	}
	if len(doc.Records) != 3 || doc.Records[0].Kind != "ROW" || doc.Records[1].Kind != "ROW" || doc.Records[2].Kind != "HINT" {
		t.Fatalf("records = %#v, want two rows and one hint", doc.Records)
	}
	if got := doc.Records[0].Fields["file"]; got != "alpha.go" {
		t.Fatalf("first row file = %q, want deterministic alpha.go", got)
	}
	if got := doc.Records[1].Fields["file"]; got != "zeta.go" {
		t.Fatalf("second row file = %q, want retained zeta.go", got)
	}
}

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

func TestDisplayNormalizerPointerTypeKeysPreserveTypedNil(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		typeKey   reflect.Type
		normalize func() any
	}{
		{
			name:      "location",
			value:     (*protocol.Location)(nil),
			typeKey:   reflect.TypeFor[*protocol.Location](),
			normalize: func() any { return NormalizeForDisplay((*protocol.Location)(nil)) },
		},
		{
			name:      "hover result",
			value:     (*protocol.HoverResult)(nil),
			typeKey:   reflect.TypeFor[*protocol.HoverResult](),
			normalize: func() any { return NormalizeForDisplay((*protocol.HoverResult)(nil)) },
		},
		{
			name:      "workspace edit",
			value:     (*protocol.WorkspaceEdit)(nil),
			typeKey:   reflect.TypeFor[*protocol.WorkspaceEdit](),
			normalize: func() any { return NormalizeForDisplay((*protocol.WorkspaceEdit)(nil)) },
		},
		{
			name:      "semantic tokens result",
			value:     (*protocol.SemanticTokensResult)(nil),
			typeKey:   reflect.TypeFor[*protocol.SemanticTokensResult](),
			normalize: func() any { return NormalizeForDisplay((*protocol.SemanticTokensResult)(nil)) },
		},
	}

	dispatch := newDisplayNormalizerDispatch()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if legacyKey := reflect.TypeOf(test.value); legacyKey != test.typeKey {
				t.Fatalf("legacy key = %v, TypeFor key = %v", legacyKey, test.typeKey)
			}
			if _, ok := dispatch.normalizers[test.typeKey]; !ok {
				t.Fatalf("normalizer key %v was not registered", test.typeKey)
			}
			if _, ok := dispatch.normalize(test.value); !ok {
				t.Fatal("typed nil did not hit its normalizer")
			}

			normalized := test.normalize()
			if normalizedType := reflect.TypeOf(normalized); normalizedType != test.typeKey {
				t.Fatalf("normalized type = %v, want %v", normalizedType, test.typeKey)
			}
			if !reflect.ValueOf(normalized).IsNil() {
				t.Fatalf("normalized typed nil = %#v, want nil", normalized)
			}
		})
	}
}

func TestNormalizeForDisplayConcurrent(t *testing.T) {
	const workers = 16
	const iterations = 200
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for j := range iterations {
				location := protocol.Location{Range: protocol.Range{Start: protocol.Position{Line: j}}}
				_ = NormalizeForDisplay(location)
				_ = NormalizeForDisplay([]protocol.FoldingRange{{StartLine: j}})
			}
		})
	}
	wg.Wait()
}
