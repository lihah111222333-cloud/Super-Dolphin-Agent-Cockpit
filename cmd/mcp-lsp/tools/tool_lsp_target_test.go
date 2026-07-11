package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestInspectImplementationWrongTargetReturnsInvalidTargetError(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "auditlog.go", strings.Join([]string{
		"package auditlog",
		"",
		"func NewFileAuditLogger() {}",
		"",
	}, "\n"))
	manager := &targetReproManager{implementationErr: errors.New("NewFileAuditLogger is a function, not a method (query at 'func' token to find matching signatures)")}
	handler := NewInspectHandler(&structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, inspectParams{
		Action:             "implementation",
		filePositionParams: filePositionParams{Pos: target + ":3:6"},
	})

	got, err := handler(testToolContext(root), input)
	if err == nil || !strings.Contains(err.Error(), "function, not a method") {
		t.Fatalf("implementation error = %v, result=%#v, want wrong-target reason", err, got)
	}
	requireInvalidTargetError(t, err, "next: inspect action=implementation")
}

func TestInspectImplementationCorrectInterfaceTargetReturnsImplementations(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "auditlog.go", "package auditlog\n\ntype Logger interface{}\n\ntype FileAuditLogger struct{}\n")
	manager := &targetReproManager{
		implementationResults: []protocol.LocationResult{{
			Location: &protocol.Location{
				URI:   fileURI(target),
				Range: reproRange(4, 5, 20),
			},
		}},
	}
	handler := NewInspectHandler(&structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, inspectParams{
		Action:             "implementation",
		filePositionParams: filePositionParams{Pos: target + ":3:6"},
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("implementation returned error = %v, want implementation locations", err)
	}
	results := requireGroupedLocationResults(t, got)
	locations := results.Data["auditlog.go"]
	if len(locations) != 1 {
		t.Fatalf("implementation grouped locations = %#v, want one location", results.Data)
	}
	if locations[0].Line != 5 || locations[0].Col != 6 {
		t.Fatalf("implementation location = %#v, want line 5 col 6", locations[0])
	}
}

func TestXRefTypeHierarchyWrongTargetReturnsInvalidTargetError(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "file_audit_logger.go", strings.Join([]string{
		"package auditlog",
		"",
		"type FileAuditLogger struct{}",
		"",
		"func NewFileAuditLogger() *FileAuditLogger { return &FileAuditLogger{} }",
		"",
	}, "\n"))
	manager := &targetReproManager{typeHierarchyErr: errors.New("position is not a type name")}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, xrefParams{
		Action:    "type_hierarchy",
		Pos:       target + ":5:31",
		Direction: "supertypes",
	})

	got, err := handler(testToolContext(root), input)
	if err == nil || !strings.Contains(err.Error(), "not a type name") {
		t.Fatalf("type_hierarchy error = %v, result=%#v, want wrong-target reason", err, got)
	}
	requireInvalidTargetError(t, err, "next: xref action=type_hierarchy")
}

func TestXRefTypeHierarchyCorrectTypeTargetsReturnHierarchy(t *testing.T) {
	root := t.TempDir()
	target := writeReproFile(t, root, "auditlog.go", "package auditlog\n\ntype Logger interface{}\n\ntype FileAuditLogger struct{}\n")
	manager := &targetReproManager{
		typeHierarchyResults: []protocol.TypeHierarchyResult{{
			Item:       reproTypeHierarchyItem("FileAuditLogger", target, 4),
			Supertypes: []protocol.TypeHierarchyItem{reproTypeHierarchyItem("Logger", target, 2)},
		}},
	}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, xrefParams{
		Action:    "type_hierarchy",
		Pos:       target + ":5:6",
		Direction: "supertypes",
	})

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("type_hierarchy returned error = %v, want hierarchy", err)
	}
	results := requireTypeHierarchyResults(t, got)
	if len(results) != 1 || results[0].Item.Name != "FileAuditLogger" {
		t.Fatalf("type hierarchy results = %#v, want FileAuditLogger item", results)
	}
	if len(results[0].Supertypes) != 1 || results[0].Supertypes[0].Name != "Logger" {
		t.Fatalf("type hierarchy supertypes = %#v, want Logger", results[0].Supertypes)
	}
}

type targetReproManager struct {
	structureTestManager

	implementationErr     error
	implementationResults []protocol.LocationResult
	typeHierarchyErr      error
	typeHierarchyResults  []protocol.TypeHierarchyResult
}

func (m *targetReproManager) Implementation(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	if m.implementationErr != nil {
		return nil, m.implementationErr
	}
	return append([]protocol.LocationResult(nil), m.implementationResults...), nil
}

func (m *targetReproManager) TypeHierarchy(context.Context, string, protocol.Position, string) ([]protocol.TypeHierarchyResult, error) {
	if m.typeHierarchyErr != nil {
		return nil, m.typeHierarchyErr
	}
	return append([]protocol.TypeHierarchyResult(nil), m.typeHierarchyResults...), nil
}

func requireGroupedLocationResults(t *testing.T, got any) protocol.GroupedLocationResult {
	t.Helper()
	results, ok := got.(protocol.GroupedLocationResult)
	if !ok {
		t.Fatalf("result type = %T, want protocol.GroupedLocationResult", got)
	}
	return results
}

func requireTypeHierarchyResults(t *testing.T, got any) []protocol.TypeHierarchyResult {
	t.Helper()
	results, ok := got.([]protocol.TypeHierarchyResult)
	if !ok {
		t.Fatalf("result type = %T, want []protocol.TypeHierarchyResult", got)
	}
	return results
}

func requireInvalidTargetError(t *testing.T, err error, wantHintPart string) {
	t.Helper()
	var coded *common.CodedToolError
	if !errors.As(err, &coded) {
		t.Fatalf("error type = %T, want *common.CodedToolError", err)
	}
	if coded.Code != "invalid_target" {
		t.Fatalf("coded error code = %q, want invalid_target", coded.Code)
	}
	if coded.Retryable {
		t.Fatalf("coded error retryable = true, want false")
	}
	if !strings.Contains(coded.Hint, wantHintPart) {
		t.Fatalf("coded error hint = %q, want it to contain %q", coded.Hint, wantHintPart)
	}
}

func reproTypeHierarchyItem(name, path string, line int) protocol.TypeHierarchyItem {
	return protocol.TypeHierarchyItem{
		Name:           name,
		Kind:           int(protocol.SymbolKindClass),
		URI:            fileURI(path),
		Range:          reproRange(line, 0, len(name)),
		SelectionRange: reproRange(line, 5, 5+len(name)),
	}
}

func reproRange(line, start, end int) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: line, Character: start},
		End:   protocol.Position{Line: line, Character: end},
	}
}
