package archtest

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func prioritySSACallNameAndPackage(call *ssa.CallCommon) (string, string) {
	if call == nil {
		return "", ""
	}
	if call.Method != nil {
		return call.Method.Name(), prioritySSAPackagePath(call.Method.Pkg())
	}
	if callee := call.StaticCallee(); callee != nil {
		if obj, ok := callee.Object().(*types.Func); ok {
			return obj.Name(), prioritySSAPackagePath(obj.Pkg())
		}
		return callee.Name(), prioritySSAFunctionPackagePath(callee)
	}
	return "", ""
}

func prioritySSADisplayCallName(call *ssa.CallCommon, name string) string {
	_, pkgPath := prioritySSACallNameAndPackage(call)
	if pkgPath == "github.com/kelindar/event" && name == "Subscribe" {
		return "event.Subscribe"
	}
	return name
}

// prioritySSACallReceiver 提取普通方法、方法值和方法表达式调用的 receiver。
func prioritySSACallReceiver(call *ssa.CallCommon) ssa.Value {
	return SSACallReceiver(call)
}

// SSACallReceiver 提取普通方法、方法值和方法表达式调用的 receiver。
func SSACallReceiver(call *ssa.CallCommon) ssa.Value {
	if call == nil {
		return nil
	}
	if call.Method != nil {
		return call.Value
	}
	if receiver := prioritySSABoundMethodReceiver(call); receiver != nil {
		return receiver
	}
	if callee := call.StaticCallee(); prioritySSAStaticCalleeHasReceiver(callee) && len(call.Args) > 0 {
		return call.Args[0]
	}
	return nil
}

func prioritySSAStaticCalleeHasReceiver(callee *ssa.Function) bool {
	return callee != nil && callee.Signature != nil && callee.Signature.Recv() != nil
}

func prioritySSABoundMethodReceiver(call *ssa.CallCommon) ssa.Value {
	closure, ok := call.Value.(*ssa.MakeClosure)
	if !ok || len(closure.Bindings) == 0 {
		return nil
	}
	fn, _ := closure.Fn.(*ssa.Function)
	if fn == nil || !prioritySSAFunctionHasReceiver(fn) {
		return nil
	}
	return closure.Bindings[0]
}

// prioritySSAFunctionHasReceiver 判断 SSA 函数是否代表带 receiver 的方法。
func prioritySSAFunctionHasReceiver(fn *ssa.Function) bool {
	return SSAFunctionHasReceiver(fn)
}

// SSAFunctionHasReceiver 判断 SSA 函数是否代表带 receiver 的方法。
func SSAFunctionHasReceiver(fn *ssa.Function) bool {
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

func prioritySSAReturnsContextCancelFunc(sig *types.Signature) bool {
	return prioritySSASignatureReturns(sig, "context.CancelFunc")
}

func prioritySSAReturnsString(sig *types.Signature) bool {
	return prioritySSASignatureReturns(sig, "string")
}

func prioritySSASignatureReturns(sig *types.Signature, want string) bool {
	if sig == nil || sig.Results() == nil || sig.Results().Len() != 1 {
		return false
	}
	return sig.Results().At(0).Type().String() == want
}

func prioritySSAIsDatabaseSQLType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	text := typ.String()
	return text == "*database/sql.DB" || text == "*database/sql.Tx" || text == "*database/sql.Conn"
}

func prioritySSAImplementsError(typ types.Type) bool {
	if typ == nil {
		return false
	}
	errorType := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	return types.Implements(typ, errorType)
}

func prioritySSAPackagePath(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}

func prioritySSAFunctionPackagePath(fn *ssa.Function) string {
	return SSAFunctionPackagePath(fn)
}

// SSAFunctionPackagePath 返回普通函数、方法和闭包所属的 Go package path。
func SSAFunctionPackagePath(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		if object := fn.Object(); object != nil && object.Pkg() != nil {
			return object.Pkg().Path()
		}
		if parent := fn.Parent(); parent != nil {
			return SSAFunctionPackagePath(parent)
		}
		return ""
	}
	return fn.Pkg.Pkg.Path()
}

func prioritySSARootBridgeAllowed(pkg *prioritySSAPackage, fn *ssa.Function) bool {
	if pkg == nil || fn == nil {
		return false
	}
	relPath, _ := prioritySSAFunctionPosition(pkg, fn)
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

func prioritySSAFunctionPosition(pkg *prioritySSAPackage, fn *ssa.Function) (string, int) {
	if fn == nil {
		return "(unknown)", 0
	}
	return prioritySSAPosition(pkg, fn.Pos())
}

func prioritySSAExprString(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return fmt.Sprint(expr)
}

func prioritySSADedupeStrings(values []string) []string {
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

func prioritySSAVarName(variable *types.Var) string {
	if variable == nil || variable.Name() == "" {
		return "(anonymous)"
	}
	return variable.Name()
}

func prioritySSAValueName(value ssa.Value) string {
	if value == nil || value.Name() == "" {
		return "(anonymous)"
	}
	return value.Name()
}

func prioritySSAClosureSymbol(closure *ssa.MakeClosure) string {
	fn, ok := closure.Fn.(*ssa.Function)
	if !ok || fn == nil {
		return ""
	}
	name := prioritySSACleanFunctionValueName(fn.Name())
	if index := strings.LastIndex(name, "."); index >= 0 && index+1 < len(name) {
		return name[index+1:]
	}
	return name
}

func prioritySSAMethodExpressionValueName(value ssa.Value) string {
	fn, ok := value.(*ssa.Function)
	if !ok || fn == nil || !strings.Contains(fn.Name(), "$thunk") {
		return ""
	}
	return prioritySSACleanFunctionValueName(fn.Name())
}

func prioritySSACleanFunctionValueName(name string) string {
	name = strings.TrimSuffix(name, "$bound")
	name = strings.TrimSuffix(name, "$thunk")
	if index := strings.LastIndex(name, "."); index >= 0 && index+1 < len(name) {
		return name[index+1:]
	}
	return name
}

func prioritySSASymbolOrValueName(symbol string, value ssa.Value) string {
	if symbol != "" {
		return symbol
	}
	return prioritySSAValueName(value)
}
