package archtest_test

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// TestResultReceiptWorkloadPlanGuard keeps the authoritative workload plan on
// the signed receipt and rejects a shard-only reconstruction path.
func TestResultReceiptWorkloadPlanGuard(t *testing.T) {
	root := repoRoot(t)
	contracts := parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/contracts.go"))
	receipt := parseCurrentReportContractFile(t, filepath.Join(root, "internal/devtools/gate/result_receipt_v2.go"))
	if !resultReceiptSchemaVersionIs(contracts, 4) {
		t.Fatal("ResultReceiptSchemaVersion must be 4 for the workload_plan receipt shape")
	}
	if !resultReceiptHasWorkloadPlanField(contracts) {
		t.Fatal("ResultReceipt must carry the workload_plan field")
	}
	if !resultReceiptFunctionUsesWorkloadPlan(receipt, "validatePassedShardReceipt") ||
		!resultReceiptFunctionUsesWorkloadPlan(receipt, "validateStoredPassedShardReceipt") {
		t.Fatal("receipt validation must bind both live and stored paths to workload_plan")
	}
	if !resultReceiptShardSetBindsWorkloadPlan(receipt) {
		t.Fatal("uncheckedResultReceiptShardSet must map the producer workload plan into ContainerShardSet")
	}
}

// TestResultReceiptWorkloadPlanGuardRejectsShardOnlyCounterexample proves the
// guard fails when the producer mapping is removed rather than accepting a
// self-consistent shard-only reconstruction.
func TestResultReceiptWorkloadPlanGuardRejectsShardOnlyCounterexample(t *testing.T) {
	file := parseCurrentReportContractSource(t, `package gate
type WorkloadExecutionPlan struct{}
type ContainerShardSet struct{ WorkloadPlan WorkloadExecutionPlan }
func uncheckedResultReceiptShardSet(workloadPlan WorkloadExecutionPlan) ContainerShardSet {
	return ContainerShardSet{}
}`)
	if resultReceiptShardSetBindsWorkloadPlan(file) {
		t.Fatal("shard-only receipt reconstruction counterexample unexpectedly passed")
	}
}

func resultReceiptSchemaVersionIs(file *ast.File, want int) bool {
	literal := resultReceiptSchemaVersionLiteral(file)
	value, err := strconv.Atoi(literal)
	return err == nil && value == want
}

func resultReceiptSchemaVersionLiteral(file *ast.File) string {
	literal := ""
	ast.Inspect(file, func(node ast.Node) bool {
		valueSpec, ok := node.(*ast.ValueSpec)
		if !ok || valueSpec == nil {
			return literal == ""
		}
		for index, name := range valueSpec.Names {
			if name.Name != "ResultReceiptSchemaVersion" || index >= len(valueSpec.Values) {
				continue
			}
			candidate, ok := valueSpec.Values[index].(*ast.BasicLit)
			if ok && candidate.Kind == token.INT {
				literal = candidate.Value
			}
		}
		return literal == ""
	})
	return literal
}

func resultReceiptHasWorkloadPlanField(file *ast.File) bool {
	for _, field := range resultReceiptStructFields(file) {
		if len(field.Names) != 1 || field.Names[0].Name != "WorkloadPlan" || field.Tag == nil {
			continue
		}
		value, err := strconv.Unquote(field.Tag.Value)
		if err == nil && value == `json:"workload_plan"` {
			return true
		}
	}
	return false
}

func resultReceiptStructFields(file *ast.File) []*ast.Field {
	var fields []*ast.Field
	ast.Inspect(file, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != "ResultReceipt" {
			return true
		}
		structType, ok := typeSpec.Type.(*ast.StructType)
		if ok && structType.Fields != nil {
			fields = structType.Fields.List
		}
		return false
	})
	return fields
}

func resultReceiptFunctionUsesWorkloadPlan(file *ast.File, functionName string) bool {
	function := currentReportFunction(file, functionName)
	if function == nil {
		return false
	}
	return currentReportFunctionReferences(file, functionName, "WorkloadPlan")
}

func resultReceiptShardSetBindsWorkloadPlan(file *ast.File) bool {
	function := currentReportFunction(file, "uncheckedResultReceiptShardSet")
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function, func(node ast.Node) bool {
		assignment, ok := node.(*ast.KeyValueExpr)
		if !ok || assignment == nil {
			return !found
		}
		key, keyOK := assignment.Key.(*ast.Ident)
		value, valueOK := assignment.Value.(*ast.Ident)
		if keyOK && valueOK && key.Name == "WorkloadPlan" && value.Name == "workloadPlan" {
			found = true
		}
		return !found
	})
	return found
}
