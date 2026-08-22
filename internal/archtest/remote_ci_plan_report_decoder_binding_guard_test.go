package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIPlanReportDecoderBindingContract 锁定生产报告解码必须绑定 coordinator gate 集合，并在有身份时绑定 agent 摘要。
func TestRemoteCIPlanReportDecoderBindingContract(t *testing.T) {
	root := repoRoot(t)
	assertProductionDecoderSources(t, root, "internal/devtools/gate", true)
}

func assertProductionDecoderSources(t *testing.T, root, relativeDir string, rejectWrappers bool) {
	t.Helper()
	paths := decoderSourcePaths(t, root, relativeDir)
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file := parseCurrentReportContractFile(t, path)
		if rejectWrappers {
			assertNoUnboundDecoderDeclarations(t, path, file)
		}
		for _, caller := range currentReportUnboundDecoderCallers(file) {
			t.Errorf("%s.%s calls the unbound plan report decoder", path, caller)
		}
	}
}

func decoderSourcePaths(t *testing.T, root, relativeDir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, relativeDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("%s production decoder sources are missing", relativeDir)
	}
	return paths
}

func assertNoUnboundDecoderDeclarations(t *testing.T, path string, file *ast.File) {
	t.Helper()
	for _, forbidden := range []string{"DecodePlanExecutionReportChunks", "DecodePlanExecutionReport"} {
		if currentReportFunction(file, forbidden) != nil {
			t.Errorf("%s retains unbound production report decoder %s", path, forbidden)
		}
	}
}

func assertCoordinatorDecoderBinding(t *testing.T, root string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "internal/devtools/gate/workload_packing_evidence_validation.go"))
	if err != nil || strings.TrimSpace(string(data)) == "" {
		t.Fatal("retained local Gate report validation source is empty")
	}
}

func currentReportUnboundDecoderCallers(file *ast.File) []string {
	var callers []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function == nil || function.Name == nil {
			continue
		}
		if function.Name.Name == "DecodePlanExecutionReportChunksForGateSet" || function.Name.Name == "DecodePlanExecutionReportChunksForGateSetAndAgentTokenDigest" {
			continue
		}
		if currentReportFunctionCalls(file, function.Name.Name, "decodePlanExecutionReportChunks") {
			callers = append(callers, function.Name.Name)
		}
	}
	return callers
}

// TestRemoteCIPlanReportDecoderBindingGuardRejectsCounterexamples 证明宽松 wrapper 和直接底层调用都会被守卫识别。
func TestRemoteCIPlanReportDecoderBindingGuardRejectsCounterexamples(t *testing.T) {
	legacy := parseCurrentReportContractSource(t, `package gate
func DecodePlanExecutionReportChunks(chunks []string) {}
func DecodePlanExecutionReport(text string) {}
`)
	for _, forbidden := range []string{"DecodePlanExecutionReportChunks", "DecodePlanExecutionReport"} {
		if currentReportFunction(legacy, forbidden) == nil {
			t.Fatalf("counterexample decoder %s was not identified", forbidden)
		}
	}
	direct := parseCurrentReportContractSource(t, `package remoteci
func decodeReportLog(chunks []string) { decodePlanExecutionReportChunks(chunks, nil) }
`)
	if !currentReportFunctionCalls(direct, "decodeReportLog", "decodePlanExecutionReportChunks") {
		t.Fatal("unbound decoder counterexample was not identified")
	}
}
func parseCurrentReportContractFile(t *testing.T, path string) *ast.File {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return parseCurrentReportContractSource(t, string(data))
}

func parseCurrentReportContractSource(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "current_report_fixture.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse current report source: %v", err)
	}
	return file
}

func currentReportFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	return nil
}

func currentReportFunctionCalls(file *ast.File, functionName, target string) bool {
	function := currentReportFunction(file, functionName)
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := ""
		switch expression := call.Fun.(type) {
		case *ast.Ident:
			name = expression.Name
		case *ast.SelectorExpr:
			name = expression.Sel.Name
		}
		if name == target {
			found = true
			return false
		}
		return true
	})
	return found
}
