package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func remoteCIFunctionContainsIdentifier(file *ast.File, functionName, want string) bool {
	function := remoteCIFunctionByName(file, functionName)
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier != nil && identifier.Name == want {
			found = true
			return false
		}
		return true
	})
	return found
}

func remoteCIFunctionHasSelector(file *ast.File, functionName, packageName, selectorName string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		found := false
		ast.Inspect(function, func(node ast.Node) bool {
			expression, ok := node.(*ast.SelectorExpr)
			if ok && remoteCISelectorMatches(expression, packageName, selectorName) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	return false
}

func remoteCISelectorMatches(expression *ast.SelectorExpr, packageName, selectorName string) bool {
	identifier, ok := expression.X.(*ast.Ident)
	return ok && identifier.Name == packageName && expression.Sel.Name == selectorName
}

func remoteCIFunctionExists(file *ast.File, want string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == want {
			return true
		}
	}
	return false
}

func remoteCIFunctionByName(file *ast.File, want string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == want {
			return function
		}
	}
	return nil
}

func remoteCIFunctionCallCount(file *ast.File, calledName string) int {
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == calledName {
			count++
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == calledName {
			count++
		}
		return true
	})
	return count
}

func readRemoteCIContractGuardFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func relativeRemoteCIContractPath(t *testing.T, root, path string) string {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(relative)
}

func remoteCIContainsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func remoteCIConcurrencyCapViolations(file *ast.File) []string {
	violations := map[string]bool{}
	for identifier := range remoteCIForbiddenIdentifiers(file) {
		remoteCIRecordAdmissionCapIdentifier(violations, identifier)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		remoteCIRecordConcurrencyCapCall(violations, node)
		return true
	})
	return remoteCIViolationList(violations)
}

func remoteCIRecordAdmissionCapIdentifier(violations map[string]bool, identifier string) {
	if identifier == "SetLimit" || remoteCIAdmissionCapIdentifier(identifier) {
		violations["identifier "+identifier] = true
	}
}

func remoteCIRecordConcurrencyCapCall(violations map[string]bool, node ast.Node) {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return
	}
	if remoteCIIsSemaphoreNewWeightedCall(call) {
		violations["call semaphore.NewWeighted"] = true
	}
	if channel, ok := remoteCIAdmissionChannelFromMakeCall(call); ok {
		violations["buffered admission channel type "+remoteCIExpressionName(channel.Value)] = true
	}
}

func remoteCIIsSemaphoreNewWeightedCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && remoteCISelectorMatches(selector, "semaphore", "NewWeighted")
}

func remoteCIAdmissionChannelFromMakeCall(call *ast.CallExpr) (*ast.ChanType, bool) {
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok || identifier.Name != "make" || len(call.Args) != 2 {
		return nil, false
	}
	channel, ok := call.Args[0].(*ast.ChanType)
	if !ok || !remoteCIAdmissionChannelType(channel) {
		return nil, false
	}
	return channel, true
}

func remoteCIAdmissionCapIdentifier(identifier string) bool {
	normalized := strings.ToLower(identifier)
	for _, marker := range []string{"maxshard", "maxconcurrency", "shardbatch", "batchlimit", "coordinatorlimit", "admissioncap", "admissionlimit", "globalhooklock", "activejoblock", "calibrationlock", "sharedrawtoken"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func remoteCIAdmissionChannelType(channel *ast.ChanType) bool {
	_, ok := channel.Value.(*ast.StructType)
	return ok
}

func remoteCIExpressionName(expression ast.Expr) string {
	if _, ok := expression.(*ast.StructType); ok {
		return "struct{}"
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Name
	}
	return "unknown"
}

func remoteCIParseGuardFixture(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "remote_ci_guard_fixture.go", source, 0)
	if err != nil {
		t.Fatalf("parse remote CI guard fixture: %v", err)
	}
	return file
}

func remoteCIWorkloadPassReuseViolations(file *ast.File) []string {
	violations := map[string]bool{}
	for identifier := range remoteCIForbiddenIdentifiers(file) {
		if identifier == "remoteCIRunHasOnlyReusedWorkloadResults" {
			continue
		}
		normalized := strings.ToLower(identifier)
		if remoteCILegacyWorkloadReuseIdentifier(normalized) {
			violations["identifier "+identifier] = true
		}
	}
	return remoteCIViolationList(violations)
}

func remoteCILegacyWorkloadReuseIdentifier(normalized string) bool {
	for _, forbidden := range []string{
		"workloadpasscache",
		"reusedworkloadresult",
		"ciworkloadfingerprints",
		"workloadpassresultcache",
		"passfile",
		"passmarker",
	} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

func remoteCIAuthorityWriterViolations(file *ast.File) []string {
	violations := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok {
			remoteCIRecordAuthorityWriterViolations(violations, call)
		}
		return true
	})
	return remoteCIViolationList(violations)
}

func remoteCIRecordAuthorityWriterViolations(violations map[string]bool, call *ast.CallExpr) {
	selector, ok := remoteCIAuthorityWriterSelector(call)
	if !ok {
		return
	}
	for _, path := range remoteCIAuthorityWriterStatePaths(call) {
		violations["os."+selector.Sel.Name+" "+path] = true
	}
}

func remoteCIAuthorityWriterSelector(call *ast.CallExpr) (*ast.SelectorExpr, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !remoteCISelectorMatchesPackage(selector, "os") {
		return nil, false
	}
	return selector, remoteCIAuthorityWriterMethod(selector.Sel.Name)
}

func remoteCIAuthorityWriterMethod(name string) bool {
	switch name {
	case "WriteFile", "Create", "OpenFile":
		return true
	default:
		return false
	}
}

func remoteCISelectorMatchesPackage(selector *ast.SelectorExpr, packageName string) bool {
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == packageName
}

func remoteCIAuthorityWriterStatePaths(call *ast.CallExpr) []string {
	var paths []string
	for _, argument := range call.Args {
		literal, ok := argument.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		path := strings.ToLower(strings.Trim(literal.Value, "\""))
		if remoteCIContainsAny(path, []string{".json", ".sqlite", ".db"}) {
			paths = append(paths, path)
		}
	}
	return paths
}

func remoteCIViolationList(violations map[string]bool) []string {
	result := make([]string, 0, len(violations))
	for violation := range violations {
		result = append(result, violation)
	}
	slices.Sort(result)
	return result
}

func remoteCIHundredSecondTerminationViolations(file *ast.File) []string {
	targets := remoteCIHundredSecondDurationTargets(file)
	violations := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && remoteCITerminatingCall(call) {
			remoteCIRecordTerminatingHundredSecondViolations(call, targets, violations)
		}
		return true
	})
	return remoteCIViolationList(violations)
}

func remoteCIHundredSecondDurationTargets(file *ast.File) map[string]bool {
	targets := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.ValueSpec:
			remoteCIRecordValueSpecHundredSecondTargets(statement, targets)
		case *ast.AssignStmt:
			remoteCIRecordAssignHundredSecondTargets(statement, targets)
		}
		return true
	})
	return targets
}

func remoteCIRecordValueSpecHundredSecondTargets(statement *ast.ValueSpec, targets map[string]bool) {
	for index, value := range statement.Values {
		if index < len(statement.Names) && remoteCIIsHundredSecondDuration(value) {
			targets[statement.Names[index].Name] = true
		}
	}
}

func remoteCIRecordAssignHundredSecondTargets(statement *ast.AssignStmt, targets map[string]bool) {
	for index, value := range statement.Rhs {
		if identifier, ok := remoteCIHundredSecondAssignmentTarget(statement, index, value); ok {
			targets[identifier.Name] = true
		}
	}
}

func remoteCIHundredSecondAssignmentTarget(statement *ast.AssignStmt, index int, value ast.Expr) (*ast.Ident, bool) {
	if index >= len(statement.Lhs) || !remoteCIIsHundredSecondDuration(value) {
		return nil, false
	}
	identifier, ok := statement.Lhs[index].(*ast.Ident)
	return identifier, ok
}

func remoteCIRecordTerminatingHundredSecondViolations(call *ast.CallExpr, targets, violations map[string]bool) {
	for _, argument := range call.Args {
		if remoteCIIsHundredSecondDuration(argument) {
			violations[remoteCICallName(call)] = true
		}
		if identifier, ok := argument.(*ast.Ident); ok && targets[identifier.Name] {
			violations[remoteCICallName(call)+" via "+identifier.Name] = true
		}
	}
}

func remoteCIIsHundredSecondDuration(expression ast.Expr) bool {
	if remoteCIIsContractShardTargetDuration(expression) {
		return true
	}
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.MUL {
		return false
	}
	return remoteCIIsLiteralHundredSecondDuration(binary)
}

func remoteCIIsContractShardTargetDuration(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "cicontract" && selector.Sel.Name == "ShardTargetDuration"
}

func remoteCIIsLiteralHundredSecondDuration(binary *ast.BinaryExpr) bool {
	literal, ok := binary.Y.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := literal.X.(*ast.Ident)
	if !ok || packageName.Name != "time" || (literal.Sel.Name != "Second" && literal.Sel.Name != "Millisecond") {
		return false
	}
	value := remoteCIIntegerLiteral(binary.X)
	return (literal.Sel.Name == "Second" && value == 100) || (literal.Sel.Name == "Millisecond" && value == 100000)
}

func remoteCIIntegerLiteral(expression ast.Expr) int64 {
	if value, ok := remoteCIPlainIntegerLiteral(expression); ok {
		return value
	}
	if call, ok := expression.(*ast.CallExpr); ok && remoteCIIsTimeDurationCall(call) {
		return remoteCIIntegerLiteral(call.Args[0])
	}
	return -1
}

func remoteCIPlainIntegerLiteral(expression ast.Expr) (int64, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return 0, false
	}
	var value int64
	for _, digit := range literal.Value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		value = value*10 + int64(digit-'0')
	}
	return value, true
}

func remoteCIIsTimeDurationCall(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && remoteCISelectorMatches(selector, "time", "Duration")
}

func remoteCITerminatingCall(call *ast.CallExpr) bool {
	name := strings.ToLower(remoteCICallName(call))
	for _, terminating := range []string{"timeout", "deadline", "cancel", "kill", "fail", "cleanup"} {
		if strings.Contains(name, terminating) {
			return true
		}
	}
	return false
}

func remoteCICallName(call *ast.CallExpr) string {
	if identifier, ok := call.Fun.(*ast.Ident); ok {
		return identifier.Name
	}
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		return selector.Sel.Name
	}
	return "call"
}
