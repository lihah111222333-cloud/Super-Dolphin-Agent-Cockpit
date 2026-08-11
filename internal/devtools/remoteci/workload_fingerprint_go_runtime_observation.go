package remoteci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"
)

// remoteGoProductionDeclaration records one production declaration that can be
// reached from an exact Go test declaration.
type remoteGoProductionDeclaration struct {
	directory string
	filePath  string
	file      *ast.File
	decl      ast.Decl
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
	packageRoots, err := snapshot.remoteGoProductionRuntimeRoots(directory, root.file, index, profile)
	if err != nil {
		return remoteGoTestScopeTree, err
	}
	for _, packageDirectory := range packageRoots {
		for _, initializer := range index.byPackage[packageDirectory]["\x00remote-initializer"] {
			queue = append(queue, remoteGoProductionCall{directory: initializer.directory, file: initializer.file, decl: initializer.decl})
		}
	}
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
		callScope, targets, err := snapshot.inspectGoProductionRuntimeCalls(call, directory, index, selected, profile)
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

// remoteGoProductionRuntimeRoots 收集测试运行时会执行的全部本地生产包初始化根。
// It resolves every local production package
// imported by the selected test package and its production dependencies. Go
// executes each package's vars and init functions even when no exported
// function is called, so each reached package is an unconditional runtime root.
func (snapshot *remoteGitTreeSnapshot) remoteGoProductionRuntimeRoots(
	directory string,
	rootFile *ast.File,
	index remoteGoProductionIndex,
	profile remoteGoBuildProfile,
) ([]string, error) {
	seen := make(map[string]struct{})
	queue := []string{directory}
	if rootFile != nil {
		for _, importPath := range remoteGoTestImports(rootFile) {
			if importedDirectory, ok := snapshot.resolveLocalGoImport(importPath); ok {
				queue = append(queue, importedDirectory)
			}
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, alreadySeen := seen[current]; alreadySeen {
			continue
		}
		seen[current] = struct{}{}
		imports, err := snapshot.remoteGoProductionImportedDirectories(current, index, profile)
		if err != nil {
			return nil, err
		}
		queue = append(queue, imports...)
	}
	roots := make([]string, 0, len(seen))
	for packageDirectory := range seen {
		if _, hasProduction := index.byPackage[packageDirectory]; hasProduction {
			roots = append(roots, packageDirectory)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

// remoteGoProductionImportedDirectories 收集一个生产包适用源码中的本地依赖目录。
func (snapshot *remoteGitTreeSnapshot) remoteGoProductionImportedDirectories(
	directory string,
	index remoteGoProductionIndex,
	profile remoteGoBuildProfile,
) ([]string, error) {
	if _, hasProduction := index.byPackage[directory]; !hasProduction {
		return nil, nil
	}
	paths := make([]string, 0)
	for filePath, source := range snapshot.goSources {
		if remoteProductionGoSourceInDirectory(filePath, source, directory, profile) {
			paths = append(paths, filePath)
		}
	}
	sort.Strings(paths)
	imports := make([]string, 0)
	for _, filePath := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), filePath, snapshot.goSources[filePath], parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("parse imported production runtime source %q: %w", filePath, err)
		}
		for _, importPath := range remoteGoTestImports(file) {
			if importedDirectory, ok := snapshot.resolveLocalGoImport(importPath); ok {
				imports = append(imports, importedDirectory)
			}
		}
	}
	return imports, nil
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
			addRemoteGoProductionDeclaration(index.byPackage[directory], directory, filePath, file, declaration)
		}
	}
	return index, nil
}

func addRemoteGoProductionDeclaration(
	packageDeclarations map[string][]remoteGoProductionDeclaration,
	directory, filePath string,
	file *ast.File,
	declaration ast.Decl,
) {
	entry := remoteGoProductionDeclaration{directory: directory, filePath: filePath, file: file, decl: declaration}
	if remoteGoProductionInitializer(declaration) {
		packageDeclarations["\x00remote-initializer"] = append(packageDeclarations["\x00remote-initializer"], entry)
	}
	function, ok := declaration.(*ast.FuncDecl)
	if !ok || function.Name == nil {
		return
	}
	entry.decl = function
	packageDeclarations[function.Name.Name] = append(packageDeclarations[function.Name.Name], entry)
}

func remoteGoProductionInitializer(declaration ast.Decl) bool {
	switch declaration := declaration.(type) {
	case *ast.GenDecl:
		return declaration.Tok == token.VAR
	case *ast.FuncDecl:
		return declaration.Name != nil && declaration.Name.Name == "init" && declaration.Recv == nil
	default:
		return false
	}
}

// inspectGoProductionRuntimeCalls 扫描一个生产声明并返回其可达调用目标与观察范围。
func (snapshot *remoteGitTreeSnapshot) inspectGoProductionRuntimeCalls(
	call remoteGoProductionCall,
	targetDirectory string,
	index remoteGoProductionIndex,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	imports := remoteGoTestImports(call.file)
	if remoteGoDotImportWholeTree(imports) {
		return remoteGoTestScopeTree, nil, nil
	}
	if snapshot.remoteGoSensitiveObservedAliasInPackage(call.directory, profile, false) {
		return remoteGoTestScopeTree, nil, nil
	}
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
	case "Args", "Stdin", "Stdout", "Stderr", "Environ":
		return true
	default:
		return false
	}
}

// remoteGoSensitiveObservedAliasInPackage 扫描同包文件中的敏感函数别名。
// 别名可在 helper 文件初始化、在 target 文件调用，故必须提升到包级检查。
func (snapshot *remoteGitTreeSnapshot) remoteGoSensitiveObservedAliasInPackage(
	directory string,
	profile remoteGoBuildProfile,
	includeTests bool,
) bool {
	cacheKey := fmt.Sprintf("%t:%s:%s", includeTests, profile.cacheKey(), directory)
	snapshot.cacheMu.Lock()
	if snapshot.remoteObservedAliasCache == nil {
		snapshot.remoteObservedAliasCache = make(map[string]bool)
	}
	if cached, ok := snapshot.remoteObservedAliasCache[cacheKey]; ok {
		snapshot.cacheMu.Unlock()
		return cached
	}
	snapshot.cacheMu.Unlock()
	found := snapshot.remoteGoSensitiveObservedAliasInSources(directory, profile, includeTests)
	snapshot.cacheMu.Lock()
	snapshot.remoteObservedAliasCache[cacheKey] = found
	snapshot.cacheMu.Unlock()
	return found
}

// remoteGoSensitiveObservedAliasInSources 检查同包适用源码中的敏感别名。
func (snapshot *remoteGitTreeSnapshot) remoteGoSensitiveObservedAliasInSources(directory string, profile remoteGoBuildProfile, includeTests bool) bool {
	for filePath, source := range snapshot.goSources {
		if path.Dir(filePath) != directory || path.Ext(filePath) != ".go" {
			continue
		}
		if !includeTests && strings.HasSuffix(filePath, "_test.go") {
			continue
		}
		if !remoteGoSourceAppliesLinuxAMD64WithProfile(filePath, source, profile) {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filePath, source, 0)
		if err != nil || remoteGoSensitiveObservedAliasInFile(file) {
			return true
		}
	}
	return false
}

func remoteGoSensitiveObservedAliasInFile(file *ast.File) bool {
	if file == nil {
		return true
	}
	imports := remoteGoTestImports(file)
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if found {
			return false
		}
		switch declaration := node.(type) {
		case *ast.ValueSpec:
			found = remoteGoSensitiveObservedAliasExpressions(declaration.Values, imports)
		case *ast.AssignStmt:
			found = remoteGoSensitiveObservedAliasExpressions(declaration.Rhs, imports)
		}
		return !found
	})
	return found
}

func remoteGoSensitiveObservedAliasExpressions(values []ast.Expr, imports map[string]string) bool {
	for _, value := range values {
		if remoteGoSensitiveObservedAliasExpression(value, imports) {
			return true
		}
	}
	return false
}

func remoteGoSensitiveObservedAliasExpression(expression ast.Expr, imports map[string]string) bool {
	if expression == nil {
		return false
	}
	// A direct call such as os.ReadFile("fixture.txt") is handled by the
	// path-aware call scanner and remains selector-scoped. The same selector
	// used as a function value (including inside a composite literal or a
	// parenthesized expression) has an unknown invocation/input boundary and
	// must widen the runtime observation to the whole tree.
	if selector, ok := expression.(*ast.SelectorExpr); ok && remoteGoSensitiveSelector(selector, imports) {
		return true
	}
	return remoteGoSensitiveAliasSelectorFound(expression, imports)
}

// remoteGoSensitiveAliasSelectorFound 在复合表达式中识别非直接调用的敏感 selector。
func remoteGoSensitiveAliasSelectorFound(expression ast.Expr, imports map[string]string) bool {
	callCallees := remoteGoSensitiveCallCallees(expression)
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		if found || node == expression {
			return !found
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || !remoteGoSensitiveSelector(selector, imports) {
			return true
		}
		if _, callee := callCallees[selector]; callee {
			return true
		}
		found = true
		return false
	})
	return found
}

// remoteGoSensitiveCallCallees 列出应交由静态调用扫描的 selector。
func remoteGoSensitiveCallCallees(expression ast.Expr) map[*ast.SelectorExpr]struct{} {
	callees := make(map[*ast.SelectorExpr]struct{})
	ast.Inspect(expression, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				callees[selector] = struct{}{}
			}
		}
		return true
	})
	return callees
}

func remoteGoSensitiveSelector(selector *ast.SelectorExpr, imports map[string]string) bool {
	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	return remoteGoWholeTreeImport(imports[packageName.Name])
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
		return remoteGoNonLocalProductionScope(importPath), nil, nil
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
	if remoteGoWholeTreeImport(importPath) {
		switch importPath {
		case "os/exec", "syscall", "golang.org/x/sys/unix", "reflect":
			return true
		case "os":
			return remoteGoWholeTreeOSMethod(method)
		}
	}
	return false
}

func remoteGoWholeTreeImport(importPath string) bool {
	switch importPath {
	case "os", "os/exec", "syscall", "golang.org/x/sys/unix", "reflect":
		return true
	default:
		return false
	}
}

// remoteGoStandardLibraryImport 识别由已绑定 Go toolchain 提供的标准库导入。
func remoteGoStandardLibraryImport(importPath string) bool {
	standardRoot, _, _ := strings.Cut(importPath, "/")
	return standardRoot != "" && standardRoot != "C" && !strings.Contains(standardRoot, ".")
}

// remoteGoNonLocalProductionScope 保留外部依赖 fail-closed，并允许已绑定标准库精确复用。
func remoteGoNonLocalProductionScope(importPath string) remoteGoTestScope {
	if remoteGoStandardLibraryImport(importPath) {
		return remoteGoTestScopeSelector
	}
	return remoteGoTestScopeTree
}

func remoteGoDotImportWholeTree(imports map[string]string) bool {
	return remoteGoWholeTreeImport(imports["."])
}

func remoteGoWholeTreeOSMethod(method string) bool {
	switch method {
	case "Chdir", "Getenv", "LookupEnv", "Environ", "ExpandEnv", "Getwd", "Executable", "UserHomeDir", "Hostname", "StartProcess", "Args":
		return true
	default:
		return false
	}
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
		return fmt.Errorf("production runtime observation path %q escapes canonical worker source", staticPath)
	}
	if kind != "tree" && kind != "glob" {
		if _, exists := snapshot.byPath[observedPath]; !exists {
			return fmt.Errorf("static production runtime observation %q is absent from Git tree", observedPath)
		}
	}
	return snapshot.addRemoteGitObservedPath(kind, observedPath, selected)
}

// addGoTestFileObservedEntriesScoped 收集选中 Go 测试声明的保守运行时观察。
func (snapshot *remoteGitTreeSnapshot) addGoTestFileObservedEntriesScoped(
	directory string,
	file *ast.File,
	observed ast.Node,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (remoteGoTestScope, error) {
	imports := remoteGoTestImports(file)
	if remoteGoDotImportWholeTree(imports) {
		return remoteGoTestScopeTree, nil
	}
	if snapshot.remoteGoSensitiveObservedAliasInPackage(directory, profile, true) {
		return remoteGoTestScopeTree, nil
	}
	scope := remoteGoTestScopeSelector
	var visitErr error
	ast.Inspect(observed, func(node ast.Node) bool {
		if visitErr != nil {
			return false
		}
		if selector, ok := node.(*ast.SelectorExpr); ok && remoteGoProductionEnvironmentSelector(selector, imports) {
			scope = remoteGoTestScopeTree
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
	if kind != "tree" && kind != "glob" {
		if _, exists := snapshot.byPath[resolvedPath]; !exists {
			return fmt.Errorf("static Go test file observation %q is absent from Git tree", resolvedPath)
		}
	}
	return snapshot.addRemoteGitObservedPath(kind, resolvedPath, selected)
}

// remoteGoTestObservationScoped 分类测试中的候选树运行时观察，并保守选择三级作用域。
func remoteGoTestObservationScoped(directory string, call *ast.CallExpr, file *ast.File, imports map[string]string) (kind string, observedPath string, scope remoteGoTestScope) {
	if remoteGoDotImportWholeTree(imports) {
		return "tree", "", remoteGoTestScopeTree
	}
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
	if remoteGoDotImportWholeTree(imports) {
		return "tree", "", true
	}
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
	return remoteGoTestStaticObservationValue(call, file, imports, importPath, method, index)
}

// remoteGoTestStaticObservationValue 解析已识别的静态路径参数，无法闭合时绑定整树。
func remoteGoTestStaticObservationValue(
	call *ast.CallExpr,
	file *ast.File,
	imports map[string]string,
	importPath, method string,
	index int,
) (string, string, bool) {
	value, ok := remoteGoTestStringArgument(call, index)
	if !ok {
		return remoteGoTestObservationKind(method), "", true
	}
	if importPath == "io/fs" {
		value, ok = remoteGoTestFSObservationPath(call, file, imports, value)
		if !ok {
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
		return remoteGoWholeTreeOSMethod(method)
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
