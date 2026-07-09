package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

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
		if ok {
			violations = append(violations, prioritySSAViolation(pkg, call.Pos(), PrioritySSAErrorStringRule, "error string match "+name))
		}
	}
	return violations
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
	targets := prioritySSAOnStartTargets(pkg.syntax)
	if len(targets) == 0 {
		return nil
	}
	functions := prioritySSAFunctionsByName(ssaPkg)
	var violations []PrioritySSAViolation
	for name, pos := range targets {
		fn := functions[name]
		if fn == nil || prioritySSARootBridgeAllowed(pkg, fn) {
			continue
		}
		for _, reason := range prioritySSAFunctionSideEffects(fn, 2, map[*ssa.Function]bool{}) {
			detail := fmt.Sprintf("fx.Hook OnStart target %s %s", name, reason)
			violations = append(violations, prioritySSAViolation(pkg, pos, PrioritySSAOnStartRule, detail))
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
	if value == nil || seen[value] {
		return false
	}
	seen[value] = true
	switch typed := value.(type) {
	case *ssa.Call:
		return prioritySSAIsErrorMethodCall(&typed.Call)
	case *ssa.ChangeInterface:
		return prioritySSAValueComesFromErrorString(typed.X, seen)
	case *ssa.ChangeType:
		return prioritySSAValueComesFromErrorString(typed.X, seen)
	case *ssa.Convert:
		return prioritySSAValueComesFromErrorString(typed.X, seen)
	case *ssa.Phi:
		return prioritySSAPhiComesFromErrorString(typed, seen)
	}
	return false
}

func prioritySSAPhiComesFromErrorString(phi *ssa.Phi, seen map[ssa.Value]bool) bool {
	for _, edge := range phi.Edges {
		if prioritySSAValueComesFromErrorString(edge, seen) {
			return true
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

func prioritySSAOnStartTargets(files []*ast.File) map[string]token.Pos {
	targets := map[string]token.Pos{}
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			kv, ok := node.(*ast.KeyValueExpr)
			if !ok || prioritySSAExprString(kv.Key) != "OnStart" {
				return true
			}
			if ident, ok := kv.Value.(*ast.Ident); ok {
				targets[ident.Name] = ident.Pos()
			}
			return true
		})
	}
	return targets
}

func prioritySSAFunctionsByName(ssaPkg *ssa.Package) map[string]*ssa.Function {
	out := map[string]*ssa.Function{}
	for _, fn := range prioritySSAFunctions(ssaPkg) {
		if fn.Parent() == nil {
			out[fn.Name()] = fn
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
