package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestDiagnosticsResponseUsesTopLevelMetaFields(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "broken.go")
	targetURI := canonicalFileURI(t, target)
	duplicate := protocol.Diagnostic{
		Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 4}, End: protocol.Position{Line: 2, Character: 8}},
		Severity: protocol.SeverityError, Source: "gopls", Message: "undefined: missing",
	}
	registry := &diagnosticsTestRegistry{diagnosticsByURI: map[string][]protocol.Diagnostic{
		targetURI: {duplicate, duplicate, {
			Range: protocol.Range{Start: protocol.Position{Line: 3, Character: 1}, End: protocol.Position{Line: 3, Character: 5}},
			Severity: protocol.SeverityWarning, Source: "gopls", Message: "unused value",
		}},
	}}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	result, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "broken.go"}))
	if err != nil {
		t.Fatalf("diagnostics returned error: %v", err)
	}
	assertDiagnosticsTopLevelMeta(t, result)
}

func assertDiagnosticsTopLevelMeta(t *testing.T, result any) {
	t.Helper()
	payload := mustMarshalObject(t, result)
	if payload["total"] != float64(2) || payload["showing"] != float64(2) {
		t.Fatalf("diagnostics total/showing = %#v/%#v, want 2/2", payload["total"], payload["showing"])
	}
	if hint, _ := payload["hint"].(string); !strings.Contains(hint, "native file tools") || !strings.Contains(hint, "apply_patch") {
		t.Fatalf("diagnostics hint = %q, want repair guidance", hint)
	}
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostics meta = %#v, want object", payload["meta"])
	}
	if _, ok := meta["count"]; ok {
		t.Fatalf("diagnostics meta contains legacy count: %#v", meta)
	}
	text := result.(diagnosticsResponse).ToPlainText()
	if !strings.HasPrefix(text, "OK total=2 showing=2 truncated=0 unit=diagnostic\n") || strings.Count(text, "\nROW\t") != 2 {
		t.Fatalf("diagnostics text protocol = %q, want exact header and two ROW records", text)
	}
}

func TestDiagnosticsZeroResultHintReportsNoDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsFixture(t, root, "clean.go")
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: &diagnosticsTestRegistry{}})
	result, err := handler(testToolContext(root), marshalDiagnosticsInput(t, fileToolInput{FilePath: "clean.go"}))
	if err != nil {
		t.Fatalf("diagnostics returned error: %v", err)
	}
	response, ok := result.(diagnosticsResponse)
	if !ok {
		t.Fatalf("result type = %T, want diagnosticsResponse", result)
	}
	assertZeroDiagnosticsHint(t, response)
}

func assertZeroDiagnosticsHint(t *testing.T, response diagnosticsResponse) {
	t.Helper()
	if !response.Success || response.Total != 0 || response.Showing != 0 {
		t.Fatalf("response = %#v, want successful zero-result response", response)
	}
	for _, forbidden := range []string{"inspect reported locations", "apply_patch", "rerun"} {
		if strings.Contains(response.Hint, forbidden) {
			t.Fatalf("zero-result hint = %q, must not contain repair action %q", response.Hint, forbidden)
		}
	}
	for _, want := range []string{"no diagnostics reported", "continue focused tests"} {
		if !strings.Contains(response.Hint, want) {
			t.Fatalf("zero-result hint = %q, want %q", response.Hint, want)
		}
	}
	plainText := response.ToPlainText()
	if !strings.HasPrefix(plainText, "OK total=0 showing=0 truncated=0 unit=diagnostic\n") {
		t.Fatalf("zero-result plain text = %q, want successful protocol header", plainText)
	}
}

func TestDiagnosticsResultCacheRequiresStableArgsDigestAndGeneration(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "cached.go")
	uri := canonicalFileURI(t, target)
	registry := &diagnosticsTestRegistry{diagnosticsByURI: map[string][]protocol.Diagnostic{uri: {{Message: "cached diagnostic"}}}}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
	request := marshalDiagnosticsInput(t, fileToolInput{FilePath: "cached.go"})
	callDiagnosticsAndRequireSuccess(t, handler, ctx, request, "first diagnostics call")
	callDiagnosticsAndRequireSuccess(t, handler, ctx, request, "cached diagnostics call")
	if registry.diagnosticsCalls != 1 {
		t.Fatalf("registry diagnostics calls after cache hit = %d, want 1", registry.diagnosticsCalls)
	}
	if err := os.WriteFile(target, []byte("package cached\n// changed\n"), 0o644); err != nil {
		t.Fatalf("change diagnostics fixture: %v", err)
	}
	callDiagnosticsAndRequireSuccess(t, handler, ctx, request, "changed-file diagnostics call")
	if registry.diagnosticsCalls != 2 || registry.waitCalls < 2 {
		t.Fatalf("calls after file change = diagnostics:%d wait:%d, want cache invalidation", registry.diagnosticsCalls, registry.waitCalls)
	}
	registry.generation = 2
	callDiagnosticsAndRequireSuccess(t, handler, ctx, request, "new-generation diagnostics call")
	if registry.waitCalls < 3 {
		t.Fatalf("WaitDiagnosticsStable calls after generation change = %d, want cache invalidation", registry.waitCalls)
	}
}

func callDiagnosticsAndRequireSuccess(t *testing.T, handler Handler, ctx context.Context, request []byte, label string) {
	t.Helper()
	if _, err := handler(ctx, request); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
}

func TestDiagnosticsNonZeroHintRequiresChangedLineTriage(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "broken.go")
	registry := &diagnosticsTestRegistry{diagnosticsByURI: map[string][]protocol.Diagnostic{
		canonicalFileURI(t, target): {{
			Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}}, Severity: protocol.SeverityError,
			Source: "gopls", Code: "compiler-error", Message: "undefined: missing",
		}},
	}}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	result, err := handler(testToolContext(root), marshalDiagnosticsInput(t, fileToolInput{FilePath: "broken.go"}))
	if err != nil {
		t.Fatalf("diagnostics returned error: %v", err)
	}
	response := result.(diagnosticsResponse)
	if response.Total != 1 {
		t.Fatalf("response.Total = %d, want one diagnostic", response.Total)
	}
	for _, want := range []string{"process changed-line findings", "pre-existing or environment errors", "rerun focused tests", "diagnostics are not clean"} {
		if !strings.Contains(response.Hint, want) {
			t.Fatalf("non-zero hint = %q, want %q", response.Hint, want)
		}
	}
}

func TestDiagnosticsRetainsInformationSeverity(t *testing.T) {
	tables, _, err := buildDiagnosticsTables([]protocol.PublishDiagnosticsParams{{
		URI: "file:///workspace/info.go",
		Diagnostics: []protocol.Diagnostic{{
			Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 1}}, Severity: protocol.SeverityInformation,
			Source: "gopls", Code: "info-code", Message: "informational note",
		}},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("build diagnostics tables: %v", err)
	}
	if len(tables) != 1 || len(tables[0].Rows) != 1 || tables[0].Rows[0][2] != "information" {
		t.Fatalf("diagnostics tables = %#v, want one Information row", tables)
	}
}

func TestDiagnosticsRendersTypeScriptDeprecatedSuggestionsAsHint(t *testing.T) {
	tables, _, err := buildDiagnosticsTables([]protocol.PublishDiagnosticsParams{{
		URI: "file:///workspace/form.tsx",
		Diagnostics: []protocol.Diagnostic{{
			Range: protocol.Range{Start: protocol.Position{Line: 9, Character: 17}, End: protocol.Position{Line: 9, Character: 26}},
			Severity: protocol.SeverityWarning, Source: "typescript", Code: float64(6385), Message: "'FormEvent' is deprecated.",
		}},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("build diagnostics tables: %v", err)
	}
	if len(tables) != 1 || len(tables[0].Rows) != 1 || tables[0].Rows[0][2] != "hint" {
		t.Fatalf("diagnostics tables = %#v, want one hint row", tables)
	}
}

