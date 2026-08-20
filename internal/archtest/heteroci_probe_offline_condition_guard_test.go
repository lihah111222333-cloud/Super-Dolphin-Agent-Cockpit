package archtest

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// TestHeterociProbeOfflineConditionSoundness 冻结 offline 投影的三项同边界条件。
func TestHeterociProbeOfflineConditionSoundness(t *testing.T) {
	root := findRepoRoot(t)
	guard, err := loadHeterociProbeSSAGuard(root, "./internal/devtools/heteroci", heterociProbePackagePath)
	if err != nil {
		t.Fatal(err)
	}
	if violations := guard.offlineConditionViolations(); len(violations) != 0 {
		t.Fatalf("offline probe condition violations: %s", strings.Join(violations, "; "))
	}
}

func TestHeterociProbeOfflineConditionCounterexamples(t *testing.T) {
	for _, fixture := range heterociProbeOfflineCounterexamples() {
		t.Run(fixture.name, func(t *testing.T) {
			guard, _ := newHeterociProbeSyntheticGuard(t, heterociProbeOfflinePreamble()+fixture.source, "")
			if violations := guard.offlineConditionViolations(); len(violations) == 0 {
				t.Fatalf("offline condition violations = none, want rejection")
			}
		})
	}
}

type heterociProbeOfflineFixture struct {
	name   string
	source string
}

func heterociProbeOfflineCounterexamples() []heterociProbeOfflineFixture {
	fixtures := append(heterociProbeOfflineConditionFixtures(), heterociProbeOfflineSnapshotFixtures()...)
	return append(fixtures, heterociProbeOfflineOverwriteFixtures()...)
}

func heterociProbeOfflineConditionFixtures() []heterociProbeOfflineFixture {
	return append(heterociProbeOfflineBooleanFixtures(), heterociProbeOfflineContextFixtures()...)
}

func heterociProbeOfflineBooleanFixtures() []heterociProbeOfflineFixture {
	return []heterociProbeOfflineFixture{
		{
			name: "raw fatal OR is rejected",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					if bodyNil && ctxErr == nil && (transportErr.onlyUnavailable || err != nil) { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
		{
			name: "extra boolean is rejected",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					extra := true
					if bodyNil && ctxErr == nil && transportErr.onlyUnavailable && extra { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
		{
			name: "non-nil body is rejected",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					_ = bodyNil
					if body != nil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
	}
}

func heterociProbeOfflineContextFixtures() []heterociProbeOfflineFixture {
	return []heterociProbeOfflineFixture{
		{
			name: "non-nil context error is rejected",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					if bodyNil && ctxErr != nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
		{
			name: "context comparison to raw error is rejected",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					if bodyNil && ctxErr == err && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
		{
			name: "duplicate context boundary is rejected",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					otherCtxErr := ctx.Err()
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					_ = otherCtxErr
					if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
	}
}

func heterociProbeOfflineSnapshotFixtures() []heterociProbeOfflineFixture {
	return []heterociProbeOfflineFixture{
		{
			name: "different snapshot value is rejected",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					var otherTransportErr probeExternalErrorSnapshot
					if err != nil {
						transportErr = snapshotProbeExternalError(err)
						otherTransportErr = snapshotProbeExternalError(err)
					}
					_ = transportErr
					if bodyNil && ctxErr == nil && otherTransportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
		{
			name: "snapshot of a different raw error is rejected",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					var otherErr error
					if err != nil { transportErr = snapshotProbeExternalError(otherErr) }
					if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
	}
}

func heterociProbeOfflineOverwriteFixtures() []heterociProbeOfflineFixture {
	return []heterociProbeOfflineFixture{
		{
			name: "bodyNil reaching definition is overwritten",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					bodyNil = true
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
		{
			name: "ctxErr reaching definition is overwritten",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					ctxErr = context.Canceled
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
		{
			name: "whole snapshot reaching definition is overwritten",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					transportErr = probeExternalErrorSnapshot{}
					if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
		{
			name: "snapshot field reaching definition is overwritten",
			source: `
				func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
					body, err := transport.Probe(ctx, "", request)
					bodyNil := isNilProbeValue(body)
					ctxErr := ctx.Err()
					var transportErr probeExternalErrorSnapshot
					if err != nil { transportErr = snapshotProbeExternalError(err) }
					transportErr.onlyUnavailable = false
					if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }
					return NodeSnapshot{}, nil
				}`,
		},
	}
}

func heterociProbeOfflinePreamble() string {
	return `package heteroci

import "context"

type ProbeResponseBody interface{}
type ProbeTransport interface {
	Probe(context.Context, string, []byte) (ProbeResponseBody, error)
}
type NodeConfig struct{}
type NodeSnapshot struct{}
type probeExternalErrorSnapshot struct { onlyUnavailable bool }

func isNilProbeValue(any) bool { return false }
func snapshotProbeExternalError(error) probeExternalErrorSnapshot {
	return probeExternalErrorSnapshot{onlyUnavailable: true}
}
func offlineProbeSnapshot(NodeConfig) NodeSnapshot { return NodeSnapshot{} }
`
}

type heterociProbeOfflineEvidence struct {
	probeCall      *ast.CallExpr
	bodyNilCall    *ast.CallExpr
	ctxErrCall     *ast.CallExpr
	snapshotCall   *ast.CallExpr
	body           types.Object
	rawErr         types.Object
	bodyNil        types.Object
	ctxErr         types.Object
	transportErr   types.Object
	contextParam   types.Object
	transportParam types.Object
}

func (guard *heterociProbeSSAGuard) offlineConditionViolations() []string {
	found := make(map[string]bool)
	function, offlineObject := guard.offlineFunction()
	if function == nil || offlineObject == nil {
		found["acquireProbeNode or offlineProbeSnapshot is missing"] = true
		return sortedHeterociProbeSet(found)
	}
	if calls := guard.offlineCalls(function, offlineObject); len(calls) != 1 {
		found["acquireProbeNode must return exactly one offlineProbeSnapshot"] = true
	}
	branches := guard.offlineBranches(function, offlineObject)
	if len(branches) != 1 {
		found["offlineProbeSnapshot must have exactly one direct return branch"] = true
		return sortedHeterociProbeSet(found)
	}
	evidence, violations := guard.offlineEvidence(function, branches[0])
	for _, violation := range violations {
		found[violation] = true
	}
	conditions := guard.offlineConditionPath(function, branches[0])
	if evidence != nil && !guard.offlineConditionMatches(conditions, evidence) {
		found["offline branch condition is not exact bodyNil && ctxErr == nil && transportErr.onlyUnavailable"] = true
	}
	return sortedHeterociProbeSet(found)
}

func (guard *heterociProbeSSAGuard) offlineFunction() (*ast.FuncDecl, types.Object) {
	functionObject := guard.pkg.Types.Scope().Lookup("acquireProbeNode")
	offlineObject := guard.pkg.Types.Scope().Lookup("offlineProbeSnapshot")
	if functionObject == nil || offlineObject == nil {
		return nil, nil
	}
	for _, file := range guard.pkg.Syntax {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && guard.pkg.TypesInfo.Defs[function.Name] == functionObject {
				return function, offlineObject
			}
		}
	}
	return nil, offlineObject
}

func (guard *heterociProbeSSAGuard) offlineCalls(function *ast.FuncDecl, object types.Object) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && guard.offlineObject(call.Fun) == object {
			calls = append(calls, call)
		}
		return true
	})
	return calls
}

func (guard *heterociProbeSSAGuard) offlineBranches(function *ast.FuncDecl, object types.Object) []*ast.IfStmt {
	var branches []*ast.IfStmt
	ast.Inspect(function.Body, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		for _, statement := range branch.Body.List {
			returned, ok := statement.(*ast.ReturnStmt)
			if ok && guard.offlineReturn(returned, object) {
				branches = append(branches, branch)
				break
			}
		}
		return true
	})
	return branches
}

func (guard *heterociProbeSSAGuard) offlineReturn(returned *ast.ReturnStmt, object types.Object) bool {
	if len(returned.Results) != 2 {
		return false
	}
	call, ok := returned.Results[0].(*ast.CallExpr)
	return ok && guard.offlineObject(call.Fun) == object && len(call.Args) == 1 && heterociProbeNilIdent(returned.Results[1])
}

func (guard *heterociProbeSSAGuard) offlineEvidence(function *ast.FuncDecl, branch *ast.IfStmt) (*heterociProbeOfflineEvidence, []string) {
	contextParam, transportParam := guard.offlineFunctionParams(function)
	evidence := &heterociProbeOfflineEvidence{
		contextParam:   contextParam,
		transportParam: transportParam,
	}
	probes := guard.offlineProbeAssignments(function, evidence.transportParam)
	if len(probes) != 1 {
		return nil, []string{"offline body and raw error must come from one Probe call"}
	}
	evidence.probeCall, evidence.body, evidence.rawErr = probes[0].call, probes[0].body, probes[0].rawErr
	bodyNil := guard.offlineBodyNilAssignments(function, evidence.body)
	if len(bodyNil) != 1 {
		return nil, []string{"bodyNil must be produced by isNilProbeValue of the Probe body"}
	}
	evidence.bodyNilCall, evidence.bodyNil = bodyNil[0].call, bodyNil[0].object
	ctxErr := guard.offlineContextAssignments(function, evidence.contextParam)
	if len(ctxErr) != 1 {
		return nil, []string{"ctxErr must come from the unique Context.Err at this boundary"}
	}
	evidence.ctxErrCall, evidence.ctxErr = ctxErr[0].call, ctxErr[0].object
	snapshots := guard.offlineSnapshotAssignments(function, evidence.rawErr)
	if len(snapshots) != 1 {
		return nil, []string{"transportErr must use the unique snapshot of the Probe raw error"}
	}
	evidence.snapshotCall, evidence.transportErr = snapshots[0].call, snapshots[0].object
	if !heterociProbeNodesBefore(branch, evidence.probeCall, evidence.bodyNilCall, evidence.ctxErrCall, evidence.snapshotCall) {
		return nil, []string{"offline evidence must be captured before its return branch"}
	}
	if !guard.offlineReachingDefinitions(function, evidence) {
		return nil, []string{"offline evidence reaching definitions are overwritten before the branch"}
	}
	return evidence, nil
}

type heterociProbeOfflineAssignment struct {
	call   *ast.CallExpr
	body   types.Object
	rawErr types.Object
	object types.Object
}

func (guard *heterociProbeSSAGuard) offlineProbeAssignments(function *ast.FuncDecl, transport types.Object) []heterociProbeOfflineAssignment {
	probe := guard.pkg.Types.Scope().Lookup("ProbeTransport")
	method := guard.packageInterfaceMethodObject(guard.pkg.Types, "ProbeTransport", "Probe")
	if probe == nil || method == nil || transport == nil {
		return nil
	}
	var assignments []heterociProbeOfflineAssignment
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if candidate, ok := guard.offlineProbeAssignment(assignment, method, transport); ok {
			assignments = append(assignments, candidate)
		}
		return true
	})
	return assignments
}

func (guard *heterociProbeSSAGuard) offlineProbeAssignment(assignment *ast.AssignStmt, method, transport types.Object) (heterociProbeOfflineAssignment, bool) {
	if len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 {
		return heterociProbeOfflineAssignment{}, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok {
		return heterociProbeOfflineAssignment{}, false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || guard.offlineObject(call.Fun) != method || guard.offlineObject(selector.X) != transport {
		return heterociProbeOfflineAssignment{}, false
	}
	left, leftOK := assignment.Lhs[0].(*ast.Ident)
	right, rightOK := assignment.Lhs[1].(*ast.Ident)
	if !leftOK || !rightOK {
		return heterociProbeOfflineAssignment{}, false
	}
	return heterociProbeOfflineAssignment{call: call, body: guard.offlineDefinition(left), rawErr: guard.offlineDefinition(right)}, true
}

func (guard *heterociProbeSSAGuard) offlineBodyNilAssignments(function *ast.FuncDecl, body types.Object) []heterociProbeOfflineAssignment {
	object := guard.pkg.Types.Scope().Lookup("isNilProbeValue")
	if object == nil || body == nil {
		return nil
	}
	var assignments []heterociProbeOfflineAssignment
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if candidate, ok := guard.offlineBodyNilAssignment(assignment, object, body); ok {
			assignments = append(assignments, candidate)
		}
		return true
	})
	return assignments
}

func (guard *heterociProbeSSAGuard) offlineBodyNilAssignment(assignment *ast.AssignStmt, object, body types.Object) (heterociProbeOfflineAssignment, bool) {
	if len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return heterociProbeOfflineAssignment{}, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || guard.offlineObject(call.Fun) != object {
		return heterociProbeOfflineAssignment{}, false
	}
	argument, ok := heterociProbeIdent(guard.pkg.TypesInfo, callArg(call, 0))
	if !ok || argument != body {
		return heterociProbeOfflineAssignment{}, false
	}
	name, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return heterociProbeOfflineAssignment{}, false
	}
	return heterociProbeOfflineAssignment{call: call, object: guard.offlineDefinition(name)}, true
}

func (guard *heterociProbeSSAGuard) offlineContextAssignments(function *ast.FuncDecl, contextParam types.Object) []heterociProbeOfflineAssignment {
	method := guard.contextErrMethodObject()
	if method == nil || contextParam == nil || guard.offlineContextErrCallCount(function, method, contextParam) != 1 {
		return nil
	}
	var assignments []heterociProbeOfflineAssignment
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if candidate, ok := guard.offlineContextAssignment(assignment, method, contextParam); ok {
			assignments = append(assignments, candidate)
		}
		return true
	})
	return assignments
}

func (guard *heterociProbeSSAGuard) offlineContextErrCallCount(function *ast.FuncDecl, method, contextParam types.Object) int {
	count := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && guard.offlineObject(call.Fun) == method && guard.offlineObject(selector.X) == contextParam {
			count++
		}
		return true
	})
	return count
}

func (guard *heterociProbeSSAGuard) offlineContextAssignment(assignment *ast.AssignStmt, method, contextParam types.Object) (heterociProbeOfflineAssignment, bool) {
	if len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return heterociProbeOfflineAssignment{}, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok {
		return heterociProbeOfflineAssignment{}, false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || guard.offlineObject(call.Fun) != method || guard.offlineObject(selector.X) != contextParam {
		return heterociProbeOfflineAssignment{}, false
	}
	name, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return heterociProbeOfflineAssignment{}, false
	}
	return heterociProbeOfflineAssignment{call: call, object: guard.offlineDefinition(name)}, true
}

func (guard *heterociProbeSSAGuard) offlineSnapshotAssignments(function *ast.FuncDecl, rawErr types.Object) []heterociProbeOfflineAssignment {
	snapshot := guard.pkg.Types.Scope().Lookup("snapshotProbeExternalError")
	typeName := guard.pkg.Types.Scope().Lookup("probeExternalErrorSnapshot")
	if snapshot == nil || typeName == nil || rawErr == nil {
		return nil
	}
	wantType := typeName.Type()
	var assignments []heterociProbeOfflineAssignment
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if candidate, ok := guard.offlineSnapshotAssignment(assignment, snapshot, wantType, rawErr); ok {
			assignments = append(assignments, candidate)
		}
		return true
	})
	return assignments
}

func (guard *heterociProbeSSAGuard) offlineSnapshotAssignment(assignment *ast.AssignStmt, snapshot types.Object, wantType types.Type, rawErr types.Object) (heterociProbeOfflineAssignment, bool) {
	if len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return heterociProbeOfflineAssignment{}, false
	}
	left, ok := assignment.Lhs[0].(*ast.Ident)
	leftObject := guard.offlineDefinition(left)
	if !ok || leftObject == nil || !types.Identical(leftObject.Type(), wantType) {
		return heterociProbeOfflineAssignment{}, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || guard.offlineObject(call.Fun) != snapshot {
		return heterociProbeOfflineAssignment{}, false
	}
	argument, ok := heterociProbeIdent(guard.pkg.TypesInfo, callArg(call, 0))
	return heterociProbeOfflineAssignment{call: call, object: leftObject}, ok && argument == rawErr
}

func (guard *heterociProbeSSAGuard) offlineConditionMatches(conditions []ast.Expr, evidence *heterociProbeOfflineEvidence) bool {
	var terms []ast.Expr
	for _, condition := range conditions {
		terms = append(terms, heterociProbeAndTerms(condition)...)
	}
	if len(terms) != 3 {
		return false
	}
	seen := make(map[int]bool, len(terms))
	for _, term := range terms {
		key := 0
		switch {
		case guard.offlineObject(term) == evidence.bodyNil:
			key = 1
		case guard.offlineContextNilTerm(term, evidence.ctxErr):
			key = 2
		case guard.offlineSnapshotFieldTerm(term, evidence.transportErr):
			key = 3
		}
		if key == 0 || seen[key] {
			return false
		}
		seen[key] = true
	}
	return len(seen) == 3
}

func heterociProbeAndTerms(expression ast.Expr) []ast.Expr {
	if parenthesized, ok := expression.(*ast.ParenExpr); ok {
		return heterociProbeAndTerms(parenthesized.X)
	}
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.LAND {
		return []ast.Expr{expression}
	}
	return append(heterociProbeAndTerms(binary.X), heterociProbeAndTerms(binary.Y)...)
}

func (guard *heterociProbeSSAGuard) offlineContextNilTerm(expression ast.Expr, contextErr types.Object) bool {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.EQL {
		return false
	}
	left, leftOK := expressionIdent(guard.pkg.TypesInfo, binary.X)
	right, rightOK := expressionIdent(guard.pkg.TypesInfo, binary.Y)
	return leftOK && left == contextErr && heterociProbeNilIdent(binary.Y) || rightOK && right == contextErr && heterociProbeNilIdent(binary.X)
}

func (guard *heterociProbeSSAGuard) offlineSnapshotFieldTerm(expression ast.Expr, transportErr types.Object) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || guard.offlineObject(selector.X) != transportErr {
		return false
	}
	selection := guard.pkg.TypesInfo.Selections[selector]
	return selection != nil && selection.Obj() == guard.offlineSnapshotFieldObject()
}

func (guard *heterociProbeSSAGuard) offlineSnapshotFieldObject() types.Object {
	typeName, ok := guard.pkg.Types.Scope().Lookup("probeExternalErrorSnapshot").(*types.TypeName)
	if !ok {
		return nil
	}
	structure, ok := typeName.Type().Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	for field := range structure.Fields() {
		if field.Name() == "onlyUnavailable" {
			return field
		}
	}
	return nil
}

func (guard *heterociProbeSSAGuard) offlineFunctionParams(function *ast.FuncDecl) (types.Object, types.Object) {
	decl, ok := guard.pkg.TypesInfo.Defs[function.Name].(*types.Func)
	if !ok {
		return nil, nil
	}
	signature, ok := decl.Type().(*types.Signature)
	if !ok {
		return nil, nil
	}
	contextType := guard.offlineNamedType("context", "Context")
	transportType := guard.pkg.Types.Scope().Lookup("ProbeTransport")
	var contextParam, transportParam types.Object
	for parameter := range signature.Params().Variables() {
		switch {
		case contextType != nil && types.Identical(parameter.Type(), contextType):
			contextParam = parameter
		case transportType != nil && types.Identical(parameter.Type(), transportType.Type()):
			transportParam = parameter
		}
	}
	return contextParam, transportParam
}

func (guard *heterociProbeSSAGuard) offlineNamedType(packageName, typeName string) types.Type {
	packageObject := guard.importedPackage(packageName)
	if packageObject == nil {
		return nil
	}
	object := packageObject.Scope().Lookup(typeName)
	if object == nil {
		return nil
	}
	return object.Type()
}

func (guard *heterociProbeSSAGuard) offlineObject(expression ast.Expr) types.Object {
	return heterociProbeIdentObject(guard.pkg.TypesInfo, expression)
}

func (guard *heterociProbeSSAGuard) offlineDefinition(identifier *ast.Ident) types.Object {
	if identifier == nil {
		return nil
	}
	return heterociProbeIdentObject(guard.pkg.TypesInfo, identifier)
}

func heterociProbeIdentObject(info *types.Info, expression ast.Expr) types.Object {
	if identifier, ok := expression.(*ast.Ident); ok {
		if object := info.Uses[identifier]; object != nil {
			return object
		}
		return info.Defs[identifier]
	}
	if selector, ok := expression.(*ast.SelectorExpr); ok {
		if selection := info.Selections[selector]; selection != nil {
			return selection.Obj()
		}
		return info.Uses[selector.Sel]
	}
	return nil
}

func heterociProbeIdent(info *types.Info, expression ast.Expr) (types.Object, bool) {
	object := heterociProbeIdentObject(info, expression)
	return object, object != nil
}

func expressionIdent(info *types.Info, expression ast.Expr) (types.Object, bool) {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return nil, false
	}
	object := heterociProbeIdentObject(info, identifier)
	return object, object != nil
}

func callArg(call *ast.CallExpr, index int) ast.Expr {
	if call == nil || index < 0 || index >= len(call.Args) {
		return nil
	}
	return call.Args[index]
}

func heterociProbeNilIdent(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "nil"
}

func heterociProbeNodesBefore(branch ast.Node, nodes ...ast.Node) bool {
	for _, node := range nodes {
		if node == nil || node.Pos() >= branch.Pos() {
			return false
		}
	}
	return true
}
