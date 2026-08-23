package archtest

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type heterociProbeDynamicFixture struct {
	name   string
	source string
	want   []string
}

type heterociProbeCallMatrixSpec struct {
	label    string
	method   *types.Func
	expected map[string]int
}

// TestHeterociProbeDynamicBoundarySSAGuard 冻结采集闭包的动态调用与接口站点矩阵。
func TestHeterociProbeDynamicBoundarySSAGuard(t *testing.T) {
	guard, err := loadHeterociProbeSSAGuard(findRepoRoot(t), "./internal/devtools/heteroci", heterociProbePackagePath)
	if err != nil {
		t.Fatal(err)
	}
	violations := append(guard.dynamicCallViolations(), guard.probeCallMatrixViolations()...)
	if len(violations) != 0 {
		t.Fatalf("AcquireSnapshot probe boundary violations: %s", strings.Join(violations, "; "))
	}
}

func (guard *heterociProbeSSAGuard) dynamicCallViolations() []string {
	root := guard.built.Func("AcquireSnapshot")
	if root == nil {
		return []string{"AcquireSnapshot SSA root is missing"}
	}
	found := make(map[string]bool)
	for _, function := range heterociProbeReachableSSAFunctions(root) {
		guard.recordDynamicCallViolations(function, found)
	}
	return sortedHeterociProbeSet(found)
}

func (guard *heterociProbeSSAGuard) recordDynamicCallViolations(function *ssa.Function, found map[string]bool) {
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			common := call.Common()
			if common.IsInvoke() {
				if guard.allowedProbeInvoke(function, common.Method) {
					continue
				}
				found[fmt.Sprintf("%s invokes non-whitelisted interface method %s", function.String(), heterociProbeMethodLabel(common.Method))] = true
				continue
			}
			if !guard.allowedProbeStaticClosure(function, instruction, common) && heterociProbeUnresolvedDynamicCall(common) {
				found[fmt.Sprintf("%s contains unresolved dynamic call (%T)", function.String(), common.Value)] = true
			}
		}
	}
}

func heterociProbeUnresolvedDynamicCall(common *ssa.CallCommon) bool {
	if common == nil || heterociProbeBuiltinCall(common.Value) {
		return false
	}
	if common.StaticCallee() == nil {
		return true
	}
	switch value := common.Value.(type) {
	case *ssa.Function:
		return value.Object() == nil
	case *ssa.MakeClosure, *ssa.FreeVar, *ssa.Parameter, *ssa.Global, *ssa.UnOp, *ssa.Field, *ssa.Index, *ssa.Lookup, *ssa.Phi:
		return true
	default:
		return false
	}
}

func (guard *heterociProbeSSAGuard) allowedProbeStaticClosure(caller *ssa.Function, instruction ssa.Instruction, common *ssa.CallCommon) bool {
	if common == nil || common.StaticCallee() == nil {
		return false
	}
	if _, ok := common.Value.(*ssa.MakeClosure); !ok {
		return false
	}
	if _, ok := instruction.(*ssa.Defer); !ok {
		return false
	}
	callerObject, ok := caller.Object().(*types.Func)
	if !ok || callerObject != guard.receiverMethodObject("probeExternalErrorWalker", "capture") {
		return false
	}
	for _, anonymous := range caller.AnonFuncs {
		if anonymous == common.StaticCallee() {
			return true
		}
	}
	return false
}

func heterociProbeBuiltinCall(value ssa.Value) bool {
	_, ok := value.(*ssa.Builtin)
	return ok
}

func (guard *heterociProbeSSAGuard) probeCallMatrixViolations() []string {
	specs := guard.probeCallMatrixSpecs()
	found := make(map[string]bool)
	for _, spec := range specs {
		if spec.method == nil {
			found[spec.label+" method object is missing"] = true
			continue
		}
		counts := guard.probeMethodCallCounts(spec.method)
		guard.recordProbeCallMatrixDiff(spec, counts, found)
	}
	return sortedHeterociProbeSet(found)
}

func (guard *heterociProbeSSAGuard) probeCallMatrixSpecs() []heterociProbeCallMatrixSpec {
	return []heterociProbeCallMatrixSpec{
		{label: "ProbeTransport.Probe", method: guard.interfaceMethodObject("ProbeTransport", "Probe"), expected: map[string]int{"acquireProbeNode": 1}},
		{label: "ProbeResponseBody.Read", method: guard.interfaceMethodObject("ProbeResponseBody", "Read"), expected: map[string]int{"probeResponseReader.Read": 1}},
		{label: "ProbeResponseBody.Close", method: guard.interfaceMethodObject("ProbeResponseBody", "Close"), expected: map[string]int{"failedProbeTransport": 1, "decodeProbeNode": 2}},
	}
}

func (guard *heterociProbeSSAGuard) probeMethodCallCounts(method *types.Func) map[string]int {
	counts := make(map[string]int)
	for _, function := range guard.probePackageFunctions() {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common().Method != method {
					continue
				}
				counts[heterociProbeFunctionLabel(function)]++
			}
		}
	}
	return counts
}

func (guard *heterociProbeSSAGuard) recordProbeCallMatrixDiff(spec heterociProbeCallMatrixSpec, counts map[string]int, found map[string]bool) {
	for caller, want := range spec.expected {
		if got := counts[caller]; got != want {
			found[fmt.Sprintf("%s caller %s sites = %d, want %d", spec.label, caller, got, want)] = true
		}
	}
	for caller, got := range counts {
		if _, expected := spec.expected[caller]; !expected {
			found[fmt.Sprintf("%s unexpected caller %s sites = %d", spec.label, caller, got)] = true
		}
	}
}

func (guard *heterociProbeSSAGuard) probePackageFunctions() []*ssa.Function {
	functions := make(map[*ssa.Function]bool)
	for function := range ssautil.AllFunctions(guard.built.Prog) {
		if guard.probeLocalFunction(function) {
			functions[function] = true
		}
	}
	if method := guard.probeMethodFunction("probeResponseReader", "Read"); method != nil {
		functions[method] = true
	}
	result := make([]*ssa.Function, 0, len(functions))
	for function := range functions {
		if function != nil && len(function.Blocks) > 0 {
			result = append(result, function)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].String() < result[right].String() })
	return result
}

func (guard *heterociProbeSSAGuard) probeLocalFunction(function *ssa.Function) bool {
	if function == nil {
		return false
	}
	if function.Pkg == guard.built {
		return true
	}
	object, ok := function.Object().(*types.Func)
	return ok && object.Pkg() == guard.pkg.Types
}

func (guard *heterociProbeSSAGuard) probeMethodFunction(typeName, methodName string) *ssa.Function {
	typeObject, ok := guard.pkg.Types.Scope().Lookup(typeName).(*types.TypeName)
	if !ok {
		return nil
	}
	selection := types.NewMethodSet(typeObject.Type()).Lookup(guard.pkg.Types, methodName)
	if selection == nil || selection.Kind() != types.MethodVal {
		return nil
	}
	return guard.built.Prog.MethodValue(selection)
}

func heterociProbeFunctionLabel(function *ssa.Function) string {
	object, ok := function.Object().(*types.Func)
	if !ok {
		return function.String()
	}
	signature, ok := object.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return object.Name()
	}
	receiver := heterociProbeDereference(signature.Recv().Type())
	if named, ok := receiver.(*types.Named); ok {
		return named.Obj().Name() + "." + object.Name()
	}
	return object.Name()
}

func heterociProbeMethodLabel(method *types.Func) string {
	if method == nil {
		return "<unknown>"
	}
	return method.FullName()
}

func TestHeterociProbeDynamicBoundaryCounterexamples(t *testing.T) {
	for _, fixture := range heterociProbeDynamicFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			guard, _ := newHeterociProbeSyntheticGuard(t, fixture.source, "")
			got := strings.Join(guard.dynamicCallViolations(), "; ")
			assertHeterociProbeSubstrings(t, got, fixture.want, true)
		})
	}
}

func heterociProbeDynamicFixtures() []heterociProbeDynamicFixture {
	return []heterociProbeDynamicFixture{
		{name: "parameter function value", source: `package heteroci
			var currentBook = 1
			func AcquireSnapshot() { invoke(readCurrentBook) }
			func invoke(fn func()) { fn() }
			func readCurrentBook() { _ = currentBook }`, want: []string{"unresolved dynamic call"}},
		{name: "free variable closure", source: `package heteroci
			var currentBook = 1
			func AcquireSnapshot() {
				var run func()
				run = func() { _ = currentBook; run() }
				func() { run() }()
			}`, want: []string{"unresolved dynamic call"}},
		{name: "global function value", source: `package heteroci
			var currentBook = 1
			var hidden = func() { _ = currentBook }
			func AcquireSnapshot() { hidden() }`, want: []string{"unresolved dynamic call"}},
		{name: "local function value", source: `package heteroci
			var currentBook = 1
			func AcquireSnapshot() { hidden := func() { _ = currentBook }; hidden() }`, want: []string{"unresolved dynamic call"}},
		{name: "slice function value", source: `package heteroci
			var currentBook = 1
			func AcquireSnapshot() { hidden := []func(){func() { _ = currentBook }}; hidden[0]() }`, want: []string{"unresolved dynamic call"}},
		{name: "struct function value", source: `package heteroci
			type holder struct { run func() }
			var currentBook = 1
			func AcquireSnapshot() { hidden := holder{run: func() { _ = currentBook }}; hidden.run() }`, want: []string{"unresolved dynamic call"}},
		{name: "method value", source: `package heteroci
			type worker struct{}
			var currentBook = 1
			func (worker) hidden() { _ = currentBook }
			func AcquireSnapshot() { hidden := worker{}.hidden; hidden() }`, want: []string{"unresolved dynamic call"}},
	}
}

func TestHeterociProbeStaticCallMatrixCounterexamples(t *testing.T) {
	for _, fixture := range heterociProbeMatrixFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			guard, _ := newHeterociProbeSyntheticGuard(t, fixture.source, "")
			got := strings.Join(guard.probeCallMatrixViolations(), "; ")
			assertHeterociProbeSubstrings(t, got, fixture.want, true)
		})
	}
}

func heterociProbeMatrixFixtures() []heterociProbeDynamicFixture {
	const preamble = `package heteroci
		type ProbeResponseBody interface { Read([]byte) (int, error); Close() error }
		type ProbeTransport interface { Probe(...interface{}) (ProbeResponseBody, error) }
	`
	return []heterociProbeDynamicFixture{
		{name: "second Probe caller", source: preamble + `
			func AcquireSnapshot(transport ProbeTransport) { extraProbe(transport) }
			func extraProbe(transport ProbeTransport) { _, _ = transport.Probe(nil) }`, want: []string{"ProbeTransport.Probe unexpected caller extraProbe"}},
		{name: "extra Read caller", source: preamble + `
			func AcquireSnapshot(body ProbeResponseBody) { extraRead(body) }
			func extraRead(body ProbeResponseBody) { _, _ = body.Read(nil) }`, want: []string{"ProbeResponseBody.Read unexpected caller extraRead"}},
		{name: "extra Close caller", source: preamble + `
			func AcquireSnapshot(body ProbeResponseBody) { extraClose(body) }
			func extraClose(body ProbeResponseBody) { _ = body.Close() }`, want: []string{"ProbeResponseBody.Close unexpected caller extraClose"}},
	}
}
