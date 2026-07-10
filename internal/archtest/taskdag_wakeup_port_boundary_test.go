package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestTaskDAGWakeupPortsExposeOnlyRequiredMethods(t *testing.T) {
	t.Parallel()

	file := parseTaskDAGContractForWakeupPortTest(t)
	want := map[string]map[string]struct{}{
		"WakeupDispatchStore": {
			"ClaimDueWakeups": {},
			"MarkWakeupSent":  {},
			"FailWakeup":      {},
			"RetryWakeup":     {},
			"GetDAG":          {},
		},
		"WakeupReclaimStore": {
			"ReclaimStaleDispatchingWakeups":                 {},
			"MarkDispatchIncompleteNodesWithoutActiveWakeup": {},
		},
	}
	for name, wantMethods := range want {
		iface := taskDAGInterfaceForWakeupPortTest(t, file, name)
		gotMethods := map[string]struct{}{}
		for _, field := range iface.Methods.List {
			if len(field.Names) == 0 {
				t.Errorf("%s must not embed %T", name, field.Type)
				continue
			}
			if len(field.Names) != 1 {
				t.Errorf("%s method field has %d names, want 1", name, len(field.Names))
				continue
			}
			gotMethods[field.Names[0].Name] = struct{}{}
		}
		for method := range wantMethods {
			if _, ok := gotMethods[method]; !ok {
				t.Errorf("%s missing required method %s", name, method)
			}
		}
		for method := range gotMethods {
			if _, ok := wantMethods[method]; !ok {
				t.Errorf("%s exposes unrelated method %s", name, method)
			}
		}
	}
}

func parseTaskDAGContractForWakeupPortTest(t *testing.T) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(
		token.NewFileSet(),
		filepath.Join(repoRoot(t), "cmd/mcp-orch/store/taskdag/contract.go"),
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("parse taskdag contract: %v", err)
	}
	return file
}

func taskDAGInterfaceForWakeupPortTest(t *testing.T, file *ast.File, name string) *ast.InterfaceType {
	t.Helper()
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != name {
				continue
			}
			iface, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				t.Fatalf("%s is %T, want interface", name, typeSpec.Type)
			}
			return iface
		}
	}
	t.Fatalf("%s not found", name)
	return nil
}
