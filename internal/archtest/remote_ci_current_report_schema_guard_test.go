package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// TestRemoteCICurrentReportSchemaContract 把 current report、结构化 profile 和 current receipt 绑定为唯一生产路径。
func TestRemoteCICurrentReportSchemaContract(t *testing.T) {
	root := repoRoot(t)
	files := map[string]*ast.File{
		"plan":       parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/executor_plan.go")),
		"report":     parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/executor_plan_report.go")),
		"header":     parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/executor_plan_report_agent_token.go")),
		"profile":    parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/executor_plan_report_profile.go")),
		"timing":     parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/executor_plan_report_timing.go")),
		"worker":     parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/executor_execution_result.go")),
		"projection": parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/remoteci/workload_projection.go")),
		"aggregate":  parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_execution_profile.go")),
		"results":    parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_results.go")),
		"query":      parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/ci_query_store_remote.go")),
		"receipt":    parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/validation.go")),
	}
	forbidden := []string{
		"executorPlanReportSchemaVersion", "executorPlanTimingSchemaVersion",
		"encodeLegacyPlanTimingRecords", "decodeLegacyPlanTimingReportRecords",
		"decodePlanTimingRecords", "decodePlanTimingRecord", "validatePlanTimingRecordSequence",
		"normalizeRemoteWorkloadExecutionProfile", "legacyNotMeasuredExecutionProfile",
		"unmeasuredExecutionProfile",
		"legacyResultReceiptSchemaVersion",
	}
	for name, file := range files {
		if identifier := currentReportForbiddenIdentifier(file, forbidden); identifier != "" {
			t.Errorf("%s restores forbidden remote CI compatibility identifier %q", name, identifier)
		}
	}
	assertCurrentReportSchemaConsumers(t, files)
	assertCurrentStructuredExecutionProfile(t, files)
}

// TestRemoteCICurrentReportSchemaGuardRejectsCounterexamples 证明兼容函数和时间差推导无法绕过守卫。
func TestRemoteCICurrentReportSchemaGuardRejectsCounterexamples(t *testing.T) {
	legacy := parseCurrentReportContractSource(t, `package gate; func legacyNotMeasuredExecutionProfile() {}`)
	if currentReportForbiddenIdentifier(legacy, []string{"legacyNotMeasuredExecutionProfile"}) == "" {
		t.Fatal("legacy compatibility counterexample unexpectedly passed")
	}
	inferred := parseCurrentReportContractSource(t, `package gate; func loadRemoteCIExecutionRows() { completed.Sub(started) }`)
	if !currentReportFunctionCalls(inferred, "loadRemoteCIExecutionRows", "Sub") {
		t.Fatal("timestamp-derived execution profile counterexample unexpectedly passed")
	}
}

func assertCurrentReportSchemaConsumers(t *testing.T, files map[string]*ast.File) {
	t.Helper()
	checks := []struct {
		file, function, target string
	}{
		{file: "header", function: "validatePlanExecutionReportSchema", target: "ExecutorPlanReportSchemaVersion"},
		{file: "header", function: "decodePlanReportHeader", target: "validatePlanExecutionReportSchema"},
		{file: "report", function: "validatePlanExecutionReportHeader", target: "validatePlanExecutionReportSchema"},
		{file: "timing", function: "encodePlanTimingReportRecords", target: "ExecutorPlanReportSchemaVersion"},
		{file: "timing", function: "decodePlanTimingReportRecords", target: "ExecutorPlanReportSchemaVersion"},
		{file: "projection", function: "remoteFreshWorkloadExecutions", target: "ExecutorPlanReportSchemaVersion"},
		{file: "results", function: "aggregateWorkloadGate", target: "aggregateWorkloadExecutionProfile"},
		{file: "aggregate", function: "aggregateWorkloadExecutionProfile", target: "shardWorkloadIntervals"},
		{file: "aggregate", function: "aggregateWorkloadExecutionProfile", target: "ValidateAggregate"},
		{file: "receipt", function: "validateIdentity", target: "ResultReceiptSchemaVersion"},
	}
	if !currentReportHasIdentifier(files["plan"], "ExecutorPlanReportSchemaVersion") {
		t.Error("executor report schema producer is missing")
	}
	for _, check := range checks {
		if !currentReportFunctionReferences(files[check.file], check.function, check.target) {
			t.Errorf("%s.%s must reference %s", check.file, check.function, check.target)
		}
	}
}

func assertCurrentStructuredExecutionProfile(t *testing.T, files map[string]*ast.File) {
	t.Helper()
	query := files["query"]
	if !currentReportFunctionCalls(query, "decodeStoredRemoteCIExecutionProfile", "DecodeStrictJSON") {
		t.Error("stored execution profiles must use strict structured decoding")
	}
	if currentReportFunctionCalls(query, "loadRemoteCIExecutionRows", "Sub") {
		t.Error("stored execution profiles must not be inferred from started/completed timestamps")
	}
	for _, name := range []string{"query", "projection", "worker", "profile", "aggregate", "results"} {
		file := files[name]
		if currentReportHasString(file, "not_measured") {
			t.Errorf("remote CI %s must not synthesize not_measured profiles", name)
		}
	}
}

func parseCurrentReportContractFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func parseCurrentReportContractSource(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "counterexample.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func currentReportForbiddenIdentifier(file *ast.File, forbidden []string) string {
	blocked := make(map[string]struct{}, len(forbidden))
	for _, name := range forbidden {
		blocked[name] = struct{}{}
	}
	found := ""
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier != nil {
			if _, blocked := blocked[identifier.Name]; blocked && found == "" {
				found = identifier.Name
			}
		}
		return found == ""
	})
	return found
}

func currentReportHasIdentifier(file *ast.File, target string) bool {
	if file == nil {
		return false
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier != nil && identifier.Name == target {
			found = true
		}
		return !found
	})
	return found
}

func currentReportFunctionReferences(file *ast.File, functionName, target string) bool {
	function := currentReportFunction(file, functionName)
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier != nil && identifier.Name == target {
			found = true
		}
		return !found
	})
	return found
}

func currentReportFunctionCalls(file *ast.File, functionName, target string) bool {
	function := currentReportFunction(file, functionName)
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && call != nil && currentReportCallName(call) == target {
			found = true
		}
		return !found
	})
	return found
}

func currentReportFunction(file *ast.File, name string) *ast.FuncDecl {
	if file == nil {
		return nil
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function != nil && function.Name != nil && function.Name.Name == name {
			return function
		}
	}
	return nil
}

func currentReportCallName(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	switch function := call.Fun.(type) {
	case *ast.Ident:
		if function == nil {
			return ""
		}
		return function.Name
	case *ast.SelectorExpr:
		if function == nil || function.Sel == nil {
			return ""
		}
		return function.Sel.Name
	default:
		return ""
	}
}

func currentReportHasString(file *ast.File, target string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal == nil || literal.Kind != token.STRING {
			return !found
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && value == target {
			found = true
		}
		return !found
	})
	return found
}
