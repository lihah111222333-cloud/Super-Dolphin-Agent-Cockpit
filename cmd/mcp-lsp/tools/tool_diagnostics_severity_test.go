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
	registry := &diagnosticsTestRegistry{diagnosticsByURI: map[string][]protocol.Diagnostic{
		targetURI: missingImportsDiagnostics(),
	}}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: dir, Registry: registry})
	got, err := handler(testToolContext(dir), marshalDiagnosticsInput(t, fileToolInput{FilePath: "script.py"}))
	if err != nil {
		t.Fatalf("diagnostics handler returned error: %v", err)
	}
	resp, ok := got.(diagnosticsResponse)
	if !ok {
		t.Fatalf("result type = %T, want diagnosticsResponse", got)
	}
	if resp.Total != 3 || len(resp.Data) != 1 || len(resp.Data[0].Rows) != 3 {
		t.Fatalf("response = %#v, want one table with three diagnostics", resp)
	}
	if resp.Data[0].Rows[0][5] != "reportMissingImports" || resp.Data[0].Rows[1][5] != "reportMissingModuleSource" || resp.Data[0].Rows[2][5] != "reportGeneralTypeIssues" {
		t.Fatalf("default diagnostics rows = %#v, want all three diagnostic codes", resp.Data[0].Rows)
	}
}

func missingImportsDiagnostics() []protocol.Diagnostic {
	return []protocol.Diagnostic{
		{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 12}}, Severity: protocol.SeverityError, Source: "Pyright", Code: "reportMissingImports", Message: "Import \"numpy\" could not be resolved (reportMissingImports)"},
		{Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 1, Character: 13}}, Severity: protocol.SeverityError, Source: "Pyright", Code: "reportMissingModuleSource", Message: "Import \"pandas\" could not be resolved from source"},
		{Range: protocol.Range{Start: protocol.Position{Line: 9, Character: 4}, End: protocol.Position{Line: 9, Character: 10}}, Severity: protocol.SeverityError, Source: "Pyright", Code: "reportGeneralTypeIssues", Message: "Operator \"+\" not supported for types \"int\" and \"str\""},
	}
}

func TestDiagnosticsPreservesMissingImportsOnChangedLines(t *testing.T) {
	dir := t.TempDir()
	filePath := writeDiagnosticsFixture(t, dir, "script.py")
	targetURI := canonicalFileURI(t, filePath)
	registry := &diagnosticsTestRegistry{diagnosticsByURI: map[string][]protocol.Diagnostic{targetURI: missingImportsDiagnostics()}}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: dir, Registry: registry})
	got, err := handler(testToolContext(dir), marshalDiagnosticsInput(t, fileToolInput{FilePath: "script.py", ChangedLines: []int{1}}))
	if err != nil {
		t.Fatalf("diagnostics handler returned error: %v", err)
	}
	resp := got.(diagnosticsResponse)
	if resp.Total != 3 {
		t.Fatalf("resp.Total = %d, want 3 (changed_lines must not hide Error diagnostics)", resp.Total)
	}
	rows := resp.Data[0].Rows
	if rows[0][0] != 1 || rows[0][5] != "reportMissingImports" {
		t.Fatalf("first row = %#v, want line 1 reportMissingImports", rows[0])
	}
	if rows[1][0] != 2 || rows[1][5] != "reportMissingModuleSource" || rows[2][0] != 10 || rows[2][5] != "reportGeneralTypeIssues" {
		t.Fatalf("changed_lines rows = %#v, want both missing imports and type error", rows)
	}
}

func TestDiagnosticsAllMissingImportsFilteredReturnsCleanZeroResult(t *testing.T) {
	dir := t.TempDir()
	filePath := writeDiagnosticsFixture(t, dir, "script.py")
	targetURI := canonicalFileURI(t, filePath)
	registry := &diagnosticsTestRegistry{diagnosticsByURI: map[string][]protocol.Diagnostic{targetURI: {missingImportsDiagnostics()[0]}}}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: dir, Registry: registry})
	got, err := handler(testToolContext(dir), marshalDiagnosticsInput(t, fileToolInput{FilePath: "script.py"}))
	if err != nil {
		t.Fatalf("diagnostics handler returned error: %v", err)
	}
	resp := got.(diagnosticsResponse)
	if resp.Total != 1 || len(resp.Data) != 1 || len(resp.Data[0].Rows) != 1 {
		t.Fatalf("resp = %#v, want missing import retained by default", resp)
	}
	plainText := resp.ToPlainText()
	if !strings.Contains(plainText, "OK total=1 showing=1 truncated=0 unit=diagnostic") || !strings.Contains(plainText, "code=reportMissingImports") {
		t.Fatalf("plainText = %q, want retained missing import row", plainText)
	}
}
