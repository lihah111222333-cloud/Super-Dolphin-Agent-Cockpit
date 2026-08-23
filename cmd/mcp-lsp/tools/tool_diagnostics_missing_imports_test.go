package tools

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestDiagnosticsFiltersMissingImportsNoiseByDefault(t *testing.T) {
	dir := t.TempDir()
	filePath := writeDiagnosticsFixture(t, dir, "script.py")
	targetURI := canonicalFileURI(t, filePath)

	registry := &diagnosticsTestRegistry{
		diagnosticsByURI: map[string][]protocol.Diagnostic{
			targetURI: {
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 0, Character: 12},
					},
					Severity: protocol.SeverityError,
					Source:   "Pyright",
					Code:     "reportMissingImports",
					Message:  "Import \"numpy\" could not be resolved (reportMissingImports)",
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 0},
						End:   protocol.Position{Line: 1, Character: 13},
					},
					Severity: protocol.SeverityError,
					Source:   "Pyright",
					Code:     "reportMissingModuleSource",
					Message:  "Import \"pandas\" could not be resolved from source",
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 9, Character: 4},
						End:   protocol.Position{Line: 9, Character: 10},
					},
					Severity: protocol.SeverityError,
					Source:   "Pyright",
					Code:     "reportGeneralTypeIssues",
					Message:  "Operator \"+\" not supported for types \"int\" and \"str\"",
				},
			},
		},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: dir, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{FilePath: "script.py"})

	got, err := handler(testToolContext(dir), req)
	if err != nil {
		t.Fatalf("diagnostics handler returned error: %v", err)
	}
	resp, ok := got.(diagnosticsResponse)
	if !ok {
		t.Fatalf("result type = %T, want diagnosticsResponse", got)
	}
	if resp.Total != 1 {
		t.Fatalf("resp.Total = %d, want 1 (missing imports filtered out)", resp.Total)
	}
	if len(resp.Data) != 1 || len(resp.Data[0].Rows) != 1 {
		t.Fatalf("resp.Data = %#v, want 1 table with 1 row", resp.Data)
	}
	row := resp.Data[0].Rows[0]
	// Row should be line 10 reportGeneralTypeIssues
	if row[0] != 10 || row[5] != "reportGeneralTypeIssues" {
		t.Fatalf("preserved row = %#v, want line 10 reportGeneralTypeIssues", row)
	}
}

func TestDiagnosticsPreservesMissingImportsOnChangedLines(t *testing.T) {
	dir := t.TempDir()
	filePath := writeDiagnosticsFixture(t, dir, "script.py")
	targetURI := canonicalFileURI(t, filePath)

	registry := &diagnosticsTestRegistry{
		diagnosticsByURI: map[string][]protocol.Diagnostic{
			targetURI: {
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 0, Character: 12},
					},
					Severity: protocol.SeverityError,
					Source:   "Pyright",
					Code:     "reportMissingImports",
					Message:  "Import \"numpy\" could not be resolved (reportMissingImports)",
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 0},
						End:   protocol.Position{Line: 1, Character: 13},
					},
					Severity: protocol.SeverityError,
					Source:   "Pyright",
					Code:     "reportMissingModuleSource",
					Message:  "Import \"pandas\" could not be resolved from source",
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 9, Character: 4},
						End:   protocol.Position{Line: 9, Character: 10},
					},
					Severity: protocol.SeverityError,
					Source:   "Pyright",
					Code:     "reportGeneralTypeIssues",
					Message:  "Operator \"+\" not supported for types \"int\" and \"str\"",
				},
			},
		},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: dir, Registry: registry})
	// Line 1 is modified: changed_lines=[1]
	req := marshalDiagnosticsInput(t, fileToolInput{
		FilePath:     "script.py",
		ChangedLines: []int{1},
	})

	got, err := handler(testToolContext(dir), req)
	if err != nil {
		t.Fatalf("diagnostics handler returned error: %v", err)
	}
	resp := got.(diagnosticsResponse)
	if resp.Total != 2 {
		t.Fatalf("resp.Total = %d, want 2 (line 1 missing import preserved + line 10 type error)", resp.Total)
	}
	rows := resp.Data[0].Rows
	if rows[0][0] != 1 || rows[0][5] != "reportMissingImports" {
		t.Fatalf("first row = %#v, want line 1 reportMissingImports", rows[0])
	}
	if rows[1][0] != 10 || rows[1][5] != "reportGeneralTypeIssues" {
		t.Fatalf("second row = %#v, want line 10 reportGeneralTypeIssues", rows[1])
	}
}

func TestDiagnosticsAllMissingImportsFilteredReturnsCleanZeroResult(t *testing.T) {
	dir := t.TempDir()
	filePath := writeDiagnosticsFixture(t, dir, "script.py")
	targetURI := canonicalFileURI(t, filePath)

	registry := &diagnosticsTestRegistry{
		diagnosticsByURI: map[string][]protocol.Diagnostic{
			targetURI: {
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 0, Character: 12},
					},
					Severity: protocol.SeverityError,
					Source:   "Pyright",
					Code:     "reportMissingImports",
					Message:  "Import \"numpy\" could not be resolved (reportMissingImports)",
				},
			},
		},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: dir, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{FilePath: "script.py"})

	got, err := handler(testToolContext(dir), req)
	if err != nil {
		t.Fatalf("diagnostics handler returned error: %v", err)
	}
	resp := got.(diagnosticsResponse)
	if resp.Total != 0 || len(resp.Data) != 0 {
		t.Fatalf("resp = %#v, want 0 total and empty data", resp)
	}
	plainText := resp.ToPlainText()
	if !strings.Contains(plainText, "OK total=0 showing=0 truncated=0 unit=diagnostic") {
		t.Fatalf("plainText = %q, want 'OK total=0' header", plainText)
	}
}
