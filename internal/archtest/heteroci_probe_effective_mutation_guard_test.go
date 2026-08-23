package archtest

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

// TestHeterociEffectiveRoutingSnapshotMutationBuiltinGuard 固定有效快照不得被写效应 builtin 改写。
func TestHeterociEffectiveRoutingSnapshotMutationBuiltinGuard(t *testing.T) {
	guard, err := loadHeterociEffectiveProduction(t)
	if err != nil {
		t.Fatal(err)
	}
	if violations := heterociEffectiveMutationBuiltinViolations(guard.function); len(violations) != 0 {
		t.Fatalf("effectiveRoutingSnapshot builtin mutation violations: %s", strings.Join(violations, "; "))
	}
}

func TestHeterociEffectiveRoutingSnapshotMutationBuiltinGuardRejectsSynthetic(t *testing.T) {
	cases := []struct {
		name    string
		imports string
		effect  string
		want    string
	}{
		{name: "copy current node", effect: "copy(effective.Nodes[index:index+1], source.Nodes[index:index+1])", want: "copy"},
		{name: "copy whole slice", effect: "copy(effective.Nodes, source.Nodes)", want: "copy"},
		{name: "clear Nodes", effect: "clear(effective.Nodes)", want: "clear"},
		{name: "slices Replace", imports: "slices", effect: "effective.Nodes = slices.Replace(effective.Nodes, index, index+1, source.Nodes[index])", want: "slices.Replace"},
		{name: "slices Reverse", imports: "slices", effect: "slices.Reverse(effective.Nodes)", want: "slices.Reverse"},
		{name: "go slices Reverse", imports: "slices", effect: "go slices.Reverse(effective.Nodes)", want: "slices.Reverse"},
		{name: "defer slices Reverse", imports: "slices", effect: "defer slices.Reverse(effective.Nodes)", want: "slices.Reverse"},
		{name: "reflect Value Set", imports: "reflect", effect: "reflect.ValueOf(&effective.Nodes).Elem().Set(reflect.ValueOf(source.Nodes))", want: "reflect.Value.Set"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			guard := loadHeterociEffectiveSynthetic(t, heterociEffectiveMutationFixture(testCase.imports, testCase.effect))
			violations := strings.Join(heterociEffectiveMutationBuiltinViolations(guard.function), "; ")
			if !strings.Contains(violations, testCase.want) {
				t.Fatalf("synthetic builtin violations = %q, want %q", violations, testCase.want)
			}
		})
	}
}

func heterociEffectiveMutationFixture(importPath, effect string) string {
	imports := ""
	if importPath != "" {
		imports = `import "` + importPath + `"` + "\n"
	}
	return `package heteroci
	` + imports + `
	type ResourceCapacity struct { CPUMilli int64; MemoryBytes int64; MaxConcurrency int }
	type NodeSnapshot struct { Available ResourceCapacity }
	type SnapshotSet struct { Nodes []NodeSnapshot }
	type nodeReservationKey struct{}

	func cloneRoutingSnapshotSet(source SnapshotSet) SnapshotSet { return source }
	func checkedSubtractReservation(available, reserved ResourceCapacity) (ResourceCapacity, error) {
		available.CPUMilli -= reserved.CPUMilli
		return available, nil
	}
	func effectiveRoutingSnapshot(source SnapshotSet, current map[nodeReservationKey]ResourceCapacity) (SnapshotSet, error) {
		effective := cloneRoutingSnapshotSet(source)
		for index := range effective.Nodes {
			node := &effective.Nodes[index]
			reserved := current[nodeReservationKey{}]
			if reserved == (ResourceCapacity{}) { continue }
			available, err := checkedSubtractReservation(node.Available, reserved)
			if err != nil { return SnapshotSet{}, err }
			node.Available = available
			` + effect + `
		}
		return effective, nil
	}`
}

func heterociEffectiveMutationBuiltinViolations(function *ssa.Function) []string {
	if function == nil {
		return []string{"effectiveRoutingSnapshot SSA root is missing"}
	}
	found := make(map[string]bool)
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			heterociEffectiveMutationInstruction(function, instruction, found)
		}
	}
	result := make([]string, 0, len(found))
	for violation := range found {
		result = append(result, violation)
	}
	sort.Strings(result)
	return result
}

func heterociEffectiveMutationInstruction(function *ssa.Function, instruction ssa.Instruction, found map[string]bool) {
	call, ok := instruction.(ssa.CallInstruction)
	if !ok {
		return
	}
	common := call.Common()
	if builtin, ok := common.Value.(*ssa.Builtin); ok {
		heterociEffectiveMutationBuiltin(builtin, found)
		return
	}
	callee := common.StaticCallee()
	if callee == nil {
		if common.IsInvoke() {
			found[fmt.Sprintf("effectiveRoutingSnapshot contains non-whitelisted invoke %q", common.Method.FullName())] = true
		}
		return
	}
	if heterociEffectiveMutationStaticCallAllowed(function, callee) {
		return
	}
	found[fmt.Sprintf("effectiveRoutingSnapshot contains non-whitelisted static callee %q", heterociEffectiveMutationStaticCalleeLabel(callee))] = true
}

func heterociEffectiveMutationBuiltin(builtin *ssa.Builtin, found map[string]bool) {
	name := builtin.Name()
	if heterociEffectiveMutationPureBuiltin(name) {
		return
	}
	found[fmt.Sprintf("effectiveRoutingSnapshot contains non-whitelisted builtin %q", name)] = true
}

func heterociEffectiveMutationStaticCallAllowed(owner, callee *ssa.Function) bool {
	object, ok := callee.Object().(*types.Func)
	if !ok || object.Pkg() == nil {
		return false
	}
	if object.Pkg().Path() == "fmt" {
		return object.Name() == "Errorf"
	}
	if object.Pkg().Path() != heterociEffectiveMutationPackagePath(owner) {
		return false
	}
	switch object.Name() {
	case "cloneRoutingSnapshotSet", "checkedSubtractReservation":
		return true
	default:
		return false
	}
}

func heterociEffectiveMutationPackagePath(function *ssa.Function) string {
	if function == nil {
		return ""
	}
	if pkg := function.Package(); pkg != nil && pkg.Pkg != nil {
		return pkg.Pkg.Path()
	}
	if object, ok := function.Object().(*types.Func); ok && object.Pkg() != nil {
		return object.Pkg().Path()
	}
	return ""
}

func heterociEffectiveMutationStaticCalleeLabel(function *ssa.Function) string {
	object, ok := function.Object().(*types.Func)
	if !ok {
		return function.String()
	}
	name := object.Name()
	if signature, ok := object.Type().(*types.Signature); ok && signature.Recv() != nil {
		receiver := signature.Recv().Type()
		if pointer, ok := receiver.(*types.Pointer); ok {
			receiver = pointer.Elem()
		}
		if named, ok := receiver.(*types.Named); ok {
			name = named.Obj().Name() + "." + name
		}
	}
	if object.Pkg() == nil {
		return name
	}
	return object.Pkg().Path() + "." + name
}

func heterociEffectiveMutationPureBuiltin(name string) bool {
	switch name {
	case "cap", "complex", "imag", "len", "make", "max", "min", "new", "real", "recover":
		return true
	case "panic", "print", "println":
		return true
	default:
		return false
	}
}
