package remoteci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
)

// remoteGoProductionDeclaration records one production declaration that can be
// reached from an exact Go test declaration.
type remoteGoProductionDeclaration struct {
	directory string
	filePath  string
	file      *ast.File
	decl      *ast.FuncDecl
}

type remoteGoProductionIndex struct {
	byPackage map[string]map[string][]remoteGoProductionDeclaration
}

type remoteGoProductionCall struct {
	directory string
	file      *ast.File
	decl      ast.Node
}

// addGoProductionRuntimeObservedEntries recursively closes runtime observations
// made by production functions reachable from one selected test declaration.
// It returns tree scope for unknown file paths, process, environment,
// reflection, interface, or function-value dispatch.
// addGoProductionRuntimeObservedEntries 递归收敛精确测试可达的生产函数运行时观察闭包。
func (snapshot *remoteGitTreeSnapshot) addGoProductionRuntimeObservedEntries(
	directory string,
	root remoteGoTestDeclaration,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (remoteGoTestScope, error) {
	index, err := snapshot.remoteGoProductionIndex(profile)
	if err != nil {
		return remoteGoTestScopeTree, err
	}
	queue := []remoteGoProductionCall{{directory: directory, file: root.file, decl: root.declaration}}
	visited := make(map[string]struct{})
	scope := remoteGoTestScopeSelector
	for len(queue) > 0 {
		call := queue[0]
		queue = queue[1:]
		key := fmt.Sprintf("%p", call.decl)
		if _, ok := visited[key]; ok {
			continue
		}
		visited[key] = struct{}{}
		callScope, targets, err := snapshot.inspectGoProductionRuntimeCalls(call, directory, index, selected)
		if err != nil {
			return remoteGoTestScopeTree, err
		}
		scope = scope.widen(callScope)
		if scope == remoteGoTestScopeTree {
			return scope, nil
		}
		queue = append(queue, targets...)
	}
	return scope, nil
}

// remoteGoProductionIndex 返回当前 snapshot/profile 的生产函数声明索引。
func (snapshot *remoteGitTreeSnapshot) remoteGoProductionIndex(profile remoteGoBuildProfile) (remoteGoProductionIndex, error) {
	return snapshot.cachedRemoteGoProductionIndex(profile)
}

// buildRemoteGoProductionIndex 建立适用 worker 的生产函数声明索引。
func (snapshot *remoteGitTreeSnapshot) buildRemoteGoProductionIndex(profile remoteGoBuildProfile) (remoteGoProductionIndex, error) {
	index := remoteGoProductionIndex{byPackage: make(map[string]map[string][]remoteGoProductionDeclaration)}
	paths := make([]string, 0, len(snapshot.goSources))
	for filePath, source := range snapshot.goSources {
		if !remoteProductionGoSourceInDirectory(filePath, source, path.Dir(filePath), profile) {
			continue
		}
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		source := snapshot.goSources[filePath]
		file, err := parser.ParseFile(token.NewFileSet(), filePath, source, 0)
		if err != nil {
			return remoteGoProductionIndex{}, fmt.Errorf("parse production runtime source %q: %w", filePath, err)
		}
		directory := path.Dir(filePath)
		if index.byPackage[directory] == nil {
			index.byPackage[directory] = make(map[string][]remoteGoProductionDeclaration)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name == nil {
				continue
			}
			index.byPackage[directory][function.Name.Name] = append(index.byPackage[directory][function.Name.Name], remoteGoProductionDeclaration{
				directory: directory,
				filePath:  filePath,
				file:      file,
				decl:      function,
			})
		}
	}
	return index, nil
}

// inspectGoProductionRuntimeCalls 扫描一个生产声明并返回其可达调用目标与观察范围。
func (snapshot *remoteGitTreeSnapshot) inspectGoProductionRuntimeCalls(
	call remoteGoProductionCall,
	targetDirectory string,
	index remoteGoProductionIndex,
	selected map[string]remoteGitTreeEntry,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	imports := remoteGoTestImports(call.file)
	targets := make([]remoteGoProductionCall, 0)
	scope := remoteGoTestScopeSelector
	var visitErr error
	ast.Inspect(call.decl, func(node ast.Node) bool {
		if visitErr != nil || scope == remoteGoTestScopeTree {
			return false
		}
		if selector, ok := node.(*ast.SelectorExpr); ok && remoteGoProductionEnvironmentSelector(selector, imports) {
			scope = remoteGoTestScopeTree
			return false
		}
		expression, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		callScope, observedTargets, err := snapshot.inspectGoProductionCall(
			expression,
			call.directory,
			targetDirectory,
			call.file,
			imports,
			index,
			selected,
		)
		if err != nil {
			visitErr = err
			return false
		}
		scope = scope.widen(callScope)
		targets = append(targets, observedTargets...)
		return scope != remoteGoTestScopeTree
	})
	return scope, targets, visitErr
}

// remoteGoProductionEnvironmentSelector 识别直接读取进程环境或标准输入的 os 变量。
func remoteGoProductionEnvironmentSelector(selector *ast.SelectorExpr, imports map[string]string) bool {
	identifier, ok := selector.X.(*ast.Ident)
	if !ok || imports[identifier.Name] != "os" {
		return false
	}
	switch selector.Sel.Name {
	case "Args", "Stdin", "Stdout", "Stderr":
		return true
	default:
		return false
	}
}

// inspectGoProductionCall 解析单个调用的静态读取、局部递归或 fail-safe 范围。
func (snapshot *remoteGitTreeSnapshot) inspectGoProductionCall(
	call *ast.CallExpr,
	callDirectory string,
	targetDirectory string,
	file *ast.File,
	imports map[string]string,
	index remoteGoProductionIndex,
	selected map[string]remoteGitTreeEntry,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	if importPath, method, ok := remoteGoTestSelector(call, imports); ok {
		return snapshot.inspectImportedGoProductionCall(call, targetDirectory, file, imports, index, selected, importPath, method)
	}
	if _, ok := call.Fun.(*ast.SelectorExpr); ok {
		// A selector not rooted at an imported package is a method/interface call.
		return remoteGoTestScopeTree, nil, nil
	}
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok {
		return remoteGoTestScopeTree, nil, nil
	}
	return inspectLocalGoProductionCall(callDirectory, index, identifier.Name)
}

// inspectImportedGoProductionCall 处理导入包调用并递归进入本地生产函数。
func (snapshot *remoteGitTreeSnapshot) inspectImportedGoProductionCall(
	call *ast.CallExpr,
	targetDirectory string,
	file *ast.File,
	imports map[string]string,
	index remoteGoProductionIndex,
	selected map[string]remoteGitTreeEntry,
	importPath, method string,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	if importPath == "" {
		if remoteGoTestingMethod(method) {
			return remoteGoTestScopeSelector, nil, nil
		}
		return remoteGoTestScopeTree, nil, nil
	}
	if remoteGoProductionWholeTreeCall(importPath, method) {
		return remoteGoTestScopeTree, nil, nil
	}
	kind, staticPath, dynamic := remoteGoTestObservation(call, file, imports)
	if kind != "" {
		if dynamic {
			return remoteGoTestScopeTree, nil, nil
		}
		if err := snapshot.addGoProductionStaticObservation(targetDirectory, kind, staticPath, selected); err != nil {
			return remoteGoTestScopeTree, nil, err
		}
		return remoteGoTestScopeSelector, nil, nil
	}
	localDirectory, local := snapshot.resolveLocalGoImport(importPath)
	if !local {
		return remoteGoTestScopeTree, nil, nil
	}
	declarations := index.byPackage[localDirectory][method]
	if len(declarations) == 0 {
		return remoteGoTestScopeTree, nil, nil
	}
	targets := make([]remoteGoProductionCall, 0, len(declarations))
	for _, declaration := range declarations {
		targets = append(targets, remoteGoProductionCall{directory: localDirectory, file: declaration.file, decl: declaration.decl})
	}
	return remoteGoTestScopeSelector, targets, nil
}

// inspectLocalGoProductionCall 解析同包函数调用或无法证明的函数值调用。
func inspectLocalGoProductionCall(
	directory string,
	index remoteGoProductionIndex,
	name string,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	if remoteGoBuiltinCall(name) {
		return remoteGoTestScopeSelector, nil, nil
	}
	declarations := index.byPackage[directory][name]
	if len(declarations) == 0 {
		return remoteGoTestScopeTree, nil, nil
	}
	targets := make([]remoteGoProductionCall, 0, len(declarations))
	for _, declaration := range declarations {
		targets = append(targets, remoteGoProductionCall{directory: directory, file: declaration.file, decl: declaration.decl})
	}
	return remoteGoTestScopeSelector, targets, nil
}

// remoteGoTestingMethod 识别测试句柄方法，避免把测试框架日志误判为动态输入。
func remoteGoTestingMethod(method string) bool {
	switch method {
	case "Cleanup", "Error", "Errorf", "Fail", "FailNow", "Fatal", "Fatalf", "Helper", "Log", "Logf", "Name", "Parallel", "Run", "Skip", "SkipNow", "Skipf":
		return true
	default:
		return false
	}
}

// remoteGoProductionWholeTreeCall 识别必须绑定整棵候选树的生产调用。
func remoteGoProductionWholeTreeCall(importPath, method string) bool {
	switch importPath {
	case "os/exec", "syscall", "golang.org/x/sys/unix":
		return true
	case "os":
		switch method {
		case "Chdir", "Getenv", "LookupEnv", "Environ", "ExpandEnv", "Getwd", "Executable", "UserHomeDir", "Hostname", "StartProcess":
			return true
		}
	case "reflect":
		return true
	}
	return false
}

// remoteGoBuiltinCall 识别不会读取候选输入的 Go 内建调用。
func remoteGoBuiltinCall(name string) bool {
	switch name {
	case "append", "cap", "clear", "close", "complex", "copy", "delete", "imag", "len", "make", "max", "min", "new", "panic", "print", "println", "real", "recover":
		return true
	default:
		return false
	}
}

// addGoProductionStaticObservation 将可证明的生产静态路径加入精确输入集合。
func (snapshot *remoteGitTreeSnapshot) addGoProductionStaticObservation(
	directory, kind, staticPath string,
	selected map[string]remoteGitTreeEntry,
) error {
	observedPath, ok := remoteGoTestRelativePath(directory, staticPath)
	if !ok {
		if path.IsAbs(staticPath) {
			return fmt.Errorf("production runtime observation absolute path %q escapes canonical worker source", staticPath)
		}
		return nil
	}
	switch kind {
	case "glob":
		return snapshot.addRemoteGitGlobEntries(observedPath, selected)
	case "tree":
		snapshot.addRemoteGitDirectoryEntries(observedPath, selected)
	default:
		if _, exists := snapshot.byPath[observedPath]; !exists {
			return fmt.Errorf("static production runtime observation %q is absent from Git tree", observedPath)
		}
		snapshot.addRemoteGitPathEntry(observedPath, selected)
	}
	return nil
}

// addGoTestFileObservedEntriesScoped 收集选中 Go 测试声明的保守运行时观察。
func (snapshot *remoteGitTreeSnapshot) addGoTestFileObservedEntriesScoped(
	directory string,
	file *ast.File,
	observed ast.Node,
	selected map[string]remoteGitTreeEntry,
) (remoteGoTestScope, error) {
	imports := remoteGoTestImports(file)
	scope := remoteGoTestScopeSelector
	var visitErr error
	ast.Inspect(observed, func(node ast.Node) bool {
		if visitErr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		scope, visitErr = snapshot.addGoTestObservedCall(directory, file, call, imports, selected, scope)
		return visitErr == nil
	})
	return scope, visitErr
}

// addGoTestObservedCall 处理单个选中测试调用，并维护当前保守作用域。
func (snapshot *remoteGitTreeSnapshot) addGoTestObservedCall(
	directory string,
	file *ast.File,
	call *ast.CallExpr,
	imports map[string]string,
	selected map[string]remoteGitTreeEntry,
	currentScope remoteGoTestScope,
) (remoteGoTestScope, error) {
	kind, observedPath, callScope := remoteGoTestObservationScoped(directory, call, file, imports)
	currentScope = currentScope.widen(callScope)
	if kind == "" || currentScope == remoteGoTestScopeTree || observedPath == "" {
		return currentScope, nil
	}
	if callScope == remoteGoTestScopePackage {
		snapshot.addRemoteGitDirectoryEntries(directory, selected)
		return currentScope, nil
	}
	resolvedPath, ok := remoteGoTestRelativePath(directory, observedPath)
	if !ok {
		return remoteGoTestScopeTree, nil
	}
	if err := snapshot.addGoTestObservedPath(kind, resolvedPath, selected); err != nil {
		return currentScope, err
	}
	return currentScope, nil
}

func (snapshot *remoteGitTreeSnapshot) addGoTestObservedPath(
	kind, resolvedPath string,
	selected map[string]remoteGitTreeEntry,
) error {
	switch kind {
	case "tree":
		snapshot.addRemoteGitDirectoryEntries(resolvedPath, selected)
	case "glob":
		return snapshot.addRemoteGitGlobEntries(resolvedPath, selected)
	default:
		if _, exists := snapshot.byPath[resolvedPath]; !exists {
			return fmt.Errorf("static Go test file observation %q is absent from Git tree", resolvedPath)
		}
		snapshot.addRemoteGitPathEntry(resolvedPath, selected)
	}
	return nil
}

// remoteGoTestObservationScoped 分类测试中的候选树运行时观察，并保守选择三级作用域。
func remoteGoTestObservationScoped(directory string, call *ast.CallExpr, file *ast.File, imports map[string]string) (kind string, observedPath string, scope remoteGoTestScope) {
	importPath, method, ok := remoteGoTestSelector(call, imports)
	if !ok {
		if remoteGoTestObservationAlias(call, file, imports) {
			return "tree", "", remoteGoTestScopeTree
		}
		return "", "", remoteGoTestScopeSelector
	}
	if remoteGoTestUnsafeObservation(importPath, method) {
		return "tree", "", remoteGoTestScopeTree
	}
	index, observesPath := remoteGoTestObservationPathIndex(importPath, method)
	if !observesPath {
		return "", "", remoteGoTestScopeSelector
	}
	return remoteGoTestStaticObservation(directory, call, file, imports, importPath, method, index)
}

func remoteGoTestUnsafeObservation(importPath, method string) bool {
	return remoteGoTestProcessOrCWDObservation(importPath, method) ||
		(importPath == "golang.org/x/tools/go/packages" && method == "Load")
}

func remoteGoTestStaticObservation(
	directory string,
	call *ast.CallExpr,
	file *ast.File,
	imports map[string]string,
	importPath, method string,
	index int,
) (string, string, remoteGoTestScope) {
	value, static := remoteGoTestStringArgument(call, index)
	if !static {
		return remoteGoTestObservationKind(method), "", remoteGoTestScopeTree
	}
	if importPath == "io/fs" {
		value, static = remoteGoTestFSObservationPath(call, file, imports, value)
		if !static {
			return remoteGoTestObservationKind(method), "", remoteGoTestScopeTree
		}
	}
	if _, ok := remoteGoTestRelativePath(directory, value); !ok {
		return remoteGoTestObservationKind(method), "", remoteGoTestScopeTree
	}
	return remoteGoTestObservationKind(method), value, remoteGoTestScopeSelector
}

// remoteGoTestObservation 识别测试运行时观察并保守退化未知路径。
func remoteGoTestObservation(call *ast.CallExpr, file *ast.File, imports map[string]string) (kind string, staticPath string, dynamic bool) {
	importPath, method, ok := remoteGoTestSelector(call, imports)
	if !ok {
		if remoteGoTestObservationAlias(call, file, imports) {
			// 受观察函数的别名可能在运行时访问候选树或启动子进程，无法安全精确闭合。
			return "tree", "", true
		}
		return "", "", false
	}
	if remoteGoTestProcessOrCWDObservation(importPath, method) {
		// 子进程、系统调用和 cwd 变更都可能让目标观察候选树任意位置。
		return "tree", "", true
	}
	if importPath == "golang.org/x/tools/go/packages" && method == "Load" {
		return "tree", "", true
	}
	index, observesPath := remoteGoTestObservationPathIndex(importPath, method)
	if !observesPath {
		return "", "", false
	}
	value, ok := remoteGoTestStringArgument(call, index)
	if !ok {
		// 已识别但无法静态闭合路径的仓库读取可能解析到候选树任意位置，
		// 因此纳入全树，避免静默复用陈旧 PASS。
		return remoteGoTestObservationKind(method), "", true
	}
	if importPath == "io/fs" {
		value, ok = remoteGoTestFSObservationPath(call, file, imports, value)
		if !ok {
			// 无法解析的 fs.FS 根同样可能指向候选树内容。
			return remoteGoTestObservationKind(method), "", true
		}
	}
	return remoteGoTestObservationKind(method), value, false
}

func remoteGoTestSelector(call *ast.CallExpr, imports map[string]string) (string, string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return imports[packageName.Name], selector.Sel.Name, true
}

// remoteGoTestProcessOrCWDObservation 判断会逃逸静态文件闭包的进程或工作目录调用。
func remoteGoTestProcessOrCWDObservation(importPath, method string) bool {
	switch importPath {
	case "os/exec", "syscall", "golang.org/x/sys/unix":
		return true
	case "os":
		return method == "Chdir" || method == "StartProcess"
	default:
		return false
	}
}

// remoteGoTestObservationAlias 识别被赋值为受观察函数的包级或函数级别名。
func remoteGoTestObservationAlias(call *ast.CallExpr, file *ast.File, imports map[string]string) bool {
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	matched := false
	ast.Inspect(file, func(node ast.Node) bool {
		if matched {
			return false
		}
		switch value := node.(type) {
		case *ast.ValueSpec:
			matched = remoteGoTestObservationAliasValueSpec(identifier.Name, value, imports)
		case *ast.AssignStmt:
			matched = remoteGoTestObservationAliasValue(identifier.Name, value.Lhs, value.Rhs, imports)
		}
		return !matched
	})
	return matched
}

// remoteGoTestObservationAliasValueSpec 将变量声明统一为表达式列表后判断函数别名。
func remoteGoTestObservationAliasValueSpec(name string, value *ast.ValueSpec, imports map[string]string) bool {
	names := make([]ast.Expr, len(value.Names))
	for index, identifier := range value.Names {
		names[index] = identifier
	}
	return remoteGoTestObservationAliasValue(name, names, value.Values, imports)
}

// remoteGoTestObservationAliasValue 判断同位置变量是否接收受观察的函数值。
func remoteGoTestObservationAliasValue(name string, names, values []ast.Expr, imports map[string]string) bool {
	if len(names) != len(values) {
		return false
	}
	for index, candidate := range names {
		identifier, ok := candidate.(*ast.Ident)
		if !ok || identifier.Name != name {
			continue
		}
		selector, ok := values[index].(*ast.SelectorExpr)
		if !ok {
			return false
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok {
			return false
		}
		importPath := imports[packageName.Name]
		_, observesPath := remoteGoTestObservationPathIndex(importPath, selector.Sel.Name)
		return observesPath || remoteGoTestProcessOrCWDObservation(importPath, selector.Sel.Name)
	}
	return false
}
