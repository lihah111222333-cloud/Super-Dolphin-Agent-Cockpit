package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

func TestInspectImplementationUnsupportedCapabilityReturnsExplainableEmptyResult(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "sample.go", "package sample\n\ntype fileAuditLogger struct{}\n")
	manager := &lspReproManager{implementationErr: lspmanager.ErrUnsupportedCapability}
	handler := NewInspectHandler(&structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, inspectParams{
		Action: "implementation",
		filePositionParams: filePositionParams{
			Pos: target + ":3:6",
		},
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("implementation returned error = %v, want explainable empty result", err)
	}
	envelope := requireEmptyListEnvelope(t, got)
	if !strings.Contains(envelope.Meta.Message, "implementation") || !strings.Contains(envelope.Meta.Message, "unsupported") {
		t.Fatalf("empty result message = %q, want implementation unsupported explanation", envelope.Meta.Message)
	}
}

func TestXRefTypeHierarchyUnsupportedCapabilityReturnsExplainableEmptyResult(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "sample.go", "package sample\n\ntype fileAuditLogger struct{}\n")
	manager := &lspReproManager{typeHierarchyErr: lspmanager.ErrUnsupportedCapability}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, xrefParams{
		Action:    "type_hierarchy",
		Pos:       target + ":3:6",
		Direction: "supertypes",
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("type_hierarchy returned error = %v, want explainable empty result", err)
	}
	envelope := requireEmptyListEnvelope(t, got)
	if !strings.Contains(envelope.Meta.Message, "type hierarchy") || !strings.Contains(envelope.Meta.Message, "unsupported") {
		t.Fatalf("empty result message = %q, want type hierarchy unsupported explanation", envelope.Meta.Message)
	}
}

func TestXRefMarkdownCallHierarchyReportsLimitedSupport(t *testing.T) {
	root := t.TempDir()
	writeReproFile(t, root, "README.md", "# Intro\n\nBody\n")
	handler := NewXRefHandler(newMarkdownFallbackRegistry(t, root))
	input := marshalReproParams(t, xrefParams{
		Action: "call_hierarchy",
		Pos:    "README.md:1:3",
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("markdown call_hierarchy returned error = %v, want explainable empty result", err)
	}
	envelope := requireEmptyListEnvelope(t, got)
	requireLimitedMarkdownSupportMessage(t, envelope.Meta.Message, "call hierarchy")
}

func TestEditFormatAppliesLSPTextEditsAndSyncsDocument(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "main.go", "package main\n\nfunc main(){\nprintln(\"hi\")\n}\n")
	want := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	manager := &lspReproManager{
		formatEdits: []protocol.TextEdit{{
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 0},
				End:   protocol.Position{Line: 4, Character: 1},
			},
			NewText: "func main() {\n\tprintln(\"hi\")\n}",
		}},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, EditRequest{
		Action:   "format",
		FilePath: "main.go",
		Version:  11,
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("format returned error = %v, want applied format edits", err)
	}
	result := requireEditEnvelope(t, got)
	assertAppliedFormatResult(t, result)
	if manager.gotFormatPath != canonicalReproPath(t, target) {
		t.Fatalf("Format path = %q, want %q", manager.gotFormatPath, canonicalReproPath(t, target))
	}
	assertSingleDidChange(t, manager.didChanges, 11, want)
	assertFileContent(t, target, want)
}

func TestEditFormatNoEditsReturnsNoChange(t *testing.T) {
	root := t.TempDir()
	original := "package main\n"
	target := writeReproFile(t, root, "main.go", original)
	manager := &lspReproManager{}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, EditRequest{
		Action:   "format",
		FilePath: "main.go",
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("format returned error = %v, want no-change success", err)
	}
	result := requireEditEnvelope(t, got)
	if result.Status != "no_change" || result.Persisted {
		t.Fatalf("format result = %#v, want no_change without persistence", result)
	}
	if len(manager.didChanges) != 0 {
		t.Fatalf("DidChange calls = %d, want none for no-op format", len(manager.didChanges))
	}
	assertFileContent(t, target, original)
}

func TestEditFormatSyncFailureRollsBackAndReturnsError(t *testing.T) {
	root := t.TempDir()
	original := "package main\n\nfunc main(){\nprintln(\"hi\")\n}\n"
	target := writeReproFile(t, root, "main.go", original)
	manager := &lspReproManager{
		formatEdits: []protocol.TextEdit{{
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 0},
				End:   protocol.Position{Line: 4, Character: 1},
			},
			NewText: "func main() {\n\tprintln(\"hi\")\n}",
		}},
		didChangeErr: errors.New("lsp sync failed"),
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, EditRequest{
		Action:   "format",
		FilePath: "main.go",
		Version:  12,
	})

	got, err := handler(testToolContext(root), input)
	if err == nil || !strings.Contains(err.Error(), "lsp sync failed") {
		t.Fatalf("format error = %v, result=%#v, want sync failure", err, got)
	}
	assertFileContent(t, target, original)
}

func TestApplyTextEditsUsesUTF16CharacterOffsets(t *testing.T) {
	got, err := applyTextEdits("🙂x\n", []protocol.TextEdit{{
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 2},
			End:   protocol.Position{Line: 0, Character: 2},
		},
		NewText: "!",
	}})
	if err != nil {
		t.Fatalf("applyTextEdits returned error = %v, want UTF-16 offset edit", err)
	}
	if want := "🙂!x\n"; got != want {
		t.Fatalf("applyTextEdits = %q, want %q", got, want)
	}
}

func TestApplyTextEditsRejectsInvalidRanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit protocol.TextEdit
	}{
		{
			name: "negative line",
			edit: protocol.TextEdit{Range: protocol.Range{
				Start: protocol.Position{Line: -1, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			}},
		},
		{
			name: "negative character",
			edit: protocol.TextEdit{Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: -1},
				End:   protocol.Position{Line: 0, Character: 0},
			}},
		},
		{
			name: "reversed range",
			edit: protocol.TextEdit{Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 2},
				End:   protocol.Position{Line: 0, Character: 1},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireTextEditRangeError(t, []protocol.TextEdit{tc.edit})
		})
	}
}

func TestEditCodeActionNoQuickFixesReturnsEmptyList(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	manager := &lspReproManager{}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, EditRequest{
		Action: "code_action",
		Pos:    "main.go:3:1",
		Only:   []string{"quickfix"},
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("code_action returned error = %v, want empty result when no quickfixes exist", err)
	}
	assertEmptyCodeActionReceipt(t, requireEditEnvelope(t, got))
	if manager.gotCodeActionPath != canonicalReproPath(t, target) {
		t.Fatalf("CodeAction path = %q, want %q", manager.gotCodeActionPath, canonicalReproPath(t, target))
	}
	if manager.gotCodeActionRange.Start.Line != 2 || manager.gotCodeActionRange.Start.Character != 0 {
		t.Fatalf("CodeAction range start = %#v, want line 2 char 0", manager.gotCodeActionRange.Start)
	}
	if len(manager.gotCodeActionOnly) != 1 || manager.gotCodeActionOnly[0] != "quickfix" {
		t.Fatalf("CodeAction only = %#v, want [quickfix]", manager.gotCodeActionOnly)
	}
}

func assertEmptyCodeActionReceipt(t *testing.T, envelope editEnvelope) {
	t.Helper()
	doc, err := lineprotocol.Parse(envelope.ToPlainText())
	if err != nil {
		t.Fatalf("parse empty code-action receipt: %v", err)
	}
	if doc.Header != (lineprotocol.Header{Total: 0, Showing: 0, Unit: "edit"}) {
		t.Errorf("empty code-action header = %+v", doc.Header)
	}
	message, hint := "", ""
	for _, record := range doc.Records {
		switch record.Kind {
		case "MESSAGE":
			message = record.Value
		case "HINT":
			hint = record.Value
		}
	}
	if !strings.Contains(message, "no code actions found") {
		t.Errorf("empty code-action message = %q", message)
	}
	for _, required := range []string{"retry patch_edit action=code_action", "without only"} {
		if !strings.Contains(hint, required) {
			t.Errorf("empty code-action hint = %q, want %q", hint, required)
		}
	}
}

func TestEditCodeActionAppliesSingleWorkspaceEdit(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "main.go", "package main\n\nfunc main() {\n\tmissing\n}\n")
	want := "package main\n\nfunc main() {\n\tmissing()\n}\n"
	manager := &lspReproManager{
		codeActions: []protocol.CodeActionResult{{
			CodeAction: &protocol.CodeAction{
				Title: "Insert call",
				Kind:  "quickfix",
				Edit: &protocol.WorkspaceEdit{
					Changes: map[string][]protocol.TextEdit{
						fileURI(target): {{
							Range: protocol.Range{
								Start: protocol.Position{Line: 3, Character: 8},
								End:   protocol.Position{Line: 3, Character: 8},
							},
							NewText: "()",
						}},
					},
				},
			},
		}},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, EditRequest{
		Action: "code_action",
		Pos:    "main.go:4:9",
		Only:   []string{"quickfix"},
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("code_action returned error = %v, want applied quickfix", err)
	}
	result := requireCodeActionEnvelope(t, got)
	if result.Status != "applied" || !result.Persisted {
		t.Fatalf("code_action result = %#v, want applied persisted quickfix", result)
	}
	assertSingleDidChange(t, manager.didChanges, defaultEditVersion, want)
	assertFileContent(t, target, want)
}

type lspReproManager struct {
	structureTestManager

	implementationErr error
	typeHierarchyErr  error
	formatEdits       []protocol.TextEdit
	codeActions       []protocol.CodeActionResult

	gotFormatPath      string
	gotFormatOptions   protocol.FormattingOptions
	didChangeErr       error
	gotCodeActionPath  string
	gotCodeActionRange protocol.Range
	gotCodeActionOnly  []string
	didChanges         []lspReproDidChange
}

type lspReproDidChange struct {
	path    string
	version int
	text    string
}

func (m *lspReproManager) Implementation(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	if m.implementationErr != nil {
		return nil, m.implementationErr
	}
	return nil, nil
}

func (m *lspReproManager) TypeHierarchy(context.Context, string, protocol.Position, string) ([]protocol.TypeHierarchyResult, error) {
	if m.typeHierarchyErr != nil {
		return nil, m.typeHierarchyErr
	}
	return nil, nil
}

func (m *lspReproManager) Format(_ context.Context, path string, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	m.gotFormatPath = path
	m.gotFormatOptions = options
	return append([]protocol.TextEdit(nil), m.formatEdits...), nil
}

func (m *lspReproManager) CodeAction(_ context.Context, path string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error) {
	m.gotCodeActionPath = path
	m.gotCodeActionRange = rng
	m.gotCodeActionOnly = append([]string(nil), only...)
	return append([]protocol.CodeActionResult(nil), m.codeActions...), nil
}

func (m *lspReproManager) DidChange(_ context.Context, path string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	if len(changes) != 1 {
		return errors.New("DidChange requires one full-document change")
	}
	m.didChanges = append(m.didChanges, lspReproDidChange{
		path:    path,
		version: version,
		text:    changes[0].Text,
	})
	return m.didChangeErr
}

func writeReproFile(t *testing.T, root, relPath, content string) string {
	t.Helper()
	target := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return target
}

func canonicalReproPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize path %s: %v", path, err)
	}
	return resolved
}

func marshalReproParams(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

func requireEmptyListEnvelope(t *testing.T, got any) emptyListEnvelope {
	t.Helper()
	envelope, ok := got.(emptyListEnvelope)
	if !ok {
		t.Fatalf("result type = %T, want emptyListEnvelope", got)
	}
	if !envelope.Success || envelope.Meta.Count != 0 || len(envelope.Data) != 0 {
		t.Fatalf("empty envelope = %#v, want success empty result", envelope)
	}
	return envelope
}

func requireEditEnvelope(t *testing.T, got any) editEnvelope {
	t.Helper()
	result, ok := got.(editEnvelope)
	if !ok {
		t.Fatalf("result type = %T, want editEnvelope", got)
	}
	return result
}

func requireCodeActionEnvelope(t *testing.T, got any) editEnvelope {
	t.Helper()
	result, ok := got.(codeActionResult)
	if !ok {
		t.Fatalf("result type = %T, want codeActionResult", got)
	}
	return result.editEnvelope
}

func requireTextEditRangeError(t *testing.T, edits []protocol.TextEdit) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("applyTextEdits panicked for invalid range: %v", recovered)
		}
	}()
	if _, err := applyTextEdits("abc\n", edits); err == nil {
		t.Fatalf("applyTextEdits error = nil, want invalid range failure")
	}
}

func assertAppliedFormatResult(t *testing.T, result editEnvelope) {
	t.Helper()
	if result.Status != "applied" {
		t.Fatalf("format status = %q, want applied", result.Status)
	}
	if !result.Persisted || !result.LSPSync {
		t.Fatalf("format result = %#v, want persisted LSP-synced edit", result)
	}
}

func assertSingleDidChange(t *testing.T, changes []lspReproDidChange, version int, text string) {
	t.Helper()
	if len(changes) != 1 {
		t.Fatalf("DidChange calls = %d, want 1", len(changes))
	}
	if changes[0].version != version {
		t.Fatalf("DidChange version = %d, want %d", changes[0].version, version)
	}
	if changes[0].text != text {
		t.Fatalf("DidChange text = %q, want %q", changes[0].text, text)
	}
}
