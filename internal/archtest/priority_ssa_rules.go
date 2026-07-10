package archtest

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

type prioritySSAFunctionTarget struct {
	label     string
	name      string
	pos       token.Pos
	funcPos   token.Pos
	recvType  types.Type
	methodPkg *types.Package
}

func collectPrioritySSAIgnoredReturnViolations(pkg *prioritySSAPackage, fn *ssa.Function) []PrioritySSAViolation {
	var violations []PrioritySSAViolation
	for _, instr := range prioritySSAInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok || !prioritySSACallResultIgnored(call) {
			continue
		}
		name, ok := prioritySSAIgnoredReturnCallee(&call.Call)
		if ok {
			violations = append(violations, prioritySSAViolation(pkg, call.Pos(), PrioritySSAIgnoredReturnRule, "ignored return from "+name))
		}
	}
	return violations
}

func collectPrioritySSAContextCancelViolations(pkg *prioritySSAPackage, fn *ssa.Function) []PrioritySSAViolation {
	var violations []PrioritySSAViolation
	for _, instr := range prioritySSAInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok || !prioritySSAContextCancelResultDiscarded(call) {
			continue
		}
		name, _ := prioritySSAContextCancelCall(&call.Call)
		detail := "ignored cancel func from context." + name
		violations = append(violations, prioritySSAViolation(pkg, call.Pos(), PrioritySSAContextCancelRule, detail))
	}
	return violations
}

func collectPrioritySSARawSQLViolations(pkg *prioritySSAPackage, fn *ssa.Function) []PrioritySSAViolation {
	if pkg.pkgPath != prioritySSAModulePath+"/internal/store/hookstore" {
		return nil
	}
	var violations []PrioritySSAViolation
	for _, instr := range prioritySSAInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok {
			continue
		}
		name, ok := prioritySSARawSQLCall(&call.Call)
		if ok {
			violations = append(violations, prioritySSAViolation(pkg, call.Pos(), PrioritySSARawSQLRule, "raw SQL call "+name))
		}
	}
	return violations
}

func collectPrioritySSAErrorStringViolations(pkg *prioritySSAPackage, fn *ssa.Function) []PrioritySSAViolation {
	var violations []PrioritySSAViolation
	for _, instr := range prioritySSAInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok {
			continue
		}
		name, ok := prioritySSAErrorStringMatchCall(&call.Call)
		if ok && !prioritySSAArchGuardIgnored(pkg, call.Pos(), PrioritySSAErrorStringRule) {
			violations = append(violations, prioritySSAViolation(pkg, call.Pos(), PrioritySSAErrorStringRule, "error string match "+name))
		}
	}
	return violations
}

// prioritySSAArchGuardIgnored 仅让已有的逐行 archguard 标注豁免对应 priority SSA 规则。
// 它必须按 SSA 调用位置定位同一语法文件，避免任意包级注释放宽整条规则。
func prioritySSAArchGuardIgnored(pkg *prioritySSAPackage, pos token.Pos, rule PrioritySSARule) bool {
	if pkg == nil || pkg.fset == nil || !pos.IsValid() {
		return false
	}
	position := pkg.fset.Position(pos)
	if position.Filename == "" || position.Line <= 0 {
		return false
	}
	for _, file := range pkg.syntax {
		if pkg.fset.Position(file.Pos()).Filename != position.Filename {
			continue
		}
		return collectArchGuardIgnores(pkg.fset, file).has(position.Line, string(rule))
	}
	return false
}

// collectPrioritySSAFXInvokeViolations 扫描 fx.Invoke 参数函数中的阻塞或进程副作用。
func collectPrioritySSAFXInvokeViolations(pkg *prioritySSAPackage, fn *ssa.Function) []PrioritySSAViolation {
	var violations []PrioritySSAViolation
	for _, instr := range prioritySSAInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok || !prioritySSAIsFXInvokeCall(&call.Call) {
			continue
		}
		for _, arg := range call.Call.Args {
			target := prioritySSAFunctionValue(arg)
			if target == nil || prioritySSARootBridgeAllowed(pkg, target) {
				continue
			}
			for _, reason := range prioritySSAFunctionSideEffects(target, 2, map[*ssa.Function]bool{}) {
				detail := fmt.Sprintf("fx.Invoke target %s %s", target.Name(), reason)
				violations = append(violations, prioritySSAViolation(pkg, call.Pos(), PrioritySSAFXInvokeRule, detail))
			}
		}
	}
	return violations
}

// collectPrioritySSAOnStartViolations 扫描 fx.Hook OnStart 回调中的启动副作用。
func collectPrioritySSAOnStartViolations(pkg *prioritySSAPackage, ssaPkg *ssa.Package) []PrioritySSAViolation {
	targets := prioritySSAOnStartFunctionTargets(pkg)
	if len(targets) == 0 {
		return nil
	}
	functions := prioritySSAFunctionsByName(ssaPkg)
	functionsByPos := prioritySSAFunctionsByPos(ssaPkg)
	var violations []PrioritySSAViolation
	for _, target := range targets {
		fn := prioritySSAFunctionForTarget(ssaPkg, target, functions, functionsByPos)
		if fn == nil || prioritySSARootBridgeAllowed(pkg, fn) {
			continue
		}
		for _, reason := range prioritySSAFunctionSideEffects(fn, 2, map[*ssa.Function]bool{}) {
			detail := fmt.Sprintf("fx.Hook OnStart target %s %s", target.label, reason)
			violations = append(violations, prioritySSAViolation(pkg, target.pos, PrioritySSAOnStartRule, detail))
		}
	}
	return violations
}

func prioritySSAInstructions(fn *ssa.Function) []ssa.Instruction {
	if fn == nil {
		return nil
	}
	var out []ssa.Instruction
	for _, block := range fn.Blocks {
		out = append(out, block.Instrs...)
	}
	return out
}

func prioritySSACallResultIgnored(call *ssa.Call) bool {
	if call == nil || !prioritySSATypeHasResult(call.Type()) {
		return false
	}
	refs := call.Referrers()
	return refs == nil || len(*refs) == 0
}

func prioritySSATypeHasResult(typ types.Type) bool {
	if typ == nil {
		return false
	}
	if tuple, ok := typ.(*types.Tuple); ok {
		return tuple.Len() > 0
	}
	return true
}

func prioritySSAIgnoredReturnCallee(call *ssa.CallCommon) (string, bool) {
	name, pkgPath := prioritySSACallNameAndPackage(call)
	switch name {
	case "Fire", "FireCtx":
		return name, strings.Contains(pkgPath, "stateless")
	case "Subscribe":
		if pkgPath == "github.com/kelindar/event" || prioritySSAReturnsContextCancelFunc(call.Signature()) {
			return prioritySSADisplayCallName(call, name), true
		}
	}
	return "", false
}

func prioritySSAContextCancelCall(call *ssa.CallCommon) (string, bool) {
	name, pkgPath := prioritySSACallNameAndPackage(call)
	if pkgPath != "context" || !strings.HasPrefix(name, "With") {
		return "", false
	}
	_, ok := prioritySSAContextCancelResultIndex(call.Signature())
	return name, ok
}

func prioritySSAContextCancelResultIndex(sig *types.Signature) (int, bool) {
	if sig == nil || sig.Results() == nil {
		return 0, false
	}
	for index := 0; index < sig.Results().Len(); index++ {
		result := sig.Results().At(index)
		if prioritySSAIsContextCancelType(result.Type()) {
			return index, true
		}
	}
	return 0, false
}

func prioritySSAIsContextCancelType(typ types.Type) bool {
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

// prioritySSAContextCancelResultDiscarded 判断 context.With* 的 cancel 返回值是否被丢弃。
func prioritySSAContextCancelResultDiscarded(call *ssa.Call) bool {
	if call == nil {
		return false
	}
	_, ok := prioritySSAContextCancelCall(&call.Call)
	if !ok {
		return false
	}
	cancelIndex, ok := prioritySSAContextCancelResultIndex(call.Call.Signature())
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
		return !prioritySSAValueHasMeaningfulUse(extract, map[ssa.Value]bool{})
	}
	return true
}

func prioritySSARawSQLCall(call *ssa.CallCommon) (string, bool) {
	name, _ := prioritySSACallNameAndPackage(call)
	switch name {
	case "Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext":
	default:
		return "", false
	}
	receiver := prioritySSACallReceiver(call)
	return name, receiver != nil && prioritySSAIsDatabaseSQLType(receiver.Type())
}

func prioritySSAErrorStringMatchCall(call *ssa.CallCommon) (string, bool) {
	name, pkgPath := prioritySSACallNameAndPackage(call)
	if pkgPath != "strings" || !prioritySSAStringMatchName(name) || len(call.Args) == 0 {
		return "", false
	}
	if prioritySSAValueComesFromErrorString(call.Args[0], map[ssa.Value]bool{}) {
		return "strings." + name, true
	}
	return "", false
}

func prioritySSAStringMatchName(name string) bool {
	switch name {
	case "Contains", "ContainsAny", "ContainsRune", "EqualFold", "HasPrefix", "HasSuffix":
		return true
	default:
		return false
	}
}

// prioritySSAValueComesFromErrorString 沿 SSA 值流判断字符串是否来自 err.Error。
func prioritySSAValueComesFromErrorString(value ssa.Value, seen map[ssa.Value]bool) bool {
	return prioritySSAValueComesFromErrorStringWithFuncs(value, seen, map[*ssa.Function]bool{})
}

// prioritySSAValueComesFromErrorStringWithFuncs 沿值流和一层静态 helper 返回值追踪错误字符串来源。
func prioritySSAValueComesFromErrorStringWithFuncs(
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
		return prioritySSAIsErrorMethodCall(&typed.Call) ||
			prioritySSAIsErrorFormattingCall(&typed.Call) ||
			prioritySSAFunctionReturnsErrorString(typed.Call.StaticCallee(), seenFuncs)
	case *ssa.ChangeInterface:
		return prioritySSAValueComesFromErrorStringWithFuncs(typed.X, seenValues, seenFuncs)
	case *ssa.ChangeType:
		return prioritySSAValueComesFromErrorStringWithFuncs(typed.X, seenValues, seenFuncs)
	case *ssa.Convert:
		return prioritySSAValueComesFromErrorStringWithFuncs(typed.X, seenValues, seenFuncs)
	case *ssa.Phi:
		return prioritySSAPhiComesFromErrorString(typed, seenValues, seenFuncs)
	}
	return false
}

func prioritySSAPhiComesFromErrorString(
	phi *ssa.Phi,
	seenValues map[ssa.Value]bool,
	seenFuncs map[*ssa.Function]bool,
) bool {
	for _, edge := range phi.Edges {
		if prioritySSAValueComesFromErrorStringWithFuncs(edge, seenValues, seenFuncs) {
			return true
		}
	}
	return false
}

func prioritySSAIsErrorFormattingCall(call *ssa.CallCommon) bool {
	name, pkgPath := prioritySSACallNameAndPackage(call)
	if pkgPath != "fmt" {
		return false
	}
	switch name {
	case "Sprint", "Sprintln":
		return prioritySSAArgsContainError(call.Args)
	case "Sprintf":
		if len(call.Args) < 2 {
			return false
		}
		return prioritySSAArgsContainError(call.Args[1:])
	default:
		return false
	}
}

func prioritySSAArgsContainError(args []ssa.Value) bool {
	for _, arg := range args {
		if prioritySSAValueImplementsError(arg, map[ssa.Value]bool{}) {
			return true
		}
	}
	return false
}

// prioritySSAValueImplementsError 判断值或 variadic backing store 中是否携带 error。
func prioritySSAValueImplementsError(value ssa.Value, seen map[ssa.Value]bool) bool {
	if value == nil || seen[value] {
		return false
	}
	seen[value] = true
	if prioritySSAImplementsError(value.Type()) {
		return true
	}
	if unwrapped := prioritySSAUnwrapErrorCarrier(value); unwrapped != nil {
		return prioritySSAValueImplementsError(unwrapped, seen)
	}
	switch typed := value.(type) {
	case *ssa.Alloc:
		return prioritySSAAggregateStoresError(typed, seen)
	case *ssa.IndexAddr:
		return prioritySSAAggregateStoresError(typed, seen)
	case *ssa.Phi:
		return prioritySSAPhiCarriesError(typed, seen)
	}
	return false
}

// prioritySSAUnwrapErrorCarrier 去掉接口装箱、转换和 variadic slice 的外壳。
func prioritySSAUnwrapErrorCarrier(value ssa.Value) ssa.Value {
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

func prioritySSAPhiCarriesError(phi *ssa.Phi, seen map[ssa.Value]bool) bool {
	for _, edge := range phi.Edges {
		if prioritySSAValueImplementsError(edge, seen) {
			return true
		}
	}
	return false
}

// prioritySSAAggregateStoresError 检查 variadic 参数组装产生的数组或字段地址里是否写入 error。
func prioritySSAAggregateStoresError(value ssa.Value, seen map[ssa.Value]bool) bool {
	refs := value.Referrers()
	if refs == nil {
		return false
	}
	for _, ref := range *refs {
		switch typed := ref.(type) {
		case *ssa.Store:
			if prioritySSAValueImplementsError(typed.Val, seen) {
				return true
			}
		case *ssa.FieldAddr:
			if prioritySSAAggregateStoresError(typed, seen) {
				return true
			}
		case *ssa.IndexAddr:
			if prioritySSAAggregateStoresError(typed, seen) {
				return true
			}
		}
	}
	return false
}

// prioritySSAFunctionReturnsErrorString 检查静态 helper 是否直接返回 err.Error 派生字符串。
func prioritySSAFunctionReturnsErrorString(fn *ssa.Function, seen map[*ssa.Function]bool) bool {
	if fn == nil || seen[fn] || !prioritySSAReturnsString(fn.Signature) {
		return false
	}
	seen[fn] = true
	for _, instr := range prioritySSAInstructions(fn) {
		ret, ok := instr.(*ssa.Return)
		if !ok {
			continue
		}
		for _, result := range ret.Results {
			if prioritySSAValueComesFromErrorStringWithFuncs(result, map[ssa.Value]bool{}, seen) {
				return true
			}
		}
	}
	return false
}

func prioritySSAIsErrorMethodCall(call *ssa.CallCommon) bool {
	name, _ := prioritySSACallNameAndPackage(call)
	if name != "Error" || !prioritySSAReturnsString(call.Signature()) {
		return false
	}
	receiver := prioritySSACallReceiver(call)
	return receiver != nil && prioritySSAImplementsError(receiver.Type())
}

func prioritySSAIsFXInvokeCall(call *ssa.CallCommon) bool {
	name, pkgPath := prioritySSACallNameAndPackage(call)
	if name != "Invoke" {
		return false
	}
	if pkgPath == "go.uber.org/fx" {
		return true
	}
	receiver := prioritySSACallReceiver(call)
	return receiver != nil && strings.HasSuffix(receiver.Type().String(), ".fxAPI")
}

// prioritySSAFunctionSideEffects 在有限深度内收集函数启动期不应隐藏的副作用。
func prioritySSAFunctionSideEffects(fn *ssa.Function, depth int, seen map[*ssa.Function]bool) []string {
	if fn == nil || seen[fn] {
		return nil
	}
	seen[fn] = true
	var out []string
	for _, instr := range prioritySSAInstructions(fn) {
		switch typed := instr.(type) {
		case *ssa.Go:
			out = append(out, "starts goroutine")
		case *ssa.Call:
			out = append(out, prioritySSACallSideEffectReasons(&typed.Call)...)
			if depth > 0 {
				out = append(out, prioritySSAFunctionSideEffects(typed.Call.StaticCallee(), depth-1, seen)...)
			}
		}
	}
	return prioritySSADedupeStrings(out)
}

// prioritySSACallSideEffectReasons 将 SSA 调用归因为具体副作用原因。
func prioritySSACallSideEffectReasons(call *ssa.CallCommon) []string {
	name, pkgPath := prioritySSACallNameAndPackage(call)
	switch {
	case pkgPath == "os/exec" && (name == "Command" || name == "CommandContext"):
		return []string{"calls exec command"}
	case pkgPath == "time" && prioritySSATimeSideEffectName(name):
		return []string{"sleeps or creates timer"}
	default:
		return nil
	}
}

func prioritySSATimeSideEffectName(name string) bool {
	switch name {
	case "After", "NewTicker", "Sleep", "Tick":
		return true
	default:
		return false
	}
}

// prioritySSAFunctionValue 从 SSA 值中解包出可静态分析的函数目标。
func prioritySSAFunctionValue(value ssa.Value) *ssa.Function {
	switch typed := value.(type) {
	case *ssa.Function:
		return typed
	case *ssa.MakeClosure:
		fn, _ := typed.Fn.(*ssa.Function)
		return fn
	case *ssa.MakeInterface:
		return prioritySSAFunctionValue(typed.X)
	case *ssa.ChangeInterface:
		return prioritySSAFunctionValue(typed.X)
	case *ssa.ChangeType:
		return prioritySSAFunctionValue(typed.X)
	case *ssa.Convert:
		return prioritySSAFunctionValue(typed.X)
	default:
		return nil
	}
}

// prioritySSAOnStartFunctionTargets 从 Hook 字面量中提取可映射到 SSA 函数的 OnStart 回调。
func prioritySSAOnStartFunctionTargets(pkg *prioritySSAPackage) []prioritySSAFunctionTarget {
	if pkg == nil {
		return nil
	}
	var targets []prioritySSAFunctionTarget
	for _, file := range pkg.syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			kv, ok := node.(*ast.KeyValueExpr)
			if !ok || prioritySSAExprString(kv.Key) != "OnStart" {
				return true
			}
			targets = append(targets, prioritySSAFunctionTargetFromExpr(pkg, kv.Value)...)
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

// prioritySSAFunctionTargetFromExpr 兼容函数名、匿名函数和 method value 三类 OnStart 入口。
func prioritySSAFunctionTargetFromExpr(pkg *prioritySSAPackage, expr ast.Expr) []prioritySSAFunctionTarget {
	switch typed := expr.(type) {
	case *ast.Ident:
		if typed.Name == "nil" {
			return nil
		}
		target := prioritySSAFunctionTarget{
			label: typed.Name,
			name:  typed.Name,
			pos:   typed.Pos(),
		}
		if obj, ok := pkg.typesInfo.Uses[typed].(*types.Func); ok {
			target.funcPos = obj.Pos()
		}
		return []prioritySSAFunctionTarget{target}
	case *ast.FuncLit:
		return []prioritySSAFunctionTarget{{
			label:   "func literal",
			name:    "func literal",
			pos:     typed.Type.Func,
			funcPos: typed.Type.Func,
		}}
	case *ast.SelectorExpr:
		label := typed.Sel.Name
		target := prioritySSAFunctionTarget{
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
		return []prioritySSAFunctionTarget{target}
	default:
		return nil
	}
}

func prioritySSAFunctionsByPos(ssaPkg *ssa.Package) map[token.Pos]*ssa.Function {
	out := map[token.Pos]*ssa.Function{}
	for _, fn := range prioritySSAFunctions(ssaPkg) {
		if fn.Pos().IsValid() {
			out[fn.Pos()] = fn
		}
		if obj, ok := fn.Object().(*types.Func); ok && obj.Pos().IsValid() {
			out[obj.Pos()] = fn
		}
	}
	return out
}

// prioritySSAValueHasMeaningfulUse 判断 cancel 结果是否被调用、defer、返回、存储或传递。
func prioritySSAValueHasMeaningfulUse(value ssa.Value, seen map[ssa.Value]bool) bool {
	if value == nil || seen[value] {
		return false
	}
	seen[value] = true
	refs := value.Referrers()
	if refs == nil {
		return false
	}
	for _, ref := range *refs {
		if prioritySSAInstructionUsesValue(ref, value, seen) {
			return true
		}
	}
	return false
}

// prioritySSAInstructionUsesValue 将 cancel 的引用按调用、暴露和继续转发三类判定。
func prioritySSAInstructionUsesValue(instr ssa.Instruction, value ssa.Value, seen map[ssa.Value]bool) bool {
	if prioritySSACallInstructionUsesValue(instr, value) || prioritySSAInstructionExposesValue(instr) {
		return true
	}
	forwarded, ok := instr.(ssa.Value)
	if !ok || !prioritySSAInstructionForwardsValue(forwarded) {
		return false
	}
	return prioritySSAValueHasMeaningfulUse(forwarded, seen)
}

func prioritySSACallInstructionUsesValue(instr ssa.Instruction, value ssa.Value) bool {
	switch typed := instr.(type) {
	case *ssa.Call:
		return prioritySSACallUsesValue(&typed.Call, value)
	case *ssa.Defer:
		return prioritySSACallUsesValue(&typed.Call, value)
	case *ssa.Go:
		return prioritySSACallUsesValue(&typed.Call, value)
	default:
		return false
	}
}

func prioritySSAInstructionExposesValue(instr ssa.Instruction) bool {
	switch instr.(type) {
	case *ssa.Return, *ssa.Store, *ssa.MakeClosure:
		return true
	default:
		return false
	}
}

func prioritySSAInstructionForwardsValue(value ssa.Value) bool {
	switch value.(type) {
	case *ssa.ChangeInterface, *ssa.ChangeType, *ssa.Convert, *ssa.MakeInterface, *ssa.Phi:
		return true
	default:
		return false
	}
}

func prioritySSACallUsesValue(call *ssa.CallCommon, value ssa.Value) bool {
	if call == nil || value == nil {
		return false
	}
	return call.Value == value || slices.Contains(call.Args, value)
}

// prioritySSAFunctionForTarget 将 AST OnStart 入口解析成可检查副作用的 SSA 函数体。
func prioritySSAFunctionForTarget(
	ssaPkg *ssa.Package,
	target prioritySSAFunctionTarget,
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
	return prioritySSALookupMethod(ssaPkg.Prog, target.recvType, target.methodPkg, target.name)
}

func prioritySSALookupMethod(
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

func prioritySSAFunctionsByName(ssaPkg *ssa.Package) map[string]*ssa.Function {
	out := map[string]*ssa.Function{}
	for _, fn := range prioritySSAFunctions(ssaPkg) {
		if fn.Parent() == nil {
			out[fn.Name()] = fn
			out[prioritySSACleanFunctionValueName(fn.Name())] = fn
		}
	}
	return out
}

// prioritySSAFunctions 返回包内顶层和匿名函数的稳定有序列表。
func prioritySSAFunctions(pkg *ssa.Package) []*ssa.Function {
	seen := map[*ssa.Function]bool{}
	var functions []*ssa.Function
	var collect func(fn *ssa.Function)
	collect = func(fn *ssa.Function) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		functions = append(functions, fn)
		for _, nested := range fn.AnonFuncs {
			collect(nested)
		}
		for _, instr := range prioritySSAInstructions(fn) {
			value, ok := instr.(ssa.Value)
			if !ok {
				continue
			}
			collect(prioritySSAFunctionValue(value))
		}
	}
	for _, member := range pkg.Members {
		if fn, ok := member.(*ssa.Function); ok {
			collect(fn)
		}
	}
	sort.Slice(functions, func(i, j int) bool {
		return prioritySSAFunctionSortKey(functions[i]) < prioritySSAFunctionSortKey(functions[j])
	})
	return functions
}

func prioritySSAFunctionSortKey(fn *ssa.Function) string {
	if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%09d\x00%s", fn.Pkg.Pkg.Path(), fn.Pos(), fn.String())
}
