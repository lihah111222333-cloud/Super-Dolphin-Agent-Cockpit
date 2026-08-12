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
	filePath  string
	file      *ast.File
	decl      ast.Node
	testRoot  bool
}

// buildGoProductionRuntimeObservedEntries 计算单个声明的生产运行时观察闭包。
func (snapshot *remoteGitTreeSnapshot) buildGoProductionRuntimeObservedEntries(
	directory string,
	root remoteGoTestDeclaration,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (remoteGoTestScope, []remoteGoTestSource, error) {
	index, err := snapshot.remoteGoProductionIndex(profile)
	if err != nil {
		return remoteGoTestScopeTree, nil, err
	}
	queue := []remoteGoProductionCall{{directory: directory, filePath: root.filePath, file: root.file, decl: root.declaration, testRoot: true}}
	packageRoots, err := snapshot.remoteGoProductionRuntimeRoots(directory, root.file, index, profile)
	if err != nil {
		return remoteGoTestScopeTree, nil, err
	}
	for _, packageDirectory := range packageRoots {
		for _, initializer := range index.byPackage[packageDirectory]["\x00remote-initializer"] {
			queue = append(queue, remoteGoProductionCall{directory: initializer.directory, filePath: initializer.filePath, file: initializer.file, decl: initializer.decl})
		}
	}
	visited := make(map[string]struct{})
	var sources []remoteGoTestSource
	scope := remoteGoTestScopeSelector
	for len(queue) > 0 {
		call := queue[0]
		queue = queue[1:]
		key := fmt.Sprintf("%p", call.decl)
		if _, ok := visited[key]; ok {
			continue
		}
		visited[key] = struct{}{}
		sources, err = snapshot.appendRemoteGoProductionCallSource(sources, call, selected)
		if err != nil {
			return remoteGoTestScopeTree, nil, err
		}
		callScope, targets, err := snapshot.inspectGoProductionRuntimeCalls(call, directory, index, selected, profile)
		if err != nil {
			return remoteGoTestScopeTree, nil, err
		}
		scope = scope.widen(callScope)
		if scope == remoteGoTestScopeTree {
			return scope, nil, nil
		}
		queue = append(queue, targets...)
	}
	return scope, sources, nil
}

func (snapshot *remoteGitTreeSnapshot) appendRemoteGoProductionCallSource(sources []remoteGoTestSource, call remoteGoProductionCall, selected map[string]remoteGitTreeEntry) ([]remoteGoTestSource, error) {
	if call.testRoot {
		return sources, nil
	}
	source, err := snapshot.remoteGoProductionCallSource(call, selected)
	if err != nil {
		return nil, err
	}
	return append(sources, source), nil
}

// remoteGoProductionCallSource 把可达生产声明而非整包文件加入 PASS 语义，
// 同时保留声明所属文件的 go:embed 资产绑定。
func (snapshot *remoteGitTreeSnapshot) remoteGoProductionCallSource(call remoteGoProductionCall, selected map[string]remoteGitTreeEntry) (remoteGoTestSource, error) {
	source, ok := snapshot.goSources[call.filePath]
	if !ok {
		return remoteGoTestSource{}, fmt.Errorf("remote Go production source %q is absent", call.filePath)
	}
	if err := snapshot.addGoEmbedEntries(path.Dir(call.filePath), source, selected); err != nil {
		return remoteGoTestSource{}, err
	}
	declarationNode, ok := call.decl.(ast.Decl)
	if !ok {
		return remoteGoTestSource{}, fmt.Errorf("remote Go production source %q has non-declaration semantic root", call.filePath)
	}
	declaration := remoteGoTestDeclaration{filePath: call.filePath, source: source, file: call.file, declaration: declarationNode}
	return remoteGoTestSource{path: call.filePath, text: remoteGoTestDeclarationText(declaration)}, nil
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
	key := profile.cacheKey() + ":" + directory
	snapshot.productionImportsMu.Lock()
	defer snapshot.productionImportsMu.Unlock()
	if snapshot.productionImportsCache == nil {
		snapshot.productionImportsCache = make(map[string]remoteGoProductionImportsCacheEntry)
	}
	if cached, ok := snapshot.productionImportsCache[key]; ok {
		return append([]string(nil), cached.imports...), cached.err
	}
	snapshot.cacheMu.Lock()
	snapshot.productionImportsComputations++
	snapshot.cacheMu.Unlock()
	imports, err := snapshot.buildRemoteGoProductionImportedDirectories(directory, index, profile)
	snapshot.productionImportsCache[key] = remoteGoProductionImportsCacheEntry{imports: append([]string(nil), imports...), err: err}
	return imports, err
}

// buildRemoteGoProductionImportedDirectories 解析单个生产包适用源码的本地导入目录。
func (snapshot *remoteGitTreeSnapshot) buildRemoteGoProductionImportedDirectories(
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

// inspectGoProductionRuntimeCalls 扫描一个生产声明并返回其可达调用目标与观察范围。
func (snapshot *remoteGitTreeSnapshot) inspectGoProductionRuntimeCalls(
	call remoteGoProductionCall,
	targetDirectory string,
	index remoteGoProductionIndex,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	imports := remoteGoTestImports(call.file)
	aliasScope := snapshot.remoteGoObservedAliasScope(call.directory, profile, false, call.decl, imports)
	if remoteGoDotImportWholeTree(imports) {
		return remoteGoTestScopeTree, nil, nil
	}
	targets := make([]remoteGoProductionCall, 0)
	scope := remoteGoTestScopeSelector
	var visitErr error
	ast.Inspect(call.decl, func(node ast.Node) bool {
		if visitErr != nil || scope == remoteGoTestScopeTree {
			return false
		}
		expression, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if aliasScope.matches(expression) {
			scope = remoteGoTestScopeTree
			return false
		}
		callScope, observedTargets, err := snapshot.inspectGoProductionCall(
			expression,
			call.decl,
			call.directory,
			targetDirectory,
			call.file,
			imports,
			index,
			selected,
			call.testRoot,
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
	importPath := imports[packageName.Name]
	if importPath == "os" && remoteGoBoundEnvironmentMethod(selector.Sel.Name) {
		return false
	}
	return remoteGoWholeTreeImport(importPath)
}

// inspectGoProductionCall 解析单个调用的静态读取、局部递归或 fail-safe 范围。
func (snapshot *remoteGitTreeSnapshot) inspectGoProductionCall(
	call *ast.CallExpr,
	observed ast.Node,
	callDirectory string,
	targetDirectory string,
	file *ast.File,
	imports map[string]string,
	index remoteGoProductionIndex,
	selected map[string]remoteGitTreeEntry,
	testRoot bool,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	if importPath, method, ok := remoteGoTestSelector(call, imports); ok {
		return snapshot.inspectImportedGoProductionCall(call, callDirectory, targetDirectory, file, imports, index, selected, importPath, method, testRoot)
	}
	if remoteGoPureTypeConversion(call.Fun) {
		return remoteGoTestScopeSelector, nil, nil
	}
	if remoteGoNamedTypeConversion(call.Fun, callDirectory, index) {
		return remoteGoTestScopeSelector, nil, nil
	}
	if remoteGoImmediatelyInvokedFunctionLiteral(call.Fun) {
		return remoteGoTestScopeSelector, nil, nil
	}
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		return snapshot.localGoProductionReceiverCall(callDirectory, imports, index, selector.Sel.Name)
	}
	identifier, ok := remoteGoCalledIdentifier(call.Fun)
	if !ok {
		return remoteGoTestScopeCompileClosure, nil, nil
	}
	if targets, resolved := remoteGoRangeFunctionValueCalls(observed, callDirectory, index, identifier.Name); resolved {
		return remoteGoTestScopeSelector, targets, nil
	}
	return inspectLocalGoProductionCall(callDirectory, index, identifier.Name, testRoot)
}

func remoteGoImmediatelyInvokedFunctionLiteral(expression ast.Expr) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			_, literal := expression.(*ast.FuncLit)
			return literal
		}
		expression = parenthesized.X
	}
}

// inspectImportedGoProductionCall 处理导入包调用并递归进入本地生产函数。
func (snapshot *remoteGitTreeSnapshot) inspectImportedGoProductionCall(
	call *ast.CallExpr,
	callDirectory string,
	targetDirectory string,
	file *ast.File,
	imports map[string]string,
	index remoteGoProductionIndex,
	selected map[string]remoteGitTreeEntry,
	importPath, method string,
	testRoot bool,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	if importPath == "" {
		if remoteGoTestingMethod(method) {
			return remoteGoTestScopeSelector, nil, nil
		}
		return snapshot.localGoProductionReceiverCall(callDirectory, imports, index, method)
	}
	if scope, classified := remoteGoClassifiedImportedCallScope(importPath, method); classified {
		return scope, nil, nil
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
		return remoteGoTestRootNonLocalScope(importPath, testRoot), nil, nil
	}
	declarations := index.byPackage[localDirectory][method]
	if len(declarations) == 0 {
		return remoteGoMissingImportedProductionCallScope(call.Fun), nil, nil
	}
	targets := make([]remoteGoProductionCall, 0, len(declarations))
	for _, declaration := range declarations {
		targets = append(targets, remoteGoProductionCall{directory: localDirectory, filePath: declaration.filePath, file: declaration.file, decl: declaration.decl})
	}
	return remoteGoTestScopeSelector, targets, nil
}

func remoteGoClassifiedImportedCallScope(importPath, method string) (remoteGoTestScope, bool) {
	if remoteGoAuditedPureExternalCall(importPath, method) {
		return remoteGoTestScopeSelector, true
	}
	if remoteGoProductionWholeTreeCall(importPath, method) {
		return remoteGoTestScopeTree, true
	}
	if remoteGoDeclarativeExternalCall(importPath, method) {
		return remoteGoTestScopeSelector, true
	}
	return remoteGoTestScopeSelector, false
}

// remoteGoAuditedPureExternalCall 只放行不读取候选源码树的纯标准库反射查询。
// 动态反射入口仍由 remoteGoProductionWholeTreeCall 绑定整树。
func remoteGoAuditedPureExternalCall(importPath, method string) bool {
	if importPath != "reflect" {
		return false
	}
	switch method {
	case "DeepEqual", "TypeFor", "TypeOf":
		return true
	default:
		return false
	}
}

// remoteGoDeclarativeExternalCall 仅允许构造 Fx option 的纯声明调用保持 selector 范围。
// fx.New 等会真正执行 option/constructor 的入口不在此白名单，仍然 fail-closed。
func remoteGoDeclarativeExternalCall(importPath, method string) bool {
	if importPath == "github.com/google/uuid" && method == "NewString" {
		return true
	}
	if importPath != "go.uber.org/fx" {
		return false
	}
	switch method {
	case "Annotate", "As", "Decorate", "Error", "Invoke", "Module", "Options", "ParamTags", "Populate", "Private", "Provide", "Replace", "ResultTags", "Self", "Supply", "WithLogger":
		return true
	default:
		return false
	}
}

func remoteGoTestRootNonLocalScope(importPath string, testRoot bool) remoteGoTestScope {
	if testRoot {
		return remoteGoTestScopeSelector
	}
	return remoteGoNonLocalProductionScope(importPath)
}

// inspectLocalGoProductionCall 解析同包函数调用或无法证明的函数值调用。
func inspectLocalGoProductionCall(
	directory string,
	index remoteGoProductionIndex,
	name string,
	testRoot bool,
) (remoteGoTestScope, []remoteGoProductionCall, error) {
	if remoteGoBuiltinCall(name) {
		return remoteGoTestScopeSelector, nil, nil
	}
	declarations := index.byPackage[directory][name]
	if len(declarations) == 0 {
		if testRoot {
			return remoteGoTestScopeSelector, nil, nil
		}
		return remoteGoTestScopeCompileClosure, nil, nil
	}
	targets := make([]remoteGoProductionCall, 0, len(declarations))
	for _, declaration := range declarations {
		targets = append(targets, remoteGoProductionCall{directory: directory, filePath: declaration.filePath, file: declaration.file, decl: declaration.decl})
	}
	return remoteGoTestScopeSelector, targets, nil
}

func remoteGoPureTypeConversion(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.ArrayType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType, *ast.MapType, *ast.StarExpr, *ast.StructType:
		return true
	case *ast.ParenExpr:
		return remoteGoPureTypeConversion(expression.X)
	default:
		return false
	}
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
	case "Chdir", "Getwd", "Executable", "UserHomeDir", "Hostname", "StartProcess":
		return true
	default:
		return false
	}
}

func remoteGoBoundEnvironmentMethod(method string) bool {
	switch method {
	case "Environ", "ExpandEnv", "Getenv", "LookupEnv":
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
	case "bool", "byte", "complex64", "complex128", "float32", "float64", "int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
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
		if remoteGoBoundRuntimeAbsoluteObservation(staticPath) {
			return nil
		}
		return fmt.Errorf("production runtime observation path %q escapes canonical worker source", staticPath)
	}
	if kind != "tree" && kind != "glob" {
		if _, exists := snapshot.byPath[observedPath]; !exists {
			return fmt.Errorf("static production runtime observation %q is absent from Git tree", observedPath)
		}
	}
	return snapshot.addRemoteGitObservedPath(kind, observedPath, selected)
}

// remoteGoBoundRuntimeAbsoluteObservation 识别由固定 runner 环境约束、而非候选树拥有的内核观察。
func remoteGoBoundRuntimeAbsoluteObservation(staticPath string) bool {
	switch staticPath {
	case "/proc/self/mountinfo", "/proc/sys/kernel/random/boot_id":
		return true
	default:
		return false
	}
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
	aliasScope := snapshot.remoteGoObservedAliasScope(directory, profile, true, observed, imports)
	if remoteGoDotImportWholeTree(imports) {
		return remoteGoTestScopeTree, nil
	}
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
		scope, visitErr = snapshot.addGoTestObservedCall(directory, file, call, imports, selected, aliasScope, scope)
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
	aliasScope remoteGoObservedAliasCallScope,
	currentScope remoteGoTestScope,
) (remoteGoTestScope, error) {
	if aliasScope.matches(call) {
		return remoteGoTestScopeTree, nil
	}
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
