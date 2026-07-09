package archtest_test

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

type prioritySSAGuardFixture struct {
	name         string
	files        map[string]string
	targetName   string
	wantContains []string
}

func TestPrioritySSAGuardFixtures(t *testing.T) {
	t.Parallel()

	for _, tt := range prioritySSAGuardFixtures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := typeCheckPrioritySSAFixturePackage(t, tt.files)
			ssaPkg := buildOrchestrationServiceSSAPackage(t, pkg)
			got := collectPrioritySSAGuardMessages(t, pkg, ssaPkg, priorityFixtureTarget(t, pkg, tt.targetName))
			for _, want := range tt.wantContains {
				if !containsViolation(got, want) {
					t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

func prioritySSAGuardFixtures() []prioritySSAGuardFixture {
	return []prioritySSAGuardFixture{
		priorityWidePortFixture(),
		priorityIgnoredReturnFixture(),
		priorityRawSQLFixture(),
		priorityErrorStringFixture(),
		priorityFXInvokeFixture(),
		priorityLifecycleFixture(),
	}
}

func priorityWidePortFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:       "wide port propagation",
		files:      map[string]string{"internal/priorityfixture/wide.go": priorityWidePortFixtureSource()},
		targetName: "Wide",
		wantContains: []string{
			"field service uses broad port priorityfixture.Wide",
			"parameter service in returns uses broad port priorityfixture.Wide",
			"return value service in returns uses broad port priorityfixture.Wide",
		},
	}
}

func priorityIgnoredReturnFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "ignored critical return",
		files:        map[string]string{"internal/priorityfixture/ignored_return.go": priorityIgnoredReturnFixtureSource()},
		wantContains: []string{"ignored return from Subscribe"},
	}
}

func priorityRawSQLFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "raw sql receiver",
		files:        map[string]string{"internal/priorityfixture/sql.go": priorityRawSQLFixtureSource()},
		wantContains: []string{"raw SQL call QueryContext"},
	}
}

func priorityErrorStringFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "error string matching",
		files:        map[string]string{"internal/priorityfixture/error_string.go": priorityErrorStringFixtureSource()},
		wantContains: []string{"error string match strings.Contains"},
	}
}

func priorityFXInvokeFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "fx invoke helper side effect",
		files:        map[string]string{"internal/priorityfixture/fx_invoke.go": priorityFXInvokeFixtureSource()},
		wantContains: []string{"fx.Invoke target startRuntime calls exec command"},
	}
}

func priorityLifecycleFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "lifecycle onstart helper side effect",
		files:        map[string]string{"internal/priorityfixture/lifecycle.go": priorityLifecycleFixtureSource()},
		wantContains: []string{"fx.Hook OnStart target startRuntime sleeps or creates timer"},
	}
}

func priorityWidePortFixtureSource() string {
	return `package priorityfixture

type Wide interface {
	Launch()
	Stop()
	Snapshot()
}

type holder struct { service Wide }

func returns(service Wide) Wide { return service }
`
}

func priorityIgnoredReturnFixtureSource() string {
	return `package priorityfixture

import "context"

type Dispatcher struct{}
type Event struct{}

func Subscribe(*Dispatcher, func(Event)) context.CancelFunc { return func() {} }

func ignored(dispatcher *Dispatcher) {
	subscribe := Subscribe
	subscribe(dispatcher, func(Event) {})
}
`
}

func priorityRawSQLFixtureSource() string {
	return `package priorityfixture

import (
	"context"
	"database/sql"
)

func raw(ctx context.Context, db *sql.DB) {
	query := db.QueryContext
	query(ctx, "select 1")
}
`
}

func priorityErrorStringFixtureSource() string {
	return `package priorityfixture

import "strings"

func bad(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "missing")
}
`
}

func priorityFXInvokeFixtureSource() string {
	return `package priorityfixture

import "os/exec"

type Option struct{}
type fxAPI struct{}

func (fxAPI) Invoke(any) Option { return Option{} }

var fx fxAPI

func register() Option {
	return fx.Invoke(startRuntime)
}

func startRuntime() {
	runCommand()
}

func runCommand() {
	exec.Command("true")
}
`
}

func priorityLifecycleFixtureSource() string {
	return `package priorityfixture

import (
	"context"
	"time"
)

type Lifecycle interface { Append(Hook) }
type Hook struct { OnStart func(context.Context) error }

func register(lc Lifecycle) {
	lc.Append(Hook{OnStart: startRuntime})
}

func startRuntime(context.Context) error {
	waitRuntime()
	return nil
}

func waitRuntime() {
	time.Sleep(0)
}
`
}

func typeCheckPrioritySSAFixturePackage(t *testing.T, files map[string]string) *orchestrationServiceCheckedPackage {
	t.Helper()

	fset, syntax := parseOrchestrationServiceTypeGuardFixture(t, files)
	info := newOrchestrationServiceTypesInfo()
	conf := types.Config{
		Importer: importer.Default(),
	}
	checked, err := conf.Check(superAgentModulePath+"/internal/priorityfixture", fset, syntax, info)
	if err != nil {
		t.Fatalf("type check priority SSA fixture: %v", err)
	}
	return &orchestrationServiceCheckedPackage{
		pkgPath:   superAgentModulePath + "/internal/priorityfixture",
		fset:      fset,
		syntax:    syntax,
		types:     checked,
		typesInfo: info,
	}
}

func priorityFixtureTarget(t *testing.T, pkg *orchestrationServiceCheckedPackage, name string) *types.TypeName {
	t.Helper()
	if name == "" {
		return nil
	}
	obj, ok := pkg.types.Scope().Lookup(name).(*types.TypeName)
	if !ok {
		t.Fatalf("fixture target %s not found", name)
	}
	return obj
}

func collectPrioritySSAGuardMessages(
	t *testing.T,
	pkg *orchestrationServiceCheckedPackage,
	ssaPkg *ssa.Package,
	target *types.TypeName,
) []string {
	t.Helper()

	var messages []string
	if target != nil {
		messages = append(messages, collectPriorityWidePortMessages(t, pkg, target)...)
	}
	for _, fn := range orchestrationServiceSSAFunctions(ssaPkg) {
		messages = append(messages, collectPriorityIgnoredReturnMessages(pkg, fn)...)
		messages = append(messages, collectPriorityRawSQLMessages(pkg, fn)...)
		messages = append(messages, collectPriorityErrorStringMessages(pkg, fn)...)
		messages = append(messages, collectPriorityFXInvokeMessages(pkg, fn)...)
	}
	messages = append(messages, collectPriorityLifecycleMessages(pkg, ssaPkg)...)
	sort.Strings(messages)
	return messages
}

func collectPriorityWidePortMessages(
	t *testing.T,
	pkg *orchestrationServiceCheckedPackage,
	target *types.TypeName,
) []string {
	t.Helper()
	label := priorityTargetLabel(target)
	uses := collectOrchestrationServiceSSAUses(t, pkg, target)
	messages := make([]string, 0, len(uses))
	for _, use := range uses {
		messages = append(messages, priorityUseMessage(use, "uses broad port "+label))
	}
	return messages
}

func collectPriorityIgnoredReturnMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok || !priorityCallResultIgnored(call) {
			continue
		}
		name, ok := priorityIgnoredReturnCallee(&call.Call)
		if ok {
			messages = append(messages, priorityPositionMessage(pkg, call.Pos(), "ignored return from "+name))
		}
	}
	return messages
}

func collectPriorityRawSQLMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok {
			continue
		}
		name, ok := priorityRawSQLCall(&call.Call)
		if ok {
			messages = append(messages, priorityPositionMessage(pkg, call.Pos(), "raw SQL call "+name))
		}
	}
	return messages
}

func collectPriorityErrorStringMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok {
			continue
		}
		name, ok := priorityErrorStringMatchCall(&call.Call)
		if ok {
			messages = append(messages, priorityPositionMessage(pkg, call.Pos(), "error string match "+name))
		}
	}
	return messages
}

func collectPriorityFXInvokeMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok || !priorityIsFXInvokeCall(&call.Call) {
			continue
		}
		for _, arg := range call.Call.Args {
			target := priorityFunctionValue(arg)
			if target == nil || priorityRootBridgeAllowed(pkg, target) {
				continue
			}
			for _, reason := range priorityFunctionSideEffects(target, 2, map[*ssa.Function]bool{}) {
				detail := fmt.Sprintf("fx.Invoke target %s %s", target.Name(), reason)
				messages = append(messages, priorityPositionMessage(pkg, call.Pos(), detail))
			}
		}
	}
	return messages
}

func collectPriorityLifecycleMessages(pkg *orchestrationServiceCheckedPackage, ssaPkg *ssa.Package) []string {
	targets := priorityOnStartTargets(pkg.syntax)
	if len(targets) == 0 {
		return nil
	}
	functions := priorityFunctionsByName(ssaPkg)
	var messages []string
	for name, pos := range targets {
		fn := functions[name]
		if fn == nil || priorityRootBridgeAllowed(pkg, fn) {
			continue
		}
		for _, reason := range priorityFunctionSideEffects(fn, 2, map[*ssa.Function]bool{}) {
			detail := fmt.Sprintf("fx.Hook OnStart target %s %s", name, reason)
			messages = append(messages, priorityPositionMessage(pkg, pos, detail))
		}
	}
	return messages
}

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
	if value == nil || seen[value] {
		return false
	}
	seen[value] = true
	switch typed := value.(type) {
	case *ssa.Call:
		return priorityIsErrorMethodCall(&typed.Call)
	case *ssa.ChangeInterface:
		return priorityValueComesFromErrorString(typed.X, seen)
	case *ssa.ChangeType:
		return priorityValueComesFromErrorString(typed.X, seen)
	case *ssa.Convert:
		return priorityValueComesFromErrorString(typed.X, seen)
	case *ssa.Phi:
		for _, edge := range typed.Edges {
			if priorityValueComesFromErrorString(edge, seen) {
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

func priorityOnStartTargets(files []*ast.File) map[string]token.Pos {
	targets := map[string]token.Pos{}
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			kv, ok := node.(*ast.KeyValueExpr)
			if !ok || exprTypeString(kv.Key) != "OnStart" || !priorityInsideHookComposite(kv) {
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

func priorityInsideHookComposite(kv *ast.KeyValueExpr) bool {
	// The key/value itself does not hold parent pointers, so keep this check
	// intentionally permissive. SSA side-effect confirmation prevents a plain
	// OnStart data field from becoming noisy.
	return kv != nil
}

func priorityFunctionsByName(ssaPkg *ssa.Package) map[string]*ssa.Function {
	out := map[string]*ssa.Function{}
	for _, fn := range orchestrationServiceSSAFunctions(ssaPkg) {
		if fn.Parent() == nil {
			out[fn.Name()] = fn
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
