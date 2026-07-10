package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestOrchestrationServiceDoesNotExposeRegistryPassThroughWrappers(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := orchestrationInternalBoundaryGoFiles(t, root, "cmd/mcp-orch/orchestration")
	bannedNames := map[string]struct{}{
		"listAgents":                           {},
		"stoppedHookThreadSuppressed":          {},
		"suppressStoppedHookThreadLocked":      {},
		"suppressStoppedHookThreadUntilLocked": {},
		"withAgentLocked":                      {},
		"withAgentReadLocked":                  {},
		"withAgentReadLockedByAgentID":         {},
	}

	var violations []string
	for _, relPath := range files {
		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}
		fset, file := orchestrationInternalBoundaryParseFile(t, root, relPath)
		violations = append(violations, orchestrationInternalBoundaryServiceRegistryPassThroughViolations(fset, file, relPath, bannedNames)...)
	}
	failIfViolations(t, violations)
}

func TestOrchestrationServiceRegistryPassThroughGuardCatchesVoidCalls(t *testing.T) {
	t.Parallel()

	const relPath = "cmd/mcp-orch/orchestration/factory.go"
	const source = `package orchestration

type service struct {
	registry *agentRegistry
}

type agentRegistry struct{}

func (s *service) suppressStoppedHookThreadLocked(threadID string) {
	s.registry.suppressStoppedHookThreadLocked(threadID)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse synthetic orchestration source: %v", err)
	}
	bannedNames := map[string]struct{}{
		"suppressStoppedHookThreadLocked": {},
	}
	violations := orchestrationInternalBoundaryServiceRegistryPassThroughViolations(fset, file, relPath, bannedNames)
	if len(violations) == 0 {
		t.Fatal("expected void s.registry pass-through wrapper violation")
	}
}

func orchestrationInternalBoundaryServiceRegistryPassThroughViolations(
	fset *token.FileSet,
	file *ast.File,
	relPath string,
	bannedNames map[string]struct{},
) []string {
	var violations []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		recvName, ok := orchestrationInternalBoundaryServiceReceiverName(fn)
		if !ok {
			continue
		}
		line := fset.Position(fn.Pos()).Line
		if _, banned := bannedNames[fn.Name.Name]; banned {
			violations = append(violations, fmt.Sprintf("%s:%d service.%s must not reintroduce agentRegistry pass-through wrappers; use agentRegistry or a lifecycle/turn/report owner directly", relPath, line, fn.Name.Name))
			continue
		}
		if orchestrationInternalBoundaryPureRegistryPassThrough(fn, recvName, bannedNames) {
			violations = append(violations, fmt.Sprintf("%s:%d service.%s is a pure s.registry pass-through; move the call to the real owner or add domain logic behind a narrow port", relPath, line, fn.Name.Name))
		}
	}
	return violations
}

func orchestrationInternalBoundaryServiceReceiverName(fn *ast.FuncDecl) (string, bool) {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return "", false
	}
	recv := fn.Recv.List[0]
	typeName := exprTypeString(recv.Type)
	if typeName != "*service" && typeName != "service" {
		return "", false
	}
	if len(recv.Names) != 1 {
		return "", false
	}
	return recv.Names[0].Name, true
}

func orchestrationInternalBoundaryPureRegistryPassThrough(fn *ast.FuncDecl, recvName string, bannedRegistryMethods map[string]struct{}) bool {
	if fn.Body == nil || len(fn.Body.List) != 1 {
		return false
	}
	var call *ast.CallExpr
	switch stmt := fn.Body.List[0].(type) {
	case *ast.ReturnStmt:
		if len(stmt.Results) != 1 {
			return false
		}
		var ok bool
		call, ok = stmt.Results[0].(*ast.CallExpr)
		if !ok {
			return false
		}
	case *ast.ExprStmt:
		var ok bool
		call, ok = stmt.X.(*ast.CallExpr)
		if !ok {
			return false
		}
	default:
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if _, banned := bannedRegistryMethods[sel.Sel.Name]; !banned {
		return false
	}
	return orchestrationInternalBoundarySelectorChain(sel.X, recvName, "registry")
}
