package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestTrajectoryCollectorDoesNotCallObservationWriter(t *testing.T) {
	t.Parallel()
	file := filepath.Join("..", "module", "turn", "trajectory_collector.go")
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	forbidden := map[string]struct{}{
		"AttributeCall":             {},
		"Dedupe":                    {},
		"WriteSnapshot":             {},
		"MapTurn":                   {},
		"RecordTokens":              {},
		"RecordTerminal":            {},
		"SetSkillsSelected":         {},
		"IncrementToolCalls":        {},
		"IncrementToolFailures":     {},
		"IncrementApprovalRequests": {},
		"RecordStartedAt":           {},
		"RecordCompletedAt":         {},
	}
	ast.Inspect(parsed, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, bad := forbidden[sel.Sel.Name]; bad {
			pos := fset.Position(sel.Pos())
			t.Fatalf("trajectory_collector.go calls observation writer method %s at %s", sel.Sel.Name, pos)
		}
		return true
	})
}
