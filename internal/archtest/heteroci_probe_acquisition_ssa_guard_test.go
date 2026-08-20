package archtest

import (
	"fmt"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
)

const heterociProbePackagePath = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"

type heterociProbeSSAGuard struct {
	root  string
	pkg   *packages.Package
	built *ssa.Package
}

// TestHeterociProbeAcquisitionSSAGuard 以 typed SSA 冻结采集闭包与容量扣减 owner。
func TestHeterociProbeAcquisitionSSAGuard(t *testing.T) {
	root := findRepoRoot(t)
	guard, err := loadHeterociProbeSSAGuard(root, "./internal/devtools/heteroci", heterociProbePackagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.validateAcquireSignature(); err != nil {
		t.Fatal(err)
	}
	if violations := guard.acquireViolations(); len(violations) != 0 {
		t.Fatalf("AcquireSnapshot typed SSA closure owns routing reservations: %s", strings.Join(violations, "; "))
	}
	if violations := guard.externalErrorSnapshotViolations(); len(violations) != 0 {
		t.Fatalf("AcquireSnapshot external error snapshot violations: %s", strings.Join(violations, "; "))
	}
	wantOwner := filepath.Join(root, "internal", "devtools", "heteroci", "routing_lease_book.go")
	if err := guard.validateEffectiveRoutingOwner(wantOwner); err != nil {
		t.Fatal(err)
	}
}

func loadHeterociProbeSSAGuard(root, pattern, packagePath string) (*heterociProbeSSAGuard, error) {
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{pattern},
		Tests:    false,
		LoadMode: packages.LoadSyntax,
		Include:  func(pkg *packages.Package) bool { return pkg.PkgPath == packagePath },
	})
	if err != nil {
		return nil, fmt.Errorf("load heteroci probe package: %w", err)
	}
	_, built, err := ssaload.Build(loaded)
	if err != nil {
		return nil, fmt.Errorf("build heteroci probe SSA: %w", err)
	}
	if len(loaded) != 1 || len(built) != 1 {
		return nil, fmt.Errorf("heteroci probe package count = loaded %d, built %d; want one each", len(loaded), len(built))
	}
	return &heterociProbeSSAGuard{root: root, pkg: loaded[0], built: built[0]}, nil
}

func (guard *heterociProbeSSAGuard) validateAcquireSignature() error {
	wantParams := []string{"context.Context", "Manifest", "RoutingOwnerEpoch", "ProbeTransport", "time.Time", "time.Duration"}
	wantResults := []string{"SnapshotSet", "error"}
	return guard.validateTopLevelSignature("AcquireSnapshot", wantParams, wantResults)
}

func (guard *heterociProbeSSAGuard) validateTopLevelSignature(name string, wantParams, wantResults []string) error {
	object, ok := guard.pkg.Types.Scope().Lookup(name).(*types.Func)
	if !ok {
		return fmt.Errorf("package-scope function %s is missing", name)
	}
	signature, ok := object.Type().(*types.Signature)
	if !ok || signature.Recv() != nil || signature.Variadic() {
		return fmt.Errorf("%s must be a non-variadic top-level function", name)
	}
	params := heterociProbeTupleTypes(signature.Params(), guard.pkg.Types)
	results := heterociProbeTupleTypes(signature.Results(), guard.pkg.Types)
	if !equalHeterociProbeStrings(params, wantParams) || !equalHeterociProbeStrings(results, wantResults) {
		return fmt.Errorf("%s signature = (%s) (%s), want (%s) (%s)", name, strings.Join(params, ", "), strings.Join(results, ", "), strings.Join(wantParams, ", "), strings.Join(wantResults, ", "))
	}
	return nil
}

func heterociProbeTupleTypes(tuple *types.Tuple, local *types.Package) []string {
	result := make([]string, tuple.Len())
	qualifier := func(pkg *types.Package) string {
		if pkg == local {
			return ""
		}
		return pkg.Name()
	}
	for index := range tuple.Len() {
		result[index] = types.TypeString(tuple.At(index).Type(), qualifier)
	}
	return result
}

func equalHeterociProbeStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func (guard *heterociProbeSSAGuard) acquireViolations() []string {
	root := guard.built.Func("AcquireSnapshot")
	if root == nil || root.Object() != guard.pkg.Types.Scope().Lookup("AcquireSnapshot") {
		return []string{"AcquireSnapshot SSA root is missing or is not its package-scope types.Func"}
	}
	violations := make(map[string]bool)
	for _, function := range heterociProbeReachableSSAFunctions(root) {
		guard.recordFunctionViolations(function, violations)
	}
	return sortedHeterociProbeSet(violations)
}

func heterociProbeReachableSSAFunctions(root *ssa.Function) []*ssa.Function {
	queue := []*ssa.Function{root}
	seen := make(map[*ssa.Function]bool)
	result := make([]*ssa.Function, 0)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == nil || seen[current] {
			continue
		}
		seen[current] = true
		result = append(result, current)
		queue = append(queue, heterociProbeStaticCallees(current)...)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].String() < result[right].String() })
	return result
}

func heterociProbeStaticCallees(function *ssa.Function) []*ssa.Function {
	found := make(map[*ssa.Function]bool)
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			caller, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			for _, callee := range heterociProbeCallTargets(caller.Common().Value, caller.Common().StaticCallee()) {
				found[callee] = true
			}
		}
	}
	result := make([]*ssa.Function, 0, len(found))
	for function := range found {
		result = append(result, function)
	}
	return result
}

func heterociProbeCallTargets(value ssa.Value, direct *ssa.Function) []*ssa.Function {
	if direct != nil {
		return []*ssa.Function{direct}
	}
	seen := make(map[ssa.Value]bool)
	var targets []*ssa.Function
	var visit func(ssa.Value)
	visit = func(current ssa.Value) {
		if current == nil || seen[current] {
			return
		}
		seen[current] = true
		switch current := current.(type) {
		case *ssa.Function:
			targets = append(targets, current)
		case *ssa.MakeClosure:
			visit(current.Fn)
		case *ssa.ChangeType:
			visit(current.X)
		case *ssa.Convert:
			visit(current.X)
		case *ssa.Phi:
			for _, edge := range current.Edges {
				visit(edge)
			}
		}
	}
	visit(value)
	return targets
}

func (guard *heterociProbeSSAGuard) recordFunctionViolations(function *ssa.Function, found map[string]bool) {
	if heterociProbeTypeContainsNamed(function.Signature, guard.pkg.Types, "RoutingLeaseBook") {
		found[function.String()+" uses RoutingLeaseBook"] = true
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			guard.recordInstructionViolations(function, instruction, found)
		}
	}
}

func (guard *heterociProbeSSAGuard) recordInstructionViolations(function *ssa.Function, instruction ssa.Instruction, found map[string]bool) {
	guard.recordCallViolation(function, instruction, found)
	guard.recordSubtractionViolation(function, instruction, found)
	guard.recordOperandViolations(function, instruction, found)
}

func (guard *heterociProbeSSAGuard) recordCallViolation(function *ssa.Function, instruction ssa.Instruction, found map[string]bool) {
	call, ok := instruction.(ssa.CallInstruction)
	if !ok {
		return
	}
	if call.Common().IsInvoke() {
		guard.recordInvokeViolation(function, call.Common(), found)
		return
	}
	if callee := call.Common().StaticCallee(); heterociProbeForbiddenCallee(callee, guard.pkg.Types) {
		found[function.String()+" calls "+callee.Object().Name()] = true
	}
}

func (guard *heterociProbeSSAGuard) recordInvokeViolation(function *ssa.Function, call *ssa.CallCommon, found map[string]bool) {
	method := call.Method
	if guard.allowedProbeInvoke(function, method) {
		return
	}
	methodName := "<unknown>"
	if method != nil {
		methodName = method.FullName()
	}
	found[function.String()+" invokes non-whitelisted interface method "+methodName] = true
}

func (guard *heterociProbeSSAGuard) allowedProbeInvoke(caller *ssa.Function, method *types.Func) bool {
	if method == nil {
		return false
	}
	return guard.allowedProbeBoundaryMethod(method) || guard.allowedContextErr(caller, method) || guard.allowedUnavailableUnwrap(caller, method)
}

func (guard *heterociProbeSSAGuard) allowedProbeBoundaryMethod(method *types.Func) bool {
	allowed := []struct {
		typeName string
		methods  []string
	}{
		{typeName: "ProbeTransport", methods: []string{"Probe"}},
		{typeName: "ProbeResponseBody", methods: []string{"Read", "Close"}},
	}
	for _, spec := range allowed {
		for _, name := range spec.methods {
			if guard.interfaceMethodObject(spec.typeName, name) == method {
				return true
			}
		}
	}
	return false
}

func (guard *heterociProbeSSAGuard) allowedContextErr(caller *ssa.Function, method *types.Func) bool {
	contextPackage := guard.importedPackage("context")
	if contextPackage == nil || guard.packageInterfaceMethodObject(contextPackage, "Context", "Err") != method {
		return false
	}
	return guard.callerIsPackageFunction(caller, "acquireNodeSnapshot", "acquireProbeNode", "decodeProbeNode")
}

func (guard *heterociProbeSSAGuard) allowedUnavailableUnwrap(caller *ssa.Function, method *types.Func) bool {
	capture := guard.receiverMethodObject("probeExternalErrorWalker", "captureUnwrapped")
	if method.Pkg() != guard.pkg.Types || method.Name() != "Unwrap" || capture == nil || caller.Object() != capture {
		return false
	}
	signature, ok := method.Type().(*types.Signature)
	if !ok || signature.Params().Len() != 0 || signature.Results().Len() != 1 {
		return false
	}
	result := signature.Results().At(0).Type()
	errorType := types.Universe.Lookup("error").Type()
	if types.Identical(result, errorType) {
		return true
	}
	slice, ok := result.(*types.Slice)
	return ok && types.Identical(slice.Elem(), errorType)
}

func (guard *heterociProbeSSAGuard) callerIsPackageFunction(caller *ssa.Function, names ...string) bool {
	object, ok := caller.Object().(*types.Func)
	if !ok {
		return false
	}
	for _, name := range names {
		if guard.pkg.Types.Scope().Lookup(name) == object {
			return true
		}
	}
	return false
}

func (guard *heterociProbeSSAGuard) importedPackage(path string) *types.Package {
	for _, imported := range guard.pkg.Types.Imports() {
		if imported.Path() == path {
			return imported
		}
	}
	return nil
}

func (guard *heterociProbeSSAGuard) packageInterfaceMethodObject(owner *types.Package, typeName, methodName string) *types.Func {
	typeObject, ok := owner.Scope().Lookup(typeName).(*types.TypeName)
	if !ok {
		return nil
	}
	selection := types.NewMethodSet(typeObject.Type()).Lookup(owner, methodName)
	if selection == nil {
		return nil
	}
	method, _ := selection.Obj().(*types.Func)
	return method
}

func (guard *heterociProbeSSAGuard) interfaceMethodObject(typeName, methodName string) *types.Func {
	return guard.packageInterfaceMethodObject(guard.pkg.Types, typeName, methodName)
}

func (guard *heterociProbeSSAGuard) recordSubtractionViolation(function *ssa.Function, instruction ssa.Instruction, found map[string]bool) {
	if binary, ok := instruction.(*ssa.BinOp); ok && binary.Op == token.SUB && (guard.isResourceCapacityValue(binary.X, nil) || guard.isResourceCapacityValue(binary.Y, nil)) {
		found[function.String()+" subtracts ResourceCapacity"] = true
	}
}

func (guard *heterociProbeSSAGuard) recordOperandViolations(function *ssa.Function, instruction ssa.Instruction, found map[string]bool) {
	for _, operand := range instruction.Operands(nil) {
		if operand == nil || *operand == nil {
			continue
		}
		if global, ok := (*operand).(*ssa.Global); ok {
			guard.recordMutableGlobal(function, global, found)
		}
		if heterociProbeTypeContainsNamed((*operand).Type(), guard.pkg.Types, "RoutingLeaseBook") {
			found[function.String()+" uses RoutingLeaseBook"] = true
		}
	}
}

func heterociProbeForbiddenCallee(function *ssa.Function, local *types.Package) bool {
	if function == nil {
		return false
	}
	object, ok := function.Object().(*types.Func)
	if !ok || object.Pkg() != local {
		return false
	}
	return map[string]bool{"DeriveLinuxCapacity": true, "effectiveRoutingSnapshot": true, "checkedSubtractReservation": true}[object.Name()]
}

func (guard *heterociProbeSSAGuard) recordMutableGlobal(function *ssa.Function, global *ssa.Global, found map[string]bool) {
	object := global.Object()
	if object == nil || object.Pkg() != guard.pkg.Types || heterociProbeIsErrorType(object.Type()) {
		return
	}
	found[function.String()+" references package mutable state "+object.Name()] = true
}

func heterociProbeIsErrorType(typ types.Type) bool {
	object := types.Universe.Lookup("error")
	return object != nil && types.AssignableTo(typ, object.Type())
}

func heterociProbeTypeContainsNamed(typ types.Type, local *types.Package, want string) bool {
	search := heterociProbeNamedTypeSearch{local: local, want: want, seen: make(map[types.Type]bool)}
	return search.contains(typ)
}

type heterociProbeNamedTypeSearch struct {
	local *types.Package
	want  string
	seen  map[types.Type]bool
}

func (search *heterociProbeNamedTypeSearch) contains(current types.Type) bool {
	if current == nil || search.seen[current] {
		return false
	}
	search.seen[current] = true
	return search.containsFresh(current)
}

func (search *heterociProbeNamedTypeSearch) containsFresh(current types.Type) bool {
	switch current := current.(type) {
	case *types.Named:
		return search.namedMatches(current)
	case *types.Pointer:
		return search.contains(current.Elem())
	case *types.Signature:
		return search.signatureContains(current)
	case *types.Tuple:
		return search.tupleContains(current)
	case *types.Map:
		return search.contains(current.Key()) || search.contains(current.Elem())
	case *types.Slice:
		return search.contains(current.Elem())
	case *types.Array:
		return search.contains(current.Elem())
	default:
		return false
	}
}

func (search *heterociProbeNamedTypeSearch) namedMatches(named *types.Named) bool {
	return named.Obj().Pkg() == search.local && named.Obj().Name() == search.want || search.contains(named.Underlying())
}

func (search *heterociProbeNamedTypeSearch) signatureContains(signature *types.Signature) bool {
	if signature.Recv() != nil && search.contains(signature.Recv().Type()) {
		return true
	}
	return search.contains(signature.Params()) || search.contains(signature.Results())
}

func (search *heterociProbeNamedTypeSearch) tupleContains(tuple *types.Tuple) bool {
	for variable := range tuple.Variables() {
		if search.contains(variable.Type()) {
			return true
		}
	}
	return false
}

func (guard *heterociProbeSSAGuard) isResourceCapacityValue(value ssa.Value, seen map[ssa.Value]bool) bool {
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
	return guard.isFreshResourceCapacityValue(value, seen)
}

func (guard *heterociProbeSSAGuard) isFreshResourceCapacityValue(value ssa.Value, seen map[ssa.Value]bool) bool {
	switch value := value.(type) {
	case *ssa.Field:
		return guard.isResourceCapacityField(value.X.Type(), value.Field)
	case *ssa.FieldAddr:
		return guard.isResourceCapacityField(value.X.Type(), value.Field)
	case *ssa.UnOp:
		return guard.isResourceCapacityValue(value.X, seen)
	case *ssa.ChangeType:
		return guard.isResourceCapacityValue(value.X, seen)
	case *ssa.Convert:
		return guard.isResourceCapacityValue(value.X, seen)
	case *ssa.Phi:
		return guard.resourceCapacityPhi(value, seen)
	}
	return false
}

func (guard *heterociProbeSSAGuard) resourceCapacityPhi(phi *ssa.Phi, seen map[ssa.Value]bool) bool {
	for _, edge := range phi.Edges {
		if guard.isResourceCapacityValue(edge, seen) {
			return true
		}
	}
	return false
}

func (guard *heterociProbeSSAGuard) isResourceCapacityField(container types.Type, index int) bool {
	container = heterociProbeDereference(container)
	named, ok := container.(*types.Named)
	if !ok || named.Obj().Pkg() != guard.pkg.Types || named.Obj().Name() != "ResourceCapacity" {
		return false
	}
	structure, ok := named.Underlying().(*types.Struct)
	return ok && index >= 0 && index < structure.NumFields()
}

func heterociProbeDereference(typ types.Type) types.Type {
	for {
		pointer, ok := typ.(*types.Pointer)
		if !ok {
			return typ
		}
		typ = pointer.Elem()
	}
}

func (guard *heterociProbeSSAGuard) validateEffectiveRoutingOwner(wantPath string) error {
	if err := guard.validateTopLevelSignature("effectiveRoutingSnapshot", []string{"SnapshotSet", "map[nodeReservationKey]ResourceCapacity"}, []string{"SnapshotSet", "error"}); err != nil {
		return err
	}
	object := guard.pkg.Types.Scope().Lookup("effectiveRoutingSnapshot")
	position := guard.pkg.Fset.Position(object.Pos())
	if filepath.Clean(position.Filename) != filepath.Clean(wantPath) {
		return fmt.Errorf("effectiveRoutingSnapshot owner = %s, want exact %s", position.Filename, wantPath)
	}
	function := guard.built.Func("effectiveRoutingSnapshot")
	call := guard.uniqueCheckedSubtractCall(function)
	if call == nil {
		return fmt.Errorf("effectiveRoutingSnapshot must call checkedSubtractReservation exactly once")
	}
	if !heterociProbeCallHasNonzeroReservationGuard(call) {
		return fmt.Errorf("effectiveRoutingSnapshot checked subtraction is not dominated by a non-zero reservation guard")
	}
	store := heterociProbeCheckedCallAvailableStore(call, guard.pkg.Types)
	if store == nil {
		return fmt.Errorf("effectiveRoutingSnapshot does not store checked subtraction into NodeSnapshot.Available")
	}
	if !heterociProbeStoreFeedsSuccessfulReturns(function, store) {
		return fmt.Errorf("effectiveRoutingSnapshot checked subtraction does not update the same SnapshotSet returned on success")
	}
	if err := guard.validateEffectiveDirectEffects(function); err != nil {
		return err
	}
	return nil
}

func (guard *heterociProbeSSAGuard) validateEffectiveDirectEffects(function *ssa.Function) error {
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if err := guard.validateEffectiveInstruction(instruction); err != nil {
				return err
			}
		}
	}
	return nil
}

func (guard *heterociProbeSSAGuard) validateEffectiveInstruction(instruction ssa.Instruction) error {
	if err := guard.validateEffectiveSubtraction(instruction); err != nil {
		return err
	}
	return guard.validateEffectiveCall(instruction)
}

func (guard *heterociProbeSSAGuard) validateEffectiveSubtraction(instruction ssa.Instruction) error {
	if binary, ok := instruction.(*ssa.BinOp); ok && binary.Op == token.SUB && (guard.isResourceCapacityValue(binary.X, nil) || guard.isResourceCapacityValue(binary.Y, nil)) {
		return fmt.Errorf("effectiveRoutingSnapshot performs unchecked ResourceCapacity subtraction")
	}
	return nil
}

func (guard *heterociProbeSSAGuard) validateEffectiveCall(instruction ssa.Instruction) error {
	call, ok := instruction.(ssa.CallInstruction)
	if !ok {
		return nil
	}
	if call.Common().IsInvoke() {
		return fmt.Errorf("effectiveRoutingSnapshot invokes an interface method")
	}
	callee := call.Common().StaticCallee()
	if callee == nil {
		if _, builtin := call.Common().Value.(*ssa.Builtin); !builtin {
			return fmt.Errorf("effectiveRoutingSnapshot contains an unresolved dynamic call")
		}
		return nil
	}
	if object, ok := callee.Object().(*types.Func); ok && object.Pkg() == guard.pkg.Types && object.Name() != "cloneRoutingSnapshotSet" && object.Name() != "checkedSubtractReservation" {
		return fmt.Errorf("effectiveRoutingSnapshot calls unexpected local helper %s", object.Name())
	}
	return nil
}

func (guard *heterociProbeSSAGuard) uniqueCheckedSubtractCall(function *ssa.Function) *ssa.Call {
	if function == nil {
		return nil
	}
	var calls []*ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && heterociProbeFunctionNamed(call.Common().StaticCallee(), guard.pkg.Types, "checkedSubtractReservation") {
				calls = append(calls, call)
			}
		}
	}
	if len(calls) != 1 {
		return nil
	}
	return calls[0]
}

func heterociProbeFunctionNamed(function *ssa.Function, local *types.Package, name string) bool {
	if function == nil {
		return false
	}
	object, ok := function.Object().(*types.Func)
	return ok && object.Pkg() == local && object.Name() == name
}

func heterociProbeCallHasNonzeroReservationGuard(call *ssa.Call) bool {
	if call == nil || len(call.Common().Args) != 2 {
		return false
	}
	reserved := call.Common().Args[1]
	for _, block := range call.Parent().Blocks {
		if !block.Dominates(call.Block()) || len(block.Instrs) == 0 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok || !heterociProbeBranchProvesNonzero(branch, reserved, call.Block()) {
			continue
		}
		return true
	}
	return false
}

func heterociProbeBranchProvesNonzero(branch *ssa.If, reserved ssa.Value, callBlock *ssa.BasicBlock) bool {
	comparison, ok := branch.Cond.(*ssa.BinOp)
	if !ok || comparison.Op != token.EQL && comparison.Op != token.NEQ {
		return false
	}
	other, ok := heterociProbeComparisonOther(comparison, reserved)
	if !ok || !heterociProbeZeroValue(other) || len(branch.Block().Succs) != 2 {
		return false
	}
	nonzeroSuccessor := branch.Block().Succs[1]
	if comparison.Op == token.NEQ {
		nonzeroSuccessor = branch.Block().Succs[0]
	}
	return nonzeroSuccessor.Dominates(callBlock)
}

func heterociProbeComparisonOther(comparison *ssa.BinOp, value ssa.Value) (ssa.Value, bool) {
	if comparison.X == value {
		return comparison.Y, true
	}
	if comparison.Y == value {
		return comparison.X, true
	}
	return nil, false
}

func heterociProbeZeroValue(value ssa.Value) bool {
	constant, ok := value.(*ssa.Const)
	return ok && constant.Value == nil
}

func heterociProbeCheckedCallAvailableStore(call *ssa.Call, local *types.Package) *ssa.Store {
	for _, reference := range heterociProbeReferences(call) {
		extract, ok := reference.(*ssa.Extract)
		if !ok || extract.Index != 0 {
			continue
		}
		for _, use := range heterociProbeReferences(extract) {
			store, ok := use.(*ssa.Store)
			if ok && heterociProbeFieldAddrNamed(store.Addr, local, "NodeSnapshot", "Available") {
				return store
			}
		}
	}
	return nil
}

func heterociProbeStoreFeedsSuccessfulReturns(function *ssa.Function, store *ssa.Store) bool {
	storeRoots := heterociProbeSSAMemoryRoots(store.Addr)
	successReturns := 0
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			returned, ok := instruction.(*ssa.Return)
			if !ok || !heterociProbeSuccessfulReturn(returned) {
				continue
			}
			successReturns++
			if !heterociProbeBlockReaches(store.Block(), returned.Block()) || !heterociProbeSetsIntersect(storeRoots, heterociProbeSSAMemoryRoots(returned.Results[0])) {
				return false
			}
		}
	}
	return successReturns > 0
}

func heterociProbeSuccessfulReturn(returned *ssa.Return) bool {
	if returned == nil || len(returned.Results) != 2 {
		return false
	}
	constant, ok := returned.Results[1].(*ssa.Const)
	return ok && constant.Value == nil
}

func heterociProbeSSAMemoryRoots(value ssa.Value) map[ssa.Value]bool {
	result := make(map[ssa.Value]bool)
	heterociProbeCollectSSAMemoryRoots(value, result, make(map[ssa.Value]bool))
	return result
}

func heterociProbeCollectSSAMemoryRoots(value ssa.Value, roots, seen map[ssa.Value]bool) {
	if value == nil || seen[value] {
		return
	}
	seen[value] = true
	if base, ok := heterociProbeSSAUnaryMemoryBase(value); ok {
		heterociProbeCollectSSAMemoryRoots(base, roots, seen)
		return
	}
	if phi, ok := value.(*ssa.Phi); ok {
		for _, edge := range phi.Edges {
			heterociProbeCollectSSAMemoryRoots(edge, roots, seen)
		}
		return
	}
	roots[value] = true
}

func heterociProbeSSAUnaryMemoryBase(value ssa.Value) (ssa.Value, bool) {
	switch value := value.(type) {
	case *ssa.FieldAddr:
		return value.X, true
	case *ssa.IndexAddr:
		return value.X, true
	case *ssa.Slice:
		return value.X, true
	case *ssa.UnOp:
		return value.X, true
	case *ssa.ChangeType:
		return value.X, true
	case *ssa.Convert:
		return value.X, true
	default:
		return nil, false
	}
}

func heterociProbeBlockReaches(start, target *ssa.BasicBlock) bool {
	queue := []*ssa.BasicBlock{start}
	seen := make(map[*ssa.BasicBlock]bool)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target {
			return true
		}
		if current == nil || seen[current] {
			continue
		}
		seen[current] = true
		queue = append(queue, current.Succs...)
	}
	return false
}

func heterociProbeSetsIntersect(left, right map[ssa.Value]bool) bool {
	for value := range left {
		if right[value] {
			return true
		}
	}
	return false
}

func heterociProbeReferences(value ssa.Value) []ssa.Instruction {
	if value == nil || value.Referrers() == nil {
		return nil
	}
	return *value.Referrers()
}

func heterociProbeFieldAddrNamed(value ssa.Value, local *types.Package, typeName, fieldName string) bool {
	field, ok := value.(*ssa.FieldAddr)
	if !ok {
		return false
	}
	named, ok := heterociProbeDereference(field.X.Type()).(*types.Named)
	if !ok || named.Obj().Pkg() != local || named.Obj().Name() != typeName {
		return false
	}
	structure, ok := named.Underlying().(*types.Struct)
	return ok && field.Field >= 0 && field.Field < structure.NumFields() && structure.Field(field.Field).Name() == fieldName
}

func sortedHeterociProbeSet(found map[string]bool) []string {
	result := make([]string, 0, len(found))
	for value := range found {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
