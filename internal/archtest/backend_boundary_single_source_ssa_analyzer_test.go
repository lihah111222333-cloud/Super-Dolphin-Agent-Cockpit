package archtest_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	archtest "github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

type ssaBoundaryValueContext struct {
	value    ssa.Value
	bindings ssaBoundaryBindings
}

type ssaBoundaryBindings map[ssa.Value][]ssaBoundaryValueContext

type ssaBoundaryCallTarget struct {
	fn       *ssa.Function
	bindings ssaBoundaryBindings
}

type ssaBoundaryAnalysis struct {
	repoRoot     string
	program      *ssa.Program
	pkg          *packages.Package
	ssaPkg       *ssa.Package
	allowedFiles map[string]bool
	functions    []*ssa.Function
	facts        map[*ssa.Function]backendBoundaryDependencyFacts
	identities   map[*ssa.Function]string
}

type ssaBoundaryTraversal struct {
	facts      backendBoundaryDependencyFacts
	trace      map[string]bool
	unresolved map[string]bool
}

// backendBoundarySSACompileRootMode 只为选中的 external archtest variant 请求带类型语法；
// 依赖使用 export data，因此 loader 不会在把单个目标包转换为 SSA 前解析并类型检查整个依赖图。
const backendBoundarySSACompileRootMode = packages.LoadSyntax &^ packages.NeedDeps

// 文件查询只选择一个 package root，同时 Tests=true 仍暴露完整 external test variant；
// 目录模式会在 Include 丢弃前先让 go/packages 物化 production、internal-test、external-test
// 和 synthetic test-main 的所有 root。
const backendBoundarySSAEntryFile = "internal/archtest/backend_boundary_single_source_ssa_analyzer_test.go"

// backendBoundaryProductionSSAConnectivityViolations 只加载唯一 external archtest test variant 并分析 consumer files。
func backendBoundaryProductionSSAConnectivityViolations(t *testing.T, root string, consumerFiles []string) []string {
	t.Helper()
	pkgs, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"file=" + filepath.Join(root, filepath.FromSlash(backendBoundarySSAEntryFile))},
		Tests:    true,
		LoadMode: backendBoundarySSACompileRootMode,
		Include:  includeBackendBoundaryArchtestVariant,
	})
	if err != nil {
		t.Fatalf("load backend boundary production SSA: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("load backend boundary production SSA: got %d packages, want 1", len(pkgs))
	}
	program, built, err := ssaload.Build(pkgs)
	if err != nil {
		t.Fatalf("build backend boundary production SSA: %v", err)
	}
	targetFiles := make([]string, len(consumerFiles))
	for i, rel := range consumerFiles {
		targetFiles[i] = filepath.Join(root, filepath.FromSlash(rel))
	}
	return backendBoundarySSAConnectivityViolations(root, program, pkgs[0], built[0], targetFiles)
}

func includeBackendBoundaryArchtestVariant(pkg *packages.Package) bool {
	return pkg.Name == "archtest_test" && pkg.ForTest == archtestImportPath
}

func backendBoundarySSAConnectivityViolations(
	repoRoot string,
	program *ssa.Program,
	pkg *packages.Package,
	ssaPkg *ssa.Package,
	targetFiles []string,
) []string {
	analysis := newBackendBoundarySSAAnalysis(repoRoot, program, pkg, ssaPkg, targetFiles)
	var violations []string
	for _, root := range analysis.factRoots() {
		traversal := analysis.traverse(root)
		trace := sortedBackendBoundarySSASet(traversal.trace)
		rootID := analysis.identity(root)
		rootFile := analysis.relativeFunctionFile(root)
		for _, kind := range backendBoundarySSARuleKinds(traversal.facts) {
			violations = append(violations, fmt.Sprintf(
				"%s: procedural backend dependency evaluator duplicates the canonical registry [%s] root=%s trace=%s",
				rootFile, kind, rootID, strings.Join(trace, " -> "),
			))
		}
		if len(traversal.facts.parseTargets) > 0 {
			for _, site := range sortedBackendBoundarySSASet(traversal.unresolved) {
				violations = append(violations, fmt.Sprintf(
					"%s: ssa/unresolved-boundary-call root=%s trace=%s call=%s",
					rootFile, rootID, strings.Join(trace, " -> "), site,
				))
			}
		}
	}
	return dedupeBackendBoundarySSAViolations(violations)
}

func newBackendBoundarySSAAnalysis(
	repoRoot string,
	program *ssa.Program,
	pkg *packages.Package,
	ssaPkg *ssa.Package,
	targetFiles []string,
) *ssaBoundaryAnalysis {
	analysis := &ssaBoundaryAnalysis{
		repoRoot:     repoRoot,
		program:      program,
		pkg:          pkg,
		ssaPkg:       ssaPkg,
		allowedFiles: make(map[string]bool),
		facts:        make(map[*ssa.Function]backendBoundaryDependencyFacts),
		identities:   make(map[*ssa.Function]string),
	}
	for _, filename := range targetFiles {
		analysis.allowedFiles[filepath.Clean(filename)] = true
	}
	analysis.functions = analysis.collectSourceFunctions()
	analysis.collectFunctionFacts()
	return analysis
}

func (analysis *ssaBoundaryAnalysis) collectSourceFunctions() []*ssa.Function {
	seen := make(map[*ssa.Function]bool)
	var functions []*ssa.Function
	for _, member := range analysis.ssaPkg.Members {
		if fn, ok := member.(*ssa.Function); ok {
			functions = appendBackendBoundarySSAFunction(functions, fn, seen)
		}
	}
	for _, file := range analysis.pkg.Syntax {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			object, _ := analysis.pkg.TypesInfo.Defs[fn.Name].(*types.Func)
			if method := analysis.lookupDeclaredMethod(object); method != nil {
				functions = appendBackendBoundarySSAFunction(functions, method, seen)
			}
		}
	}
	sort.Slice(functions, func(i, j int) bool { return analysis.identity(functions[i]) < analysis.identity(functions[j]) })
	return functions
}

func appendBackendBoundarySSAFunction(functions []*ssa.Function, fn *ssa.Function, seen map[*ssa.Function]bool) []*ssa.Function {
	if fn == nil || seen[fn] {
		return functions
	}
	seen[fn] = true
	functions = append(functions, fn)
	for _, child := range fn.AnonFuncs {
		functions = appendBackendBoundarySSAFunction(functions, child, seen)
	}
	return functions
}

func (analysis *ssaBoundaryAnalysis) lookupDeclaredMethod(object *types.Func) *ssa.Function {
	if object == nil {
		return nil
	}
	signature, _ := object.Type().(*types.Signature)
	if signature == nil || signature.Recv() == nil {
		return nil
	}
	return analysis.program.LookupMethod(signature.Recv().Type(), object.Pkg(), object.Name())
}

func (analysis *ssaBoundaryAnalysis) collectFunctionFacts() {
	packageStrings := make(map[string]map[string]string)
	for _, file := range analysis.pkg.Syntax {
		filename := filepath.Clean(analysis.pkg.Fset.Position(file.Pos()).Filename)
		packageStrings[filename] = backendBoundaryPackageStringConstants(file)
	}
	for _, fn := range analysis.functions {
		if fn.Syntax() == nil || !analysis.sourceFunctionAllowed(fn) {
			continue
		}
		filename := filepath.Clean(analysis.pkg.Fset.Position(fn.Syntax().Pos()).Filename)
		analysis.facts[fn] = collectBackendBoundarySSAFunctionFacts(fn.Syntax(), packageStrings[filename])
	}
}

func collectBackendBoundarySSAFunctionFacts(node ast.Node, packageStrings map[string]string) backendBoundaryDependencyFacts {
	collected := newBackendBoundaryDependencyFacts()
	values := make(map[string]string, len(packageStrings))
	maps.Copy(values, packageStrings)
	inspectBackendBoundarySSANode(node, func(item ast.Node) bool {
		switch typed := item.(type) {
		case *ast.AssignStmt:
			backendBoundaryCollectAssignmentStrings(typed, values)
		case *ast.ValueSpec:
			backendBoundaryCollectValueSpecStrings(typed, values)
		}
		return true
	})
	inspectBackendBoundarySSANode(node, func(item ast.Node) bool {
		collectBackendBoundarySSANodeFact(item, values, &collected)
		return true
	})
	return collected
}

func inspectBackendBoundarySSANode(root ast.Node, visit func(ast.Node) bool) {
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		if _, nested := node.(*ast.FuncLit); nested && node != root {
			return false
		}
		return visit(node)
	})
}

func collectBackendBoundarySSANodeFact(item ast.Node, values map[string]string, collected *backendBoundaryDependencyFacts) {
	if expr, ok := item.(ast.Expr); ok {
		if value, ok := backendBoundaryStringValue(expr, values); ok {
			collected.facts[value] = true
		}
	}
	call, ok := item.(*ast.CallExpr)
	if !ok || backendBoundaryCallName(call.Fun) != "parseImportFiles" {
		return
	}
	for _, arg := range call.Args {
		if value, ok := backendBoundaryStringValue(arg, values); ok {
			collected.parseTargets[value] = true
		}
	}
}

func (analysis *ssaBoundaryAnalysis) factRoots() []*ssa.Function {
	var roots []*ssa.Function
	for fn, facts := range analysis.facts {
		if backendBoundarySSAHasRootFacts(facts) {
			roots = append(roots, fn)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return analysis.identity(roots[i]) < analysis.identity(roots[j]) })
	return roots
}

func backendBoundarySSAHasRootFacts(facts backendBoundaryDependencyFacts) bool {
	if len(facts.parseTargets) > 0 {
		return true
	}
	for _, fact := range []string{
		"internal/platform/config",
		"internal/store/sqlc",
		"go.uber.org/fx",
		"internal/tool/lsp",
		"internal/tool/ida",
		"internal/tool/orchestration",
		"internal/platform/mcpcontrol",
		"internal/platform/db",
		"internal/platform/hooks",
	} {
		if facts.facts[fact] {
			return true
		}
	}
	return false
}

func (analysis *ssaBoundaryAnalysis) traverse(root *ssa.Function) ssaBoundaryTraversal {
	result := ssaBoundaryTraversal{
		facts:      newBackendBoundaryDependencyFacts(),
		trace:      make(map[string]bool),
		unresolved: make(map[string]bool),
	}
	visited := make(map[string]bool)
	analysis.traverseTarget(ssaBoundaryCallTarget{fn: root, bindings: make(ssaBoundaryBindings)}, visited, &result)
	return result
}

func (analysis *ssaBoundaryAnalysis) traverseTarget(
	target ssaBoundaryCallTarget,
	visited map[string]bool,
	result *ssaBoundaryTraversal,
) {
	if !analysis.localFunctionAllowed(target.fn) {
		return
	}
	key := backendBoundarySSAStateKey(target)
	if visited[key] {
		return
	}
	visited[key] = true
	identity := analysis.identity(target.fn)
	result.trace[identity] = true
	if facts, ok := analysis.facts[target.fn]; ok {
		mergeBackendBoundaryFactSet(result.facts.parseTargets, facts.parseTargets)
		mergeBackendBoundaryFactSet(result.facts.facts, facts.facts)
	}
	for _, block := range target.fn.Blocks {
		for _, instruction := range block.Instrs {
			callInstruction, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			resolved, unresolved := analysis.resolveCallTargets(callInstruction.Common(), target.bindings, 0)
			if unresolved {
				result.unresolved[analysis.callSite(target.fn, callInstruction.Common())] = true
			}
			for _, callee := range resolved {
				analysis.traverseTarget(callee, visited, result)
			}
		}
	}
}

func (analysis *ssaBoundaryAnalysis) resolveCallTargets(
	call *ssa.CallCommon,
	bindings ssaBoundaryBindings,
	depth int,
) ([]ssaBoundaryCallTarget, bool) {
	if call == nil || depth > 32 {
		return nil, call != nil
	}
	if targets, handled := analysis.resolveStaticCallTarget(call, bindings); handled {
		return targets, false
	}
	if _, builtin := call.Value.(*ssa.Builtin); builtin {
		return nil, false
	}
	if call.IsInvoke() {
		return analysis.resolveInvokeTargets(call, bindings, depth+1)
	}
	values, unknown := analysis.resolveValues(call.Value, bindings, depth+1, make(map[ssa.Value]bool))
	var targets []ssaBoundaryCallTarget
	resolvedFunction := false
	for _, value := range values {
		target, ok := analysis.callTargetFromValue(value, call.Args)
		if !ok {
			unknown = true
			continue
		}
		resolvedFunction = true
		if analysis.localFunctionAllowed(target.fn) {
			targets = append(targets, target)
		}
	}
	targets = dedupeBackendBoundarySSATargets(targets)
	return targets, backendBoundarySSACallUnresolved(unknown, resolvedFunction, len(targets))
}

func backendBoundarySSACallUnresolved(unknown, resolvedFunction bool, localTargetCount int) bool {
	return unknown || !resolvedFunction && localTargetCount == 0
}

func (analysis *ssaBoundaryAnalysis) resolveStaticCallTarget(
	call *ssa.CallCommon,
	bindings ssaBoundaryBindings,
) ([]ssaBoundaryCallTarget, bool) {
	static := call.StaticCallee()
	if static == nil {
		return nil, false
	}
	if !analysis.localFunctionAllowed(static) {
		return nil, true
	}
	return []ssaBoundaryCallTarget{{fn: static, bindings: bindBackendBoundarySSAArgs(static, call.Args, bindings)}}, true
}

func (analysis *ssaBoundaryAnalysis) callTargetFromValue(
	context ssaBoundaryValueContext,
	args []ssa.Value,
) (ssaBoundaryCallTarget, bool) {
	switch value := context.value.(type) {
	case *ssa.Function:
		return ssaBoundaryCallTarget{fn: value, bindings: bindBackendBoundarySSAArgs(value, args, context.bindings)}, true
	case *ssa.MakeClosure:
		fn, _ := value.Fn.(*ssa.Function)
		if fn == nil {
			return ssaBoundaryCallTarget{}, false
		}
		bindings := bindBackendBoundarySSAClosure(fn, value, context.bindings)
		bindBackendBoundarySSAParams(bindings, fn.Params, args, context.bindings)
		return ssaBoundaryCallTarget{fn: fn, bindings: bindings}, true
	default:
		return ssaBoundaryCallTarget{}, false
	}
}

func (analysis *ssaBoundaryAnalysis) resolveInvokeTargets(
	call *ssa.CallCommon,
	bindings ssaBoundaryBindings,
	depth int,
) ([]ssaBoundaryCallTarget, bool) {
	receiverValue := archtest.SSACallReceiver(call)
	receivers, unknown := analysis.resolveValues(receiverValue, bindings, depth+1, make(map[ssa.Value]bool))
	var targets []ssaBoundaryCallTarget
	resolvedReceiver := false
	for _, receiver := range receivers {
		if analysis.knownExternalValue(receiver.value) {
			resolvedReceiver = true
			continue
		}
		method := analysis.lookupInvokeMethod(receiver.value.Type(), call.Method)
		if method == nil {
			unknown = true
			continue
		}
		resolvedReceiver = true
		methodBindings := make(ssaBoundaryBindings)
		if len(method.Params) > 0 {
			methodBindings[method.Params[0]] = []ssaBoundaryValueContext{receiver}
		}
		bindBackendBoundarySSAParams(methodBindings, method.Params[1:], call.Args, bindings)
		targets = append(targets, ssaBoundaryCallTarget{fn: method, bindings: methodBindings})
	}
	targets = dedupeBackendBoundarySSATargets(targets)
	return targets, unknown || !resolvedReceiver && len(targets) == 0
}

func (analysis *ssaBoundaryAnalysis) knownExternalValue(value ssa.Value) bool {
	switch typed := value.(type) {
	case *ssa.Call:
		return analysis.knownExternalCall(typed.Common())
	case *ssa.Extract:
		call, ok := typed.Tuple.(*ssa.Call)
		return ok && analysis.knownExternalCall(call.Common())
	default:
		return false
	}
}

func (analysis *ssaBoundaryAnalysis) knownExternalCall(call *ssa.CallCommon) bool {
	if call == nil {
		return false
	}
	callee := call.StaticCallee()
	return callee != nil && !analysis.localFunctionAllowed(callee)
}

func (analysis *ssaBoundaryAnalysis) lookupInvokeMethod(concrete types.Type, method *types.Func) *ssa.Function {
	if concrete == nil || method == nil {
		return nil
	}
	if _, ok := concrete.Underlying().(*types.Interface); ok {
		return nil
	}
	if types.NewMethodSet(concrete).Lookup(method.Pkg(), method.Name()) == nil {
		return nil
	}
	return analysis.program.LookupMethod(concrete, method.Pkg(), method.Name())
}

func (analysis *ssaBoundaryAnalysis) resolveValues(
	value ssa.Value,
	bindings ssaBoundaryBindings,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	if value == nil || depth > 32 {
		return nil, value != nil
	}
	if seen[value] {
		return nil, false
	}
	seen[value] = true
	defer delete(seen, value)
	if bound, ok := bindings[value]; ok {
		return analysis.resolveBoundValues(bound, depth+1, seen)
	}
	if unwrapped, ok := unwrapBackendBoundarySSAValue(value); ok {
		return analysis.resolveValues(unwrapped, bindings, depth+1, seen)
	}
	if resolved, unknown, handled := analysis.resolveMemoryValue(value, bindings, depth+1, seen); handled {
		return resolved, unknown
	}
	return analysis.resolveStructuredValue(value, bindings, depth+1, seen)
}

func (analysis *ssaBoundaryAnalysis) resolveStructuredValue(
	value ssa.Value,
	bindings ssaBoundaryBindings,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	switch typed := value.(type) {
	case *ssa.Phi:
		return analysis.resolvePhiValues(typed, bindings, depth+1, seen)
	case *ssa.Extract:
		return analysis.resolveExtractValue(typed, bindings, depth+1, seen)
	case *ssa.Call:
		return analysis.resolveCallResult(typed, 0, bindings, depth+1, seen)
	case *ssa.Parameter, *ssa.FreeVar:
		return nil, true
	default:
		return []ssaBoundaryValueContext{{value: value, bindings: bindings}}, false
	}
}

func (analysis *ssaBoundaryAnalysis) resolveMemoryValue(
	value ssa.Value,
	bindings ssaBoundaryBindings,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool, bool) {
	switch typed := value.(type) {
	case *ssa.UnOp:
		if typed.Op != token.MUL {
			return nil, false, false
		}
		resolved, unknown := analysis.resolveLoadedValues(typed.X, bindings, depth+1, seen)
		return resolved, unknown, true
	case *ssa.Alloc:
		resolved, unknown := analysis.resolveAllocValues(typed, bindings, depth+1, seen)
		return resolved, unknown, true
	default:
		return nil, false, false
	}
}

func (analysis *ssaBoundaryAnalysis) resolveLoadedValues(
	address ssa.Value,
	bindings ssaBoundaryBindings,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	if depth > 32 {
		return nil, true
	}
	if bound, ok := bindings[address]; ok {
		return analysis.resolveLoadedBindings(bound, depth+1, seen)
	}
	if unwrapped, ok := unwrapBackendBoundarySSAValue(address); ok {
		return analysis.resolveLoadedValues(unwrapped, bindings, depth+1, seen)
	}
	alloc, ok := address.(*ssa.Alloc)
	if !ok {
		return nil, true
	}
	return analysis.resolveAllocValues(alloc, bindings, depth+1, seen)
}

func (analysis *ssaBoundaryAnalysis) resolveLoadedBindings(
	bound []ssaBoundaryValueContext,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	var values []ssaBoundaryValueContext
	unknown := false
	for _, context := range bound {
		resolved, unresolved := analysis.resolveLoadedValues(context.value, context.bindings, depth+1, seen)
		values = append(values, resolved...)
		unknown = unknown || unresolved
	}
	return values, unknown || len(values) == 0
}

func (analysis *ssaBoundaryAnalysis) resolveAllocValues(
	alloc *ssa.Alloc,
	bindings ssaBoundaryBindings,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	refs := alloc.Referrers()
	if refs == nil {
		return nil, true
	}
	var values []ssaBoundaryValueContext
	unknown := false
	for _, ref := range *refs {
		store, ok := ref.(*ssa.Store)
		if !ok || store.Addr != alloc {
			continue
		}
		resolved, unresolved := analysis.resolveValues(store.Val, bindings, depth+1, seen)
		values = append(values, resolved...)
		unknown = unknown || unresolved
	}
	return values, unknown || len(values) == 0
}

func unwrapBackendBoundarySSAValue(value ssa.Value) (ssa.Value, bool) {
	switch typed := value.(type) {
	case *ssa.ChangeInterface:
		return typed.X, true
	case *ssa.ChangeType:
		return typed.X, true
	case *ssa.Convert:
		return typed.X, true
	case *ssa.MakeInterface:
		return typed.X, true
	default:
		return nil, false
	}
}

func (analysis *ssaBoundaryAnalysis) resolveBoundValues(
	bound []ssaBoundaryValueContext,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	var values []ssaBoundaryValueContext
	unknown := false
	for _, context := range bound {
		resolved, unresolved := analysis.resolveValues(context.value, context.bindings, depth+1, seen)
		values = append(values, resolved...)
		unknown = unknown || unresolved
	}
	return values, unknown
}

func (analysis *ssaBoundaryAnalysis) resolvePhiValues(
	phi *ssa.Phi,
	bindings ssaBoundaryBindings,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	var values []ssaBoundaryValueContext
	unknown := false
	for _, edge := range phi.Edges {
		resolved, unresolved := analysis.resolveValues(edge, bindings, depth+1, seen)
		values = append(values, resolved...)
		unknown = unknown || unresolved
	}
	return values, unknown
}

func (analysis *ssaBoundaryAnalysis) resolveExtractValue(
	extract *ssa.Extract,
	bindings ssaBoundaryBindings,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	call, ok := extract.Tuple.(*ssa.Call)
	if !ok {
		return []ssaBoundaryValueContext{{value: extract, bindings: bindings}}, false
	}
	if analysis.knownExternalCall(call.Common()) {
		return []ssaBoundaryValueContext{{value: extract, bindings: bindings}}, false
	}
	return analysis.resolveCallResult(call, extract.Index, bindings, depth+1, seen)
}

func (analysis *ssaBoundaryAnalysis) resolveCallResult(
	call *ssa.Call,
	index int,
	bindings ssaBoundaryBindings,
	depth int,
	seen map[ssa.Value]bool,
) ([]ssaBoundaryValueContext, bool) {
	if analysis.knownExternalCall(call.Common()) {
		return []ssaBoundaryValueContext{{value: call, bindings: bindings}}, false
	}
	targets, unknown := analysis.resolveCallTargets(call.Common(), bindings, depth+1)
	var values []ssaBoundaryValueContext
	for _, target := range targets {
		for _, block := range target.fn.Blocks {
			for _, instruction := range block.Instrs {
				ret, ok := instruction.(*ssa.Return)
				if !ok || index >= len(ret.Results) {
					continue
				}
				resolved, unresolved := analysis.resolveValues(ret.Results[index], target.bindings, depth+1, seen)
				values = append(values, resolved...)
				unknown = unknown || unresolved
			}
		}
	}
	return values, unknown || len(values) == 0
}

func bindBackendBoundarySSAArgs(fn *ssa.Function, args []ssa.Value, caller ssaBoundaryBindings) ssaBoundaryBindings {
	bindings := make(ssaBoundaryBindings)
	bindBackendBoundarySSAParams(bindings, fn.Params, args, caller)
	return bindings
}

func bindBackendBoundarySSAParams(
	bindings ssaBoundaryBindings,
	params []*ssa.Parameter,
	args []ssa.Value,
	caller ssaBoundaryBindings,
) {
	limit := min(len(params), len(args))
	for i := range limit {
		bindings[params[i]] = []ssaBoundaryValueContext{{value: args[i], bindings: caller}}
	}
}

func bindBackendBoundarySSAClosure(
	fn *ssa.Function,
	closure *ssa.MakeClosure,
	caller ssaBoundaryBindings,
) ssaBoundaryBindings {
	bindings := make(ssaBoundaryBindings)
	limit := min(len(fn.FreeVars), len(closure.Bindings))
	for i := range limit {
		bindings[fn.FreeVars[i]] = []ssaBoundaryValueContext{{value: closure.Bindings[i], bindings: caller}}
	}
	return bindings
}

func (analysis *ssaBoundaryAnalysis) sourceFunctionAllowed(fn *ssa.Function) bool {
	if fn == nil || fn.Syntax() == nil {
		return false
	}
	filename := filepath.Clean(analysis.pkg.Fset.Position(fn.Syntax().Pos()).Filename)
	return analysis.allowedFiles[filename]
}

func (analysis *ssaBoundaryAnalysis) localFunctionAllowed(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	if fn.Syntax() != nil {
		return analysis.sourceFunctionAllowed(fn)
	}
	return archtest.SSAFunctionPackagePath(fn) == analysis.pkg.PkgPath ||
		fn.Parent() != nil && analysis.localFunctionAllowed(fn.Parent())
}

func (analysis *ssaBoundaryAnalysis) identity(fn *ssa.Function) string {
	if identity := analysis.identities[fn]; identity != "" {
		return identity
	}
	position := analysis.pkg.Fset.Position(fn.Pos())
	name := fn.Name()
	if archtest.SSAFunctionHasReceiver(fn) && fn.Signature != nil && fn.Signature.Recv() != nil {
		name = backendBoundarySSAReceiverName(fn.Signature.Recv().Type()) + "." + name
	}
	identity := archtest.SSAFunctionPackagePath(fn) + "#" + name + "@" + analysis.relativeFilename(position.Filename) + ":" + strconv.Itoa(position.Line)
	analysis.identities[fn] = identity
	return identity
}

func backendBoundarySSAReceiverName(typ types.Type) string {
	name := strings.TrimPrefix(types.TypeString(typ, func(*types.Package) string { return "" }), "*")
	return strings.TrimPrefix(name, ".")
}

func (analysis *ssaBoundaryAnalysis) relativeFunctionFile(fn *ssa.Function) string {
	return analysis.relativeFilename(analysis.pkg.Fset.Position(fn.Pos()).Filename)
}

func (analysis *ssaBoundaryAnalysis) relativeFilename(filename string) string {
	rel, err := filepath.Rel(analysis.repoRoot, filename)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(filename)
	}
	return filepath.ToSlash(rel)
}

func (analysis *ssaBoundaryAnalysis) callSite(fn *ssa.Function, call *ssa.CallCommon) string {
	position := analysis.pkg.Fset.Position(call.Pos())
	return analysis.identity(fn) + "@" + analysis.relativeFilename(position.Filename) + ":" + strconv.Itoa(position.Line)
}

func backendBoundarySSAStateKey(target ssaBoundaryCallTarget) string {
	return fmt.Sprintf("%p", target.fn) + "|" + backendBoundarySSABindingKey(target.bindings, 0, make(map[ssa.Value]bool))
}

func backendBoundarySSABindingKey(bindings ssaBoundaryBindings, depth int, seen map[ssa.Value]bool) string {
	if depth > 16 {
		return "depth-limit"
	}
	var parts []string
	for value, contexts := range bindings {
		if seen[value] {
			parts = append(parts, fmt.Sprintf("%p=cycle", value))
			continue
		}
		seen[value] = true
		for _, context := range contexts {
			parts = append(parts, fmt.Sprintf("%p=%p{%s}", value, context.value, backendBoundarySSABindingKey(context.bindings, depth+1, seen)))
		}
		delete(seen, value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func dedupeBackendBoundarySSATargets(targets []ssaBoundaryCallTarget) []ssaBoundaryCallTarget {
	seen := make(map[string]bool)
	out := targets[:0]
	for _, target := range targets {
		key := backendBoundarySSAStateKey(target)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, target)
	}
	return out
}

func backendBoundarySSARuleKinds(facts backendBoundaryDependencyFacts) []string {
	var kinds []string
	if declaresProceduralStoreBoundary(facts.parseTargets, facts.facts) {
		kinds = append(kinds, "store boundary")
	}
	if declaresProceduralFXBoundary(facts.parseTargets, facts.facts) {
		kinds = append(kinds, "fx boundary")
	}
	if declaresProceduralMCPServerFamilyBoundary(facts.parseTargets, facts.facts) {
		kinds = append(kinds, "mcp server family boundary")
	}
	if declaresProceduralPlatformControlBoundary(facts.parseTargets, facts.facts) {
		kinds = append(kinds, "platform control boundary")
	}
	return kinds
}

func sortedBackendBoundarySSASet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func dedupeBackendBoundarySSAViolations(violations []string) []string {
	sort.Strings(violations)
	out := violations[:0]
	for _, violation := range violations {
		if len(out) == 0 || out[len(out)-1] != violation {
			out = append(out, violation)
		}
	}
	return out
}
