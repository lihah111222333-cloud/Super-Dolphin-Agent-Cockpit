package archtest_test

import (
	"go/ast"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIPlanReportDecoderBindingContract 锁定生产报告解码必须绑定 coordinator gate 集合，并在有身份时绑定 agent 摘要。
func TestRemoteCIPlanReportDecoderBindingContract(t *testing.T) {
	root := repoRoot(t)
	assertProductionDecoderSources(t, root, "internal/devtools/gate", true)
	assertProductionDecoderSources(t, root, "internal/devtools/remoteci", false)
	assertCoordinatorDecoderBinding(t, root)
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
	wait := parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_wait.go"))
	decode := currentReportFunction(wait, "decodeReportLog")
	if decode == nil {
		t.Fatal("decodeReportLog is missing")
	}
	if currentReportFunctionCalls(wait, "decodeReportLog", "decodePlanExecutionReportChunks") {
		t.Error("decodeReportLog must not call the unbound plan report decoder")
	}
	for _, strict := range []string{"DecodePlanExecutionReportChunksForGateSet", "DecodePlanExecutionReportChunksForGateSetAndAgentTokenDigest"} {
		if !currentReportFunctionCalls(wait, "decodeReportLog", strict) {
			t.Errorf("decodeReportLog must call strict decoder %s", strict)
		}
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
