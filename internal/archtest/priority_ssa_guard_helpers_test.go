package archtest_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func priorityInstructions(fn *ssa.Function) []ssa.Instruction {
	if fn == nil {
		return nil
	}
	var out []ssa.Instruction
	for _, block := range fn.Blocks {
		out = append(out, block.Instrs...)
	}
	return out
}

func priorityCallResultIgnored(call *ssa.Call) bool {
	if call == nil || !priorityTypeHasResult(call.Type()) {
		return false
	}
	refs := call.Referrers()
	return refs == nil || len(*refs) == 0
}

func priorityTypeHasResult(typ types.Type) bool {
	if typ == nil {
		return false
	}
	if tuple, ok := typ.(*types.Tuple); ok {
		return tuple.Len() > 0
	}
	return true
}

func priorityIgnoredReturnCallee(call *ssa.CallCommon) (string, bool) {
	name, pkgPath := priorityCallNameAndPackage(call)
	switch name {
	case "Fire", "FireCtx":
		return name, strings.Contains(pkgPath, "stateless")
	case "Subscribe":
		if pkgPath == "github.com/kelindar/event" || priorityReturnsContextCancelFunc(call.Signature()) {
			return priorityDisplayCallName(call, name), true
		}
	}
	return "", false
}

func priorityContextCancelCall(call *ssa.CallCommon) (string, bool) {
	name, pkgPath := priorityCallNameAndPackage(call)
	if pkgPath != "context" || !strings.HasPrefix(name, "With") {
		return "", false
	}
	_, ok := priorityContextCancelResultIndex(call.Signature())
	return name, ok
}

func priorityContextCancelResultIndex(sig *types.Signature) (int, bool) {
	if sig == nil || sig.Results() == nil {
		return 0, false
	}
	for index := 0; index < sig.Results().Len(); index++ {
		result := sig.Results().At(index)
		if priorityIsContextCancelType(result.Type()) {
			return index, true
		}
	}
	return 0, false
}

func priorityIsContextCancelType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch typ.String() {
	case "context.CancelFunc", "context.CancelCauseFunc":
		return true
	default:
		return false
	}
}

func priorityContextCancelResultDiscarded(call *ssa.Call) bool {
	if call == nil {
		return false
	}
	_, ok := priorityContextCancelCall(&call.Call)
	if !ok {
		return false
	}
	cancelIndex, ok := priorityContextCancelResultIndex(call.Call.Signature())
	if !ok {
		return false
	}
	refs := call.Referrers()
	if refs == nil {
		return true
	}
	for _, ref := range *refs {
		extract, ok := ref.(*ssa.Extract)
		if !ok || extract.Index != cancelIndex {
			continue
		}
		return !priorityValueHasMeaningfulUse(extract, map[ssa.Value]bool{})
	}
	return true
}

func priorityRawSQLCall(call *ssa.CallCommon) (string, bool) {
	name, _ := priorityCallNameAndPackage(call)
	switch name {
	case "Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext":
	default:
		return "", false
	}
	receiver := priorityCallReceiver(call)
	if receiver == nil {
		return "", false
	}
	if priorityIsDatabaseSQLType(receiver.Type()) {
		return name, true
	}
	return "", false
}

func priorityErrorStringMatchCall(call *ssa.CallCommon) (string, bool) {
	name, pkgPath := priorityCallNameAndPackage(call)
	if pkgPath != "strings" || !priorityStringMatchName(name) || len(call.Args) == 0 {
		return "", false
	}
	if priorityValueComesFromErrorString(call.Args[0], map[ssa.Value]bool{}) {
		return "strings." + name, true
	}
	return "", false
}

func priorityStringMatchName(name string) bool {
	switch name {
	case "Contains", "ContainsAny", "ContainsRune", "EqualFold", "HasPrefix", "HasSuffix":
		return true
	default:
		return false
	}
}

func priorityValueComesFromErrorString(value ssa.Value, seen map[ssa.Value]bool) bool {
	return priorityValueComesFromErrorStringWithFuncs(value, seen, map[*ssa.Function]bool{})
}

func priorityValueComesFromErrorStringWithFuncs(
	value ssa.Value,
	seenValues map[ssa.Value]bool,
	seenFuncs map[*ssa.Function]bool,
) bool {
	if value == nil || seenValues[value] {
		return false
	}
	seenValues[value] = true
	switch typed := value.(type) {
	case *ssa.Call:
		return priorityIsErrorMethodCall(&typed.Call) ||
			priorityIsErrorFormattingCall(&typed.Call) ||
			priorityFunctionReturnsErrorString(typed.Call.StaticCallee(), seenFuncs)
	case *ssa.ChangeInterface:
		return priorityValueComesFromErrorStringWithFuncs(typed.X, seenValues, seenFuncs)
	case *ssa.ChangeType:
		return priorityValueComesFromErrorStringWithFuncs(typed.X, seenValues, seenFuncs)
	case *ssa.Convert:
		return priorityValueComesFromErrorStringWithFuncs(typed.X, seenValues, seenFuncs)
	case *ssa.Phi:
		return priorityPhiComesFromErrorString(typed, seenValues, seenFuncs)
	}
	return false
}

func priorityPhiComesFromErrorString(
	phi *ssa.Phi,
	seenValues map[ssa.Value]bool,
	seenFuncs map[*ssa.Function]bool,
) bool {
	for _, edge := range phi.Edges {
		if priorityValueComesFromErrorStringWithFuncs(edge, seenValues, seenFuncs) {
			return true
		}
	}
	return false
}

func priorityIsErrorFormattingCall(call *ssa.CallCommon) bool {
	name, pkgPath := priorityCallNameAndPackage(call)
	if pkgPath != "fmt" {
		return false
	}
	switch name {
	case "Sprint", "Sprintln":
		return priorityArgsContainError(call.Args)
	case "Sprintf":
		if len(call.Args) < 2 {
			return false
		}
		return priorityArgsContainError(call.Args[1:])
	default:
		return false
	}
}

func priorityArgsContainError(args []ssa.Value) bool {
	for _, arg := range args {
		if priorityValueImplementsError(arg, map[ssa.Value]bool{}) {
			return true
		}
	}
	return false
}

func priorityValueImplementsError(value ssa.Value, seen map[ssa.Value]bool) bool {
	if value == nil || seen[value] {
		return false
	}
	seen[value] = true
	if priorityImplementsError(value.Type()) {
		return true
	}
	if unwrapped := priorityUnwrapErrorCarrier(value); unwrapped != nil {
		return priorityValueImplementsError(unwrapped, seen)
	}
	switch typed := value.(type) {
	case *ssa.Alloc:
		return priorityAggregateStoresError(typed, seen)
	case *ssa.IndexAddr:
		return priorityAggregateStoresError(typed, seen)
	case *ssa.Phi:
		return priorityPhiCarriesError(typed, seen)
	}
	return false
}

func priorityUnwrapErrorCarrier(value ssa.Value) ssa.Value {
	switch typed := value.(type) {
	case *ssa.ChangeInterface:
		return typed.X
	case *ssa.ChangeType:
		return typed.X
	case *ssa.Convert:
		return typed.X
	case *ssa.MakeInterface:
		return typed.X
	case *ssa.Slice:
		return typed.X
	default:
		return nil
	}
}

func priorityPhiCarriesError(phi *ssa.Phi, seen map[ssa.Value]bool) bool {
	for _, edge := range phi.Edges {
		if priorityValueImplementsError(edge, seen) {
			return true
		}
	}
	return false
}

func priorityAggregateStoresError(value ssa.Value, seen map[ssa.Value]bool) bool {
	refs := value.Referrers()
	if refs == nil {
		return false
	}
	for _, ref := range *refs {
		switch typed := ref.(type) {
		case *ssa.Store:
			if priorityValueImplementsError(typed.Val, seen) {
				return true
			}
		case *ssa.FieldAddr:
			if priorityAggregateStoresError(typed, seen) {
				return true
			}
		case *ssa.IndexAddr:
			if priorityAggregateStoresError(typed, seen) {
				return true
			}
		}
	}
	return false
}

func priorityFunctionReturnsErrorString(fn *ssa.Function, seen map[*ssa.Function]bool) bool {
	if fn == nil || seen[fn] || !priorityReturnsString(fn.Signature) {
		return false
	}
	seen[fn] = true
	for _, instr := range priorityInstructions(fn) {
		ret, ok := instr.(*ssa.Return)
		if !ok {
			continue
		}
		for _, result := range ret.Results {
			if priorityValueComesFromErrorStringWithFuncs(result, map[ssa.Value]bool{}, seen) {
				return true
			}
		}
	}
	return false
}

func priorityIsErrorMethodCall(call *ssa.CallCommon) bool {
	name, _ := priorityCallNameAndPackage(call)
	if name != "Error" || !priorityReturnsString(call.Signature()) {
		return false
	}
	receiver := priorityCallReceiver(call)
	return receiver != nil && priorityImplementsError(receiver.Type())
}

func priorityIsFXInvokeCall(call *ssa.CallCommon) bool {
	name, pkgPath := priorityCallNameAndPackage(call)
	if name != "Invoke" {
		return false
	}
	if pkgPath == "go.uber.org/fx" {
		return true
	}
	receiver := priorityCallReceiver(call)
	return receiver != nil && strings.HasSuffix(receiver.Type().String(), ".fxAPI")
}

func priorityFunctionSideEffects(fn *ssa.Function, depth int, seen map[*ssa.Function]bool) []string {
	if fn == nil || seen[fn] {
		return nil
	}
	seen[fn] = true
	var out []string
	for _, instr := range priorityInstructions(fn) {
		switch typed := instr.(type) {
		case *ssa.Go:
			out = append(out, "starts goroutine")
		case *ssa.Call:
			out = append(out, priorityCallSideEffectReasons(&typed.Call)...)
			if depth > 0 {
				out = append(out, priorityFunctionSideEffects(typed.Call.StaticCallee(), depth-1, seen)...)
			}
		}
	}
	return priorityDedupeStrings(out)
}

func priorityCallSideEffectReasons(call *ssa.CallCommon) []string {
	name, pkgPath := priorityCallNameAndPackage(call)
	switch {
	case pkgPath == "os/exec" && (name == "Command" || name == "CommandContext"):
		return []string{"calls exec command"}
	case pkgPath == "time" && priorityTimeSideEffectName(name):
		return []string{"sleeps or creates timer"}
	default:
		return nil
	}
}

func priorityTimeSideEffectName(name string) bool {
	switch name {
	case "After", "NewTicker", "Sleep", "Tick":
		return true
	default:
		return false
	}
}

func priorityFunctionValue(value ssa.Value) *ssa.Function {
	switch typed := value.(type) {
	case *ssa.Function:
		return typed
	case *ssa.MakeClosure:
		fn, _ := typed.Fn.(*ssa.Function)
		return fn
	case *ssa.MakeInterface:
		return priorityFunctionValue(typed.X)
	case *ssa.ChangeInterface:
		return priorityFunctionValue(typed.X)
	case *ssa.ChangeType:
		return priorityFunctionValue(typed.X)
	case *ssa.Convert:
		return priorityFunctionValue(typed.X)
	default:
		return nil
	}
}

func priorityOnStartFunctionTargets(pkg *orchestrationServiceCheckedPackage) []priorityFunctionTarget {
	if pkg == nil {
		return nil
	}
	var targets []priorityFunctionTarget
	for _, file := range pkg.syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			kv, ok := node.(*ast.KeyValueExpr)
			if !ok || exprTypeString(kv.Key) != "OnStart" {
				return true
			}
			targets = append(targets, priorityFunctionTargetFromExpr(pkg, kv.Value)...)
			return true
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].pos == targets[j].pos {
			return targets[i].label < targets[j].label
		}
		return targets[i].pos < targets[j].pos
	})
	return targets
}

func priorityFunctionTargetFromExpr(pkg *orchestrationServiceCheckedPackage, expr ast.Expr) []priorityFunctionTarget {
	switch typed := expr.(type) {
	case *ast.Ident:
		if typed.Name == "nil" {
			return nil
		}
		target := priorityFunctionTarget{
			label: typed.Name,
			name:  typed.Name,
			pos:   typed.Pos(),
		}
		if obj, ok := pkg.typesInfo.Uses[typed].(*types.Func); ok {
			target.funcPos = obj.Pos()
		}
		return []priorityFunctionTarget{target}
	case *ast.FuncLit:
		return []priorityFunctionTarget{{
			label:   "func literal",
			name:    "func literal",
			pos:     typed.Type.Func,
			funcPos: typed.Type.Func,
		}}
	case *ast.SelectorExpr:
		label := typed.Sel.Name
		target := priorityFunctionTarget{
			label: label,
			name:  label,
			pos:   typed.Sel.Pos(),
		}
		if selection := pkg.typesInfo.Selections[typed]; selection != nil {
			if obj, ok := selection.Obj().(*types.Func); ok {
				target.funcPos = obj.Pos()
				target.recvType = selection.Recv()
				target.methodPkg = obj.Pkg()
			}
		} else if obj, ok := pkg.typesInfo.Uses[typed.Sel].(*types.Func); ok {
			target.funcPos = obj.Pos()
			target.methodPkg = obj.Pkg()
		}
		return []priorityFunctionTarget{target}
	default:
		return nil
	}
}

func priorityFunctionsByName(ssaPkg *ssa.Package) map[string]*ssa.Function {
	out := map[string]*ssa.Function{}
	for _, fn := range orchestrationServiceSSAFunctions(ssaPkg) {
		if fn.Parent() == nil {
			out[fn.Name()] = fn
			out[priorityCleanFunctionValueName(fn.Name())] = fn
		}
	}
	return out
}

func priorityCleanFunctionValueName(name string) string {
	name = strings.TrimSuffix(name, "$bound")
	name = strings.TrimSuffix(name, "$thunk")
	if index := strings.LastIndex(name, "."); index >= 0 && index+1 < len(name) {
		return name[index+1:]
	}
	return name
}

func priorityFunctionsByPos(ssaPkg *ssa.Package) map[token.Pos]*ssa.Function {
	out := map[token.Pos]*ssa.Function{}
	for _, fn := range orchestrationServiceSSAFunctions(ssaPkg) {
		if fn.Pos().IsValid() {
			out[fn.Pos()] = fn
		}
		if obj, ok := fn.Object().(*types.Func); ok && obj.Pos().IsValid() {
			out[obj.Pos()] = fn
		}
	}
	return out
}

func priorityCallNameAndPackage(call *ssa.CallCommon) (string, string) {
	if call == nil {
		return "", ""
	}
	if call.Method != nil {
		return call.Method.Name(), priorityPackagePath(call.Method.Pkg())
	}
	if callee := call.StaticCallee(); callee != nil {
		if obj, ok := callee.Object().(*types.Func); ok {
			return obj.Name(), priorityPackagePath(obj.Pkg())
		}
		return callee.Name(), priorityFunctionPackagePath(callee)
	}
	return "", ""
}

func priorityDisplayCallName(call *ssa.CallCommon, name string) string {
	_, pkgPath := priorityCallNameAndPackage(call)
	if pkgPath == "github.com/kelindar/event" && name == "Subscribe" {
		return "event.Subscribe"
	}
	return name
}

func priorityCallReceiver(call *ssa.CallCommon) ssa.Value {
	if call == nil {
		return nil
	}
	if call.Method != nil {
		return call.Value
	}
	if receiver := priorityBoundMethodReceiver(call); receiver != nil {
		return receiver
	}
	if callee := call.StaticCallee(); callee != nil && callee.Signature != nil && callee.Signature.Recv() != nil && len(call.Args) > 0 {
		return call.Args[0]
	}
	return nil
}

func priorityBoundMethodReceiver(call *ssa.CallCommon) ssa.Value {
	if call == nil {
		return nil
	}
	closure, ok := call.Value.(*ssa.MakeClosure)
	if !ok || len(closure.Bindings) == 0 {
		return nil
	}
	fn, _ := closure.Fn.(*ssa.Function)
	if fn == nil || !priorityFunctionHasReceiver(fn) {
		return nil
	}
	return closure.Bindings[0]
}

func priorityFunctionHasReceiver(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	if fn.Signature != nil && fn.Signature.Recv() != nil {
		return true
	}
	obj, ok := fn.Object().(*types.Func)
	if !ok {
		return false
	}
	sig, ok := obj.Type().(*types.Signature)
	return ok && sig.Recv() != nil
}

func priorityReturnsContextCancelFunc(sig *types.Signature) bool {
	return prioritySignatureReturns(sig, "context.CancelFunc")
}

func priorityReturnsString(sig *types.Signature) bool {
	return prioritySignatureReturns(sig, "string")
}

func prioritySignatureReturns(sig *types.Signature, want string) bool {
	if sig == nil || sig.Results() == nil || sig.Results().Len() != 1 {
		return false
	}
	return sig.Results().At(0).Type().String() == want
}

func priorityIsDatabaseSQLType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	text := typ.String()
	return text == "*database/sql.DB" || text == "*database/sql.Tx" || text == "*database/sql.Conn"
}

func priorityImplementsError(typ types.Type) bool {
	if typ == nil {
		return false
	}
	errorType := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	return types.Implements(typ, errorType)
}

func priorityPackagePath(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}

func priorityFunctionPackagePath(fn *ssa.Function) string {
	if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return ""
	}
	return fn.Pkg.Pkg.Path()
}

func priorityRootBridgeAllowed(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) bool {
	if pkg == nil || fn == nil {
		return false
	}
	relPath, _ := priorityFunctionPosition(pkg, fn)
	switch relPath + "#" + fn.Name() {
	case "internal/app/app.go#BindRuntime",
		"cmd/mcp-orch/fx.go#bindRuntime",
		"cmd/mcp-lsp/fx.go#bindRuntime",
		"cmd/mcp-ida/fx.go#bindRuntime":
		return true
	default:
		return false
	}
}

func priorityFunctionPosition(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) (string, int) {
	if fn == nil {
		return "(unknown)", 0
	}
	return priorityPosition(pkg, fn.Pos())
}

func priorityPositionMessage(pkg *orchestrationServiceCheckedPackage, pos token.Pos, detail string) string {
	relPath, line := priorityPosition(pkg, pos)
	return fmt.Sprintf("%s:%d %s", relPath, line, detail)
}

func priorityPosition(pkg *orchestrationServiceCheckedPackage, pos token.Pos) (string, int) {
	if pkg != nil && pos.IsValid() {
		position := pkg.fset.Position(pos)
		if position.Filename != "" {
			return orchestrationServiceTypeRelPath(position.Filename), position.Line
		}
	}
	return "(unknown)", 0
}

func priorityValueHasMeaningfulUse(value ssa.Value, seen map[ssa.Value]bool) bool {
	if value == nil || seen[value] {
		return false
	}
	seen[value] = true
	refs := value.Referrers()
	if refs == nil {
		return false
	}
	for _, ref := range *refs {
		if priorityInstructionUsesValue(ref, value, seen) {
			return true
		}
	}
	return false
}

func priorityInstructionUsesValue(instr ssa.Instruction, value ssa.Value, seen map[ssa.Value]bool) bool {
	if priorityCallInstructionUsesValue(instr, value) || priorityInstructionExposesValue(instr) {
		return true
	}
	forwarded, ok := instr.(ssa.Value)
	if !ok || !priorityInstructionForwardsValue(forwarded) {
		return false
	}
	return priorityValueHasMeaningfulUse(forwarded, seen)
}

func priorityCallInstructionUsesValue(instr ssa.Instruction, value ssa.Value) bool {
	switch typed := instr.(type) {
	case *ssa.Call:
		return priorityCallUsesValue(&typed.Call, value)
	case *ssa.Defer:
		return priorityCallUsesValue(&typed.Call, value)
	case *ssa.Go:
		return priorityCallUsesValue(&typed.Call, value)
	default:
		return false
	}
}

func priorityInstructionExposesValue(instr ssa.Instruction) bool {
	switch instr.(type) {
	case *ssa.Return, *ssa.Store, *ssa.MakeClosure:
		return true
	default:
		return false
	}
}

func priorityInstructionForwardsValue(value ssa.Value) bool {
	switch value.(type) {
	case *ssa.ChangeInterface, *ssa.ChangeType, *ssa.Convert, *ssa.MakeInterface, *ssa.Phi:
		return true
	default:
		return false
	}
}

func priorityCallUsesValue(call *ssa.CallCommon, value ssa.Value) bool {
	if call == nil || value == nil {
		return false
	}
	return call.Value == value || slices.Contains(call.Args, value)
}

func priorityFunctionForTarget(
	ssaPkg *ssa.Package,
	target priorityFunctionTarget,
	functions map[string]*ssa.Function,
	functionsByPos map[token.Pos]*ssa.Function,
) *ssa.Function {
	if fn := functionsByPos[target.funcPos]; fn != nil {
		return fn
	}
	if fn := functions[target.name]; fn != nil {
		return fn
	}
	if target.recvType == nil || target.name == "" || ssaPkg == nil || ssaPkg.Prog == nil {
		return nil
	}
	return priorityLookupMethod(ssaPkg.Prog, target.recvType, target.methodPkg, target.name)
}

func priorityLookupMethod(
	prog *ssa.Program,
	recvType types.Type,
	methodPkg *types.Package,
	methodName string,
) (fn *ssa.Function) {
	defer func() {
		if recover() != nil {
			fn = nil
		}
	}()
	return prog.LookupMethod(recvType, methodPkg, methodName)
}

func priorityUseMessage(use orchestrationServiceSSAUse, detail string) string {
	location := fmt.Sprintf("%s:%d", use.relPath, use.line)
	context := use.kind
	if use.symbol != "" {
		context += " " + use.symbol
	}
	if use.function != "" {
		context += " in " + use.function
	}
	return location + " " + context + " " + detail
}

func priorityTargetLabel(target *types.TypeName) string {
	if target == nil {
		return "(unknown)"
	}
	if target.Pkg() == nil {
		return target.Name()
	}
	return target.Pkg().Name() + "." + target.Name()
}

func priorityDedupeStrings(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	var last string
	for _, value := range values {
		if value == last {
			continue
		}
		out = append(out, value)
		last = value
	}
	return out
}
