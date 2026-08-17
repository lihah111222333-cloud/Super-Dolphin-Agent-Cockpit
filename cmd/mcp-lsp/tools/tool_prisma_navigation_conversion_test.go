package tools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestPrismaToolNavigationConversionKeepsNonEmptyHoverAndLocations(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "schema.prisma")
	fileURI := "file:///workspace/schema.prisma"
	manager := &prismaToolNavigationManager{
		hover: &protocol.HoverResult{Contents: protocol.MarkupContent{Kind: "markdown", Value: "model User"}},
		references: []protocol.LocationResult{{Location: &protocol.Location{
			URI:   fileURI,
			Range: protocol.Range{Start: protocol.Position{Line: 13, Character: 12}, End: protocol.Position{Line: 13, Character: 16}},
		}}},
	}

	hover, err := runHover(context.Background(), manager, filePath, protocol.Position{Line: 13, Character: 12})
	if err != nil {
		t.Fatalf("runHover() error = %v", err)
	}
	converted, ok := hover.(hoverLineResult)
	if !ok || converted.text != "model User" || converted.format != "markdown" {
		t.Fatalf("runHover() = %#v, want converted non-empty markup text", hover)
	}

	references, err := runReferences(context.Background(), manager, filePath, protocol.Position{Line: 13, Character: 12}, xrefParams{}, nil)
	if err != nil {
		t.Fatalf("runReferences() error = %v", err)
	}
	grouped, ok := references.(protocol.GroupedLocationResult)
	if !ok {
		t.Fatalf("runReferences() = %T, want protocol.GroupedLocationResult", references)
	}
	if grouped.Total != 1 || grouped.Showing != 1 || len(grouped.Data) != 1 {
		t.Fatalf("runReferences() = %#v, want one converted location", grouped)
	}
}

type prismaToolNavigationManager struct {
	structureTestManager
	hover      *protocol.HoverResult
	references []protocol.LocationResult
}

func (m *prismaToolNavigationManager) Hover(context.Context, string, protocol.Position) (*protocol.HoverResult, error) {
	return m.hover, nil
}

func (m *prismaToolNavigationManager) References(context.Context, string, protocol.Position, bool) ([]protocol.LocationResult, error) {
	return m.references, nil
}
