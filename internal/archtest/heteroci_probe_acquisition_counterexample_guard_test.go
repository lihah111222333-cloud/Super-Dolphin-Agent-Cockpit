package archtest

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

type heterociProbeClosureFixture struct {
	name     string
	source   string
	external string
	want     []string
	reject   []string
}

func TestHeterociProbeClosureCounterexamples(t *testing.T) {
	fixtures := append(heterociProbeFunctionClosureFixtures(), heterociProbeScopeAndInvokeFixtures()...)
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			guard, _ := newHeterociProbeSyntheticGuard(t, fixture.source, fixture.external)
			got := strings.Join(guard.acquireViolations(), "; ")
			assertHeterociProbeSubstrings(t, got, fixture.want, true)
			assertHeterociProbeSubstrings(t, got, fixture.reject, false)
		})
	}
}

func heterociProbeFunctionClosureFixtures() []heterociProbeClosureFixture {
	return []heterociProbeClosureFixture{
		{
			name: "exported private and function value closure",
			source: `package heteroci
				var currentBook = 1
				func AcquireSnapshot() { alias := privateHop; alias() }
				func privateHop() { ExportedHop() }
				func ExportedHop() { _ = currentBook }`,
			want: []string{"ExportedHop", "currentBook"},
		},
		{
			name: "outer real function survives inner same name local",
			source: `package heteroci
				var realBook = 1
				func AcquireSnapshot() {
					{ DeductBook := func() {}; DeductBook() }
					DeductBook()
				}
				func DeductBook() { _ = realBook }`,
			want: []string{"DeductBook", "realBook"},
		},
		{
			name: "receiver method reads global and subtracts capacity",
			source: `package heteroci
				type ResourceCapacity struct { CPUMilli int64 }
				type probeWorker struct{}
				var currentBook = 1
				func AcquireSnapshot() { var worker probeWorker; worker.deduct(ResourceCapacity{}, ResourceCapacity{}) }
				func (probeWorker) deduct(available, reserved ResourceCapacity) {
					_ = currentBook
					available.CPUMilli -= reserved.CPUMilli
				}`,
			want: []string{"deduct", "currentBook", "subtracts ResourceCapacity"},
		},
	}
}

func heterociProbeScopeAndInvokeFixtures() []heterociProbeClosureFixture {
	return []heterociProbeClosureFixture{
		{
			name: "local interface invoke cannot hide reservation subtraction",
			source: `package heteroci
				type ResourceCapacity struct { CPUMilli int64 }
				type capacityDeductor interface { deduct(ResourceCapacity, ResourceCapacity) }
				type hiddenDeductor struct{}
				var currentBook = 1
				func AcquireSnapshot() {
					var deductor capacityDeductor = hiddenDeductor{}
					deductor.deduct(ResourceCapacity{}, ResourceCapacity{})
				}
				func (hiddenDeductor) deduct(available, reserved ResourceCapacity) {
					_ = currentBook
					available.CPUMilli -= reserved.CPUMilli
				}`,
			want: []string{"invokes non-whitelisted interface method", "deduct"},
		},
		{
			name: "nested external selector is not local state",
			source: `package heteroci
				import "example/external"
				func AcquireSnapshot() { _ = external.Client.Book.Reservations }`,
			external: `package external
				type book struct { Reservations int }
				type client struct { Book book }
				var Client client`,
			reject: []string{"mutable state", "RoutingLeaseBook", "ResourceCapacity"},
		},
		{
			name: "context Err is rejected outside exact acquisition callers",
			source: `package heteroci
				import "context"
				func AcquireSnapshot() { inspectContext(context.Background()) }
				func inspectContext(ctx context.Context) { _ = ctx.Err() }`,
			want: []string{"invokes non-whitelisted interface method", "Err"},
		},
		{
			name: "Unwrap is rejected outside bounded unavailable helper",
			source: `package heteroci
				func AcquireSnapshot() { inspectUnwrap(nil) }
				func inspectUnwrap(err error) {
					if wrapped, ok := err.(interface{ Unwrap() error }); ok { _ = wrapped.Unwrap() }
				}`,
			want: []string{"invokes non-whitelisted interface method", "Unwrap"},
		},
		{
			name: "local reservations are lexical locals",
			source: `package heteroci
				func AcquireSnapshot() { reservations := 1; _ = reservations }`,
			reject: []string{"reservations", "mutable state"},
		},
	}
}

func TestHeterociProbeEffectiveOwnerRejectsEmptyImplementation(t *testing.T) {
	guard, sourcePath := newHeterociProbeSyntheticGuard(t, `package heteroci
		type ResourceCapacity struct { CPUMilli int64 }
		type SnapshotSet struct{}
		type nodeReservationKey struct{}
		func AcquireSnapshot() {}
		func checkedSubtractReservation(ResourceCapacity, ResourceCapacity) (ResourceCapacity, error) { return ResourceCapacity{}, nil }
		func effectiveRoutingSnapshot(SnapshotSet, map[nodeReservationKey]ResourceCapacity) (SnapshotSet, error) { return SnapshotSet{}, nil }`, "")
	err := guard.validateEffectiveRoutingOwner(sourcePath)
	if err == nil || !strings.Contains(err.Error(), "call checkedSubtractReservation exactly once") {
		t.Fatalf("empty effectiveRoutingSnapshot owner error = %v, want missing checked subtraction", err)
	}
}

func TestHeterociProbeEffectiveOwnerRejectsDummyCheckedStore(t *testing.T) {
	guard, sourcePath := newHeterociProbeSyntheticGuard(t, `package heteroci
		type ResourceCapacity struct { CPUMilli int64 }
		type NodeSnapshot struct { Available ResourceCapacity }
		type SnapshotSet struct { Nodes []NodeSnapshot }
		type nodeReservationKey struct{}
		func AcquireSnapshot() {}
		func cloneRoutingSnapshotSet(source SnapshotSet) SnapshotSet { return source }
		func checkedSubtractReservation(available, reserved ResourceCapacity) (ResourceCapacity, error) {
			available.CPUMilli -= reserved.CPUMilli
			return available, nil
		}
		func deductActual(source SnapshotSet, reserved ResourceCapacity) SnapshotSet {
			source.Nodes[0].Available.CPUMilli -= reserved.CPUMilli
			return source
		}
		func effectiveRoutingSnapshot(source SnapshotSet, current map[nodeReservationKey]ResourceCapacity) (SnapshotSet, error) {
			reserved := current[nodeReservationKey{}]
			if reserved != (ResourceCapacity{}) {
				dummy := cloneRoutingSnapshotSet(source)
				available, err := checkedSubtractReservation(dummy.Nodes[0].Available, reserved)
				if err != nil { return SnapshotSet{}, err }
				dummy.Nodes[0].Available = available
			}
			return deductActual(source, reserved), nil
		}`, "")
	err := guard.validateEffectiveRoutingOwner(sourcePath)
	if err == nil || !strings.Contains(err.Error(), "same SnapshotSet returned on success") {
		t.Fatalf("dummy checked store error = %v, want returned SnapshotSet dataflow rejection", err)
	}
}

func TestHeterociProbeExternalErrorSnapshotCounterexamples(t *testing.T) {
	fixtures := []struct{ name, body, want string }{
		{
			name: "raw error bypasses snapshot",
			body: `type ProbeTransportError struct { Cause error }
				func AcquireSnapshot(transport ProbeTransport) ProbeTransportError {
					_, err := transport.Probe()
					return ProbeTransportError{Cause: err}
				}`,
			want: "before snapshot",
		},
		{
			name: "raw error is observed twice",
			body: `func AcquireSnapshot(transport ProbeTransport) error {
					_, err := transport.Probe()
					if err == nil { return nil }
					_ = snapshotProbeExternalError(err)
					return snapshotProbeExternalError(err).cause
				}`,
			want: "observed by snapshot 2 times",
		},
		{
			name: "raw error enters self helper before snapshot",
			body: `func snapshotViaHelper(err error) probeExternalErrorSnapshot { return snapshotProbeExternalError(err) }
				func AcquireSnapshot(transport ProbeTransport) error {
					_, err := transport.Probe()
					return snapshotViaHelper(err).cause
				}`,
			want: "before snapshot",
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			guard, _ := newHeterociProbeSyntheticGuard(t, heterociProbeExternalErrorPreamble()+fixture.body, "")
			got := strings.Join(guard.rawExternalErrorViolations(), "; ")
			if !strings.Contains(got, fixture.want) {
				t.Fatalf("external error violations = %q, want %q", got, fixture.want)
			}
		})
	}
}

func TestHeterociProbeContextErrorMustEnterSnapshot(t *testing.T) {
	guard, _ := newHeterociProbeSyntheticGuard(t, `package heteroci
		import "context"
		type probeExternalErrorSnapshot struct { cause error; onlyUnavailable bool }
		func snapshotProbeExternalError(err error) probeExternalErrorSnapshot {
			return probeExternalErrorSnapshot{cause: err}
		}
		func AcquireSnapshot(ctx context.Context) error { return acquireNodeSnapshot(ctx) }
		func acquireNodeSnapshot(ctx context.Context) error {
			err := ctx.Err()
			if err != nil { return err }
			return nil
		}`, "")
	got := strings.Join(guard.rawExternalErrorViolations(), "; ")
	if !strings.Contains(got, "context.Context.Err") || !strings.Contains(got, "before snapshot") {
		t.Fatalf("context error violations = %q, want raw error snapshot rejection", got)
	}
}

func heterociProbeExternalErrorPreamble() string {
	return `package heteroci
		type ProbeResponseBody interface { Read([]byte) (int, error); Close() error }
		type ProbeTransport interface { Probe() (ProbeResponseBody, error) }
		type probeExternalErrorSnapshot struct { cause error; onlyUnavailable bool }
		func snapshotProbeExternalError(err error) probeExternalErrorSnapshot {
			return probeExternalErrorSnapshot{cause: err}
		}
	`
}

func newHeterociProbeSyntheticGuard(t *testing.T, source, external string) (*heterociProbeSSAGuard, string) {
	t.Helper()
	root := t.TempDir()
	writeHeterociProbeFixture(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.26\n")
	sourcePath := filepath.Join(root, "heteroci", "fixture.go")
	writeHeterociProbeFixture(t, sourcePath, source)
	if external != "" {
		writeHeterociProbeFixture(t, filepath.Join(root, "external", "external.go"), external)
	}
	guard, err := loadHeterociProbeSSAGuard(root, "./heteroci", "example/heteroci")
	if err != nil {
		t.Fatal(err)
	}
	return guard, sourcePath
}

func writeHeterociProbeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create synthetic probe fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write synthetic probe fixture: %v", err)
	}
}

func assertHeterociProbeSubstrings(t *testing.T, got string, values []string, want bool) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(got, value) != want {
			t.Fatalf("violations = %q, contains %q = %t, want %t", got, value, strings.Contains(got, value), want)
		}
	}
}

func (guard *heterociProbeSSAGuard) receiverMethodObject(typeName, methodName string) *types.Func {
	typeObject, ok := guard.pkg.Types.Scope().Lookup(typeName).(*types.TypeName)
	if !ok {
		return nil
	}
	selection := types.NewMethodSet(types.NewPointer(typeObject.Type())).Lookup(guard.pkg.Types, methodName)
	if selection == nil {
		return nil
	}
	method, _ := selection.Obj().(*types.Func)
	return method
}

type heterociProbeRawErrorSource struct {
	function    *ssa.Function
	value       ssa.Value
	label       string
	allowReturn bool
}

func (guard *heterociProbeSSAGuard) externalErrorSnapshotViolations() []string {
	found := make(map[string]bool)
	guard.recordExternalSnapshotOwnerViolation(found)
	for _, violation := range guard.rawExternalErrorViolations() {
		found[violation] = true
	}
	guard.recordContextErrCallCount(found)
	guard.recordOfflineSnapshotViolation(found)
	return sortedHeterociProbeSet(found)
}

func (guard *heterociProbeSSAGuard) rawExternalErrorViolations() []string {
	root := guard.built.Func("AcquireSnapshot")
	found := make(map[string]bool)
	for _, function := range heterociProbeReachableSSAFunctions(root) {
		for _, source := range guard.rawErrorSources(function) {
			if violation := guard.validateRawErrorSource(source); violation != "" {
				found[violation] = true
			}
		}
	}
	return sortedHeterociProbeSet(found)
}

func (guard *heterociProbeSSAGuard) recordExternalSnapshotOwnerViolation(found map[string]bool) {
	if guard.pkg.PkgPath != heterociProbePackagePath {
		return
	}
	if err := guard.validateTopLevelSignature("snapshotProbeExternalError", []string{"error"}, []string{"probeExternalErrorSnapshot"}); err != nil {
		found[err.Error()] = true
		return
	}
	object := guard.pkg.Types.Scope().Lookup("snapshotProbeExternalError")
	want := filepath.Join(guard.root, "internal", "devtools", "heteroci", "probe_error_snapshot.go")
	if got := guard.pkg.Fset.Position(object.Pos()).Filename; filepath.Clean(got) != filepath.Clean(want) {
		found[fmt.Sprintf("snapshotProbeExternalError owner = %s, want %s", got, want)] = true
	}
}

func (guard *heterociProbeSSAGuard) rawErrorSources(function *ssa.Function) []heterociProbeRawErrorSource {
	var result []heterociProbeRawErrorSource
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			label, boundary := guard.rawErrorCallLabel(call)
			if boundary {
				result = append(result, guard.callErrorSources(function, call, label)...)
			}
		}
	}
	return result
}

func (guard *heterociProbeSSAGuard) rawErrorCallLabel(call *ssa.Call) (string, bool) {
	common := call.Common()
	if common.IsInvoke() {
		for _, spec := range []struct{ owner, method string }{{"ProbeTransport", "Probe"}, {"ProbeResponseBody", "Read"}, {"ProbeResponseBody", "Close"}} {
			if guard.interfaceMethodObject(spec.owner, spec.method) == common.Method {
				return spec.owner + "." + spec.method, true
			}
		}
		if guard.contextErrMethodObject() == common.Method {
			return "context.Context.Err", true
		}
		return "", false
	}
	ioPackage := guard.importedPackage("io")
	callee := common.StaticCallee()
	return "io.ReadAll", ioPackage != nil && callee != nil && callee.Object() == ioPackage.Scope().Lookup("ReadAll")
}

func (guard *heterociProbeSSAGuard) contextErrMethodObject() *types.Func {
	contextPackage := guard.importedPackage("context")
	if contextPackage == nil {
		return nil
	}
	return guard.packageInterfaceMethodObject(contextPackage, "Context", "Err")
}

func (guard *heterociProbeSSAGuard) recordContextErrCallCount(found map[string]bool) {
	expected := map[string]int{"acquireNodeSnapshot": 1, "acquireProbeNode": 1, "decodeProbeNode": 2}
	method := guard.contextErrMethodObject()
	for name, want := range expected {
		function := guard.built.Func(name)
		got := heterociProbeInvokeCount(function, method)
		if got != want {
			found[fmt.Sprintf("%s context.Context.Err calls = %d, want exact %d", name, got, want)] = true
		}
	}
}

func heterociProbeInvokeCount(function *ssa.Function, method *types.Func) int {
	count := 0
	if function == nil || method == nil {
		return count
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if ok && call.Common().IsInvoke() && call.Common().Method == method {
				count++
			}
		}
	}
	return count
}

func (guard *heterociProbeSSAGuard) callErrorSources(function *ssa.Function, call *ssa.Call, label string) []heterociProbeRawErrorSource {
	signature := call.Common().Signature()
	errorType := types.Universe.Lookup("error").Type()
	allowReturn := guard.receiverMethodObject("probeResponseReader", "Read") == function.Object()
	var result []heterociProbeRawErrorSource
	for index := range signature.Results().Len() {
		if !types.Identical(signature.Results().At(index).Type(), errorType) {
			continue
		}
		for _, value := range heterociProbeCallResultValues(call, index, signature.Results().Len()) {
			result = append(result, heterociProbeRawErrorSource{function: function, value: value, label: label, allowReturn: allowReturn})
		}
	}
	return result
}

func heterociProbeCallResultValues(call *ssa.Call, index, count int) []ssa.Value {
	if count == 1 {
		return []ssa.Value{call}
	}
	var result []ssa.Value
	for _, reference := range heterociProbeReferences(call) {
		if extract, ok := reference.(*ssa.Extract); ok && extract.Index == index {
			result = append(result, extract)
		}
	}
	return result
}

func (guard *heterociProbeSSAGuard) validateRawErrorSource(source heterociProbeRawErrorSource) string {
	queue := []ssa.Value{source.value}
	seen := make(map[ssa.Value]bool)
	snapshots := make(map[*ssa.Call]bool)
	returned := false
	for len(queue) > 0 {
		value := queue[0]
		queue = queue[1:]
		if value == nil || seen[value] {
			continue
		}
		seen[value] = true
		violation, next, didReturn := guard.validateRawErrorUses(source, value, snapshots)
		if violation != "" {
			return violation
		}
		returned = returned || didReturn
		queue = append(queue, next...)
	}
	if heterociProbeRawObservationValid(source.allowReturn, returned, len(snapshots)) {
		return ""
	}
	return fmt.Sprintf("%s raw error in %s observed by snapshot %d times", source.label, source.function.String(), len(snapshots))
}

func heterociProbeRawObservationValid(allowReturn, returned bool, snapshots int) bool {
	return allowReturn && returned && snapshots == 0 || !allowReturn && snapshots == 1
}

func (guard *heterociProbeSSAGuard) validateRawErrorUses(source heterociProbeRawErrorSource, value ssa.Value, snapshots map[*ssa.Call]bool) (string, []ssa.Value, bool) {
	var next []ssa.Value
	for _, instruction := range heterociProbeReferences(value) {
		if propagated, ok := heterociProbePropagatedSSAValue(instruction); ok {
			next = append(next, propagated)
			continue
		}
		switch use := instruction.(type) {
		case *ssa.BinOp, *ssa.DebugRef:
			continue
		case *ssa.Return:
			if source.allowReturn {
				return "", next, true
			}
		case *ssa.Call:
			if guard.isSnapshotCall(use) {
				snapshots[use] = true
				continue
			}
		}
		return fmt.Sprintf("%s raw error in %s reaches %T before snapshot", source.label, source.function.String(), instruction), nil, false
	}
	return "", next, false
}

func (guard *heterociProbeSSAGuard) isSnapshotCall(call *ssa.Call) bool {
	callee := call.Common().StaticCallee()
	return callee != nil && callee.Object() == guard.pkg.Types.Scope().Lookup("snapshotProbeExternalError")
}

func (guard *heterociProbeSSAGuard) recordOfflineSnapshotViolation(found map[string]bool) {
	function := guard.built.Func("acquireProbeNode")
	if function == nil {
		found["acquireProbeNode SSA function is missing"] = true
		return
	}
	var probeSources []heterociProbeRawErrorSource
	for _, source := range guard.rawErrorSources(function) {
		if source.label == "ProbeTransport.Probe" {
			probeSources = append(probeSources, source)
		}
	}
	if len(probeSources) != 1 {
		found[fmt.Sprintf("acquireProbeNode Probe raw error sources = %d, want one", len(probeSources))] = true
		return
	}
	snapshots := guard.snapshotCallsForRaw(probeSources[0].value)
	if len(snapshots) != 1 || !guard.snapshotOnlyUnavailableControlsOffline(function, snapshots[0]) {
		found["offline projection must use onlyUnavailable from the same single Probe error snapshot"] = true
	}
}

func (guard *heterociProbeSSAGuard) snapshotCallsForRaw(root ssa.Value) []*ssa.Call {
	queue := []ssa.Value{root}
	seen := make(map[ssa.Value]bool)
	var result []*ssa.Call
	for len(queue) > 0 {
		value := queue[0]
		queue = queue[1:]
		if value == nil || seen[value] {
			continue
		}
		seen[value] = true
		for _, instruction := range heterociProbeReferences(value) {
			if call, ok := instruction.(*ssa.Call); ok && guard.isSnapshotCall(call) {
				result = append(result, call)
				continue
			}
			if propagated, ok := heterociProbePropagatedSSAValue(instruction); ok {
				queue = append(queue, propagated)
			}
		}
	}
	return result
}

func heterociProbePropagatedSSAValue(instruction ssa.Instruction) (ssa.Value, bool) {
	switch value := instruction.(type) {
	case *ssa.ChangeInterface:
		return value, true
	case *ssa.MakeInterface:
		return value, true
	case *ssa.ChangeType:
		return value, true
	case *ssa.Convert:
		return value, true
	case *ssa.Phi:
		return value, true
	default:
		return nil, false
	}
}

func (guard *heterociProbeSSAGuard) snapshotOnlyUnavailableControlsOffline(function *ssa.Function, snapshot *ssa.Call) bool {
	fieldIndex := guard.localStructFieldIndex("probeExternalErrorSnapshot", "onlyUnavailable")
	offline := guard.uniqueDirectCall(function, "offlineProbeSnapshot")
	if fieldIndex < 0 || offline == nil {
		return false
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			field, ok := guard.snapshotFieldRead(function, instruction, fieldIndex, snapshot)
			if ok && heterociProbeValueControlsCall(function, field, offline) {
				return true
			}
		}
	}
	return false
}

func (guard *heterociProbeSSAGuard) snapshotFieldRead(function *ssa.Function, instruction ssa.Instruction, fieldIndex int, snapshot *ssa.Call) (ssa.Value, bool) {
	if field, ok := instruction.(*ssa.Field); ok {
		return field, field.Field == fieldIndex && heterociProbeSSAValueDependsOn(field.X, snapshot, nil)
	}
	load, ok := instruction.(*ssa.UnOp)
	if !ok {
		return nil, false
	}
	address, ok := load.X.(*ssa.FieldAddr)
	if !ok || address.Field != fieldIndex {
		return nil, false
	}
	return load, guard.addressReceivesSnapshot(function, address.X, snapshot)
}

func (guard *heterociProbeSSAGuard) addressReceivesSnapshot(function *ssa.Function, address ssa.Value, snapshot *ssa.Call) bool {
	wantRoots := heterociProbeSSAMemoryRoots(address)
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			store, ok := instruction.(*ssa.Store)
			if ok && heterociProbeSetsIntersect(wantRoots, heterociProbeSSAMemoryRoots(store.Addr)) && heterociProbeSSAValueDependsOn(store.Val, snapshot, nil) {
				return true
			}
		}
	}
	return false
}

func (guard *heterociProbeSSAGuard) localStructFieldIndex(typeName, fieldName string) int {
	typeObject, ok := guard.pkg.Types.Scope().Lookup(typeName).(*types.TypeName)
	if !ok {
		return -1
	}
	structure, ok := typeObject.Type().Underlying().(*types.Struct)
	if !ok {
		return -1
	}
	for index := range structure.NumFields() {
		if structure.Field(index).Name() == fieldName {
			return index
		}
	}
	return -1
}

func (guard *heterociProbeSSAGuard) uniqueDirectCall(function *ssa.Function, name string) *ssa.Call {
	var result []*ssa.Call
	object := guard.pkg.Types.Scope().Lookup(name)
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().StaticCallee() != nil && call.Common().StaticCallee().Object() == object {
				result = append(result, call)
			}
		}
	}
	if len(result) != 1 {
		return nil
	}
	return result[0]
}

func heterociProbeValueControlsCall(function *ssa.Function, value ssa.Value, call *ssa.Call) bool {
	for _, block := range function.Blocks {
		if len(block.Instrs) == 0 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if ok && heterociProbeSSAValueDependsOn(branch.Cond, value, nil) && len(block.Succs) == 2 && block.Succs[0].Dominates(call.Block()) {
			return true
		}
	}
	return false
}

func heterociProbeSSAValueDependsOn(value, target ssa.Value, seen map[ssa.Value]bool) bool {
	if value == target {
		return true
	}
	if value == nil {
		return false
	}
	if seen == nil {
		seen = make(map[ssa.Value]bool)
	}
	if seen[value] {
		return false
	}
	seen[value] = true
	instruction, ok := value.(ssa.Instruction)
	if !ok {
		return false
	}
	for _, operand := range instruction.Operands(nil) {
		if operand != nil && heterociProbeSSAValueDependsOn(*operand, target, seen) {
			return true
		}
	}
	return false
}
