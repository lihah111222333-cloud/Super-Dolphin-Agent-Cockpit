package remoteci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strings"
)

type remoteGoObservedAliasCacheEntry struct {
	names         map[string]struct{}
	selectorNames map[string]struct{}
	fallback      bool
}

type remoteGoObservedAliasCallScope struct {
	packageAliases remoteGoObservedAliasCacheEntry
	localAliases   remoteGoObservedAliasCacheEntry
	localBindings  map[string]struct{}
}

type remoteGoObservedAliasDefinition struct {
	name       string
	expression ast.Expr
	imports    map[string]string
}

// remoteGoNamedTypeConversion 识别当前包已索引的命名类型转换。
func remoteGoNamedTypeConversion(expression ast.Expr, directory string, index remoteGoProductionIndex) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}
	for _, declaration := range index.byPackage[directory][identifier.Name] {
		if general, ok := declaration.decl.(*ast.GenDecl); ok && general.Tok == token.TYPE {
			return true
		}
	}
	return false
}

// remoteGoProductionIndexCacheEntry 保存一个 snapshot/profile 的生产索引或其错误。
// 索引只读返回，错误也缓存，避免同一失败输入被每个 selector 重复解析。
type remoteGoProductionIndexCacheEntry struct {
	index remoteGoProductionIndex
	err   error
}

type remoteGoProductionImportsCacheEntry struct {
	imports []string
	err     error
}

type remoteGoProductionRuntimeCacheEntry struct {
	scope   remoteGoTestScope
	entries []remoteGitTreeEntry
	sources []remoteGoTestSource
	err     error
}

// addGoProductionRuntimeObservedEntries 缓存单个测试声明的生产运行时观察闭包。
// 缓存严格归属当前 exact-tree snapshot，不跨 run、tree 或 SQLite 复用。
func (snapshot *remoteGitTreeSnapshot) addGoProductionRuntimeObservedEntries(
	directory string,
	root remoteGoTestDeclaration,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (remoteGoTestScope, error) {
	scope, _, err := snapshot.goProductionRuntimeInputs(directory, root, selected, profile)
	return scope, err
}

// goProductionRuntimeInputs 返回同一次生产可达性扫描的观察文件和声明片段；
// 两者必须共享 snapshot cache，避免不同算法看到不同闭包。
func (snapshot *remoteGitTreeSnapshot) goProductionRuntimeInputs(
	directory string,
	root remoteGoTestDeclaration,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (remoteGoTestScope, []remoteGoTestSource, error) {
	key := fmt.Sprintf("%s:%s:%p", profile.cacheKey(), directory, root.declaration)
	snapshot.productionRuntimeMu.Lock()
	defer snapshot.productionRuntimeMu.Unlock()
	if snapshot.productionRuntimeCache == nil {
		snapshot.productionRuntimeCache = make(map[string]remoteGoProductionRuntimeCacheEntry)
	}
	if cached, ok := snapshot.productionRuntimeCache[key]; ok {
		addRemoteGoProductionRuntimeEntries(selected, cached.entries)
		return cached.scope, cloneRemoteGoTestSources(cached.sources), cached.err
	}
	snapshot.cacheMu.Lock()
	snapshot.productionRuntimeComputations++
	snapshot.cacheMu.Unlock()
	observed := make(map[string]remoteGitTreeEntry)
	scope, sources, err := snapshot.buildGoProductionRuntimeObservedEntries(directory, root, observed, profile)
	entries := sortedRemoteGitTreeEntries(observed)
	snapshot.productionRuntimeCache[key] = remoteGoProductionRuntimeCacheEntry{scope: scope, entries: entries, sources: cloneRemoteGoTestSources(sources), err: err}
	addRemoteGoProductionRuntimeEntries(selected, entries)
	return scope, sources, err
}

func cloneRemoteGoTestSources(sources []remoteGoTestSource) []remoteGoTestSource {
	cloned := make([]remoteGoTestSource, len(sources))
	for index, source := range sources {
		cloned[index] = remoteGoTestSource{path: source.path, text: append([]byte(nil), source.text...)}
	}
	return cloned
}

func addRemoteGoProductionRuntimeEntries(selected map[string]remoteGitTreeEntry, entries []remoteGitTreeEntry) {
	for _, entry := range entries {
		selected[entry.path] = entry
	}
}

// addRemoteGoProductionDeclaration 将函数、类型与初始化声明加入生产运行时索引。
func addRemoteGoProductionDeclaration(packageDeclarations map[string][]remoteGoProductionDeclaration, directory, filePath string, file *ast.File, declaration ast.Decl) {
	entry := remoteGoProductionDeclaration{directory: directory, filePath: filePath, file: file, decl: declaration}
	if remoteGoProductionInitializer(declaration) {
		packageDeclarations["\x00remote-initializer"] = append(packageDeclarations["\x00remote-initializer"], entry)
	}
	if general, ok := declaration.(*ast.GenDecl); ok && general.Tok == token.TYPE {
		addRemoteGoProductionTypeDeclarations(packageDeclarations, entry, general)
		return
	}
	function, ok := declaration.(*ast.FuncDecl)
	if !ok || function.Name == nil {
		return
	}
	entry.decl = function
	packageDeclarations[function.Name.Name] = append(packageDeclarations[function.Name.Name], entry)
}

func addRemoteGoProductionTypeDeclarations(packageDeclarations map[string][]remoteGoProductionDeclaration, entry remoteGoProductionDeclaration, declaration *ast.GenDecl) {
	for _, spec := range declaration.Specs {
		if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name != nil {
			packageDeclarations[typeSpec.Name.Name] = append(packageDeclarations[typeSpec.Name.Name], entry)
		}
	}
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

// cachedRemoteGoProductionIndex 在 snapshot 内按 profile 串行构建只读生产索引。
// 该缓存不跨 tree、进程或 SQLite；私有调用链只能读取，禁止修改共享 map/slice。
func (snapshot *remoteGitTreeSnapshot) cachedRemoteGoProductionIndex(profile remoteGoBuildProfile) (remoteGoProductionIndex, error) {
	key := profile.cacheKey()
	snapshot.productionIndexMu.Lock()
	defer snapshot.productionIndexMu.Unlock()
	if snapshot.productionIndexCache == nil {
		snapshot.productionIndexCache = make(map[string]remoteGoProductionIndexCacheEntry)
	}
	if cached, ok := snapshot.productionIndexCache[key]; ok {
		return cached.index, cached.err
	}
	snapshot.cacheMu.Lock()
	snapshot.productionIndexComputations++
	snapshot.cacheMu.Unlock()
	index, err := snapshot.buildRemoteGoProductionIndex(profile)
	cached := remoteGoProductionIndexCacheEntry{index: index, err: err}
	snapshot.productionIndexCache[key] = cached
	return index, err
}

// remoteGoObservedAliasScope 将包级别名与当前声明的局部绑定分离，避免同名 receiver 误命中。
func (snapshot *remoteGitTreeSnapshot) remoteGoObservedAliasScope(directory string, profile remoteGoBuildProfile, includeTests bool, observed ast.Node, imports map[string]string) remoteGoObservedAliasCallScope {
	return remoteGoObservedAliasCallScope{
		packageAliases: snapshot.remoteGoObservedAliasesInPackage(directory, profile, includeTests),
		localAliases:   resolveRemoteGoObservedAliasNames(remoteGoObservedAliasDefinitions(observed, imports)),
		localBindings:  remoteGoObservedLocalBindings(observed),
	}
}

func (scope remoteGoObservedAliasCallScope) matches(call *ast.CallExpr) bool {
	root, selector, ok := remoteGoAliasCallRoot(call)
	if !ok || scope.packageAliases.fallback {
		return scope.packageAliases.fallback
	}
	aliases := scope.packageAliases
	if _, local := scope.localBindings[root]; local {
		aliases = scope.localAliases
	}
	if selector {
		_, found := aliases.selectorNames[root]
		return found
	}
	_, found := aliases.names[root]
	return found
}

// remoteGoObservedAliasesInPackage 缓存同包适用源码中可被调用的敏感别名根。
func (snapshot *remoteGitTreeSnapshot) remoteGoObservedAliasesInPackage(directory string, profile remoteGoBuildProfile, includeTests bool) remoteGoObservedAliasCacheEntry {
	cacheKey := fmt.Sprintf("%t:%s:%s", includeTests, profile.cacheKey(), directory)
	snapshot.cacheMu.Lock()
	if snapshot.remoteObservedAliasCache == nil {
		snapshot.remoteObservedAliasCache = make(map[string]remoteGoObservedAliasCacheEntry)
	}
	if cached, ok := snapshot.remoteObservedAliasCache[cacheKey]; ok {
		snapshot.cacheMu.Unlock()
		return cached
	}
	snapshot.cacheMu.Unlock()
	found := snapshot.remoteGoObservedAliasesInSources(directory, profile, includeTests)
	snapshot.cacheMu.Lock()
	snapshot.remoteObservedAliasCache[cacheKey] = found
	snapshot.cacheMu.Unlock()
	return found
}

// remoteGoObservedAliasesInSources 收集敏感函数值及其传递别名；解析失败必须 fail-fast。
func (snapshot *remoteGitTreeSnapshot) remoteGoObservedAliasesInSources(directory string, profile remoteGoBuildProfile, includeTests bool) remoteGoObservedAliasCacheEntry {
	definitions := make([]remoteGoObservedAliasDefinition, 0)
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
		if err != nil {
			return remoteGoObservedAliasCacheEntry{fallback: true}
		}
		definitions = append(definitions, remoteGoObservedPackageAliasDefinitions(file)...)
	}
	return resolveRemoteGoObservedAliasNames(definitions)
}

// remoteGoObservedPackageAliasDefinitions 只收集可跨文件引用的包级函数值别名。
func remoteGoObservedPackageAliasDefinitions(file *ast.File) []remoteGoObservedAliasDefinition {
	definitions := make([]remoteGoObservedAliasDefinition, 0)
	imports := remoteGoTestImports(file)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			if value, ok := spec.(*ast.ValueSpec); ok {
				definitions = append(definitions, remoteGoObservedValueSpecDefinitions(value, imports)...)
			}
		}
	}
	return definitions
}

func remoteGoObservedAliasDefinitions(observed ast.Node, imports map[string]string) []remoteGoObservedAliasDefinition {
	definitions := make([]remoteGoObservedAliasDefinition, 0)
	ast.Inspect(observed, func(node ast.Node) bool {
		switch declaration := node.(type) {
		case *ast.ValueSpec:
			definitions = append(definitions, remoteGoObservedValueSpecDefinitions(declaration, imports)...)
		case *ast.AssignStmt:
			definitions = append(definitions, remoteGoObservedAssignmentDefinitions(declaration, imports)...)
		}
		return true
	})
	return definitions
}

// remoteGoObservedLocalBindings 收集当前声明内会遮蔽同名包变量的局部绑定。
func remoteGoObservedLocalBindings(observed ast.Node) map[string]struct{} {
	bindings := make(map[string]struct{})
	ast.Inspect(observed, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncDecl:
			addRemoteGoFieldBindings(bindings, value.Recv, value.Type.Params, value.Type.Results)
		case *ast.FuncLit:
			addRemoteGoFieldBindings(bindings, value.Type.Params, value.Type.Results)
		case *ast.ValueSpec:
			addRemoteGoIdentifierBindings(bindings, value.Names)
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE {
				addRemoteGoExpressionBindings(bindings, value.Lhs)
			}
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				addRemoteGoExpressionBindings(bindings, []ast.Expr{value.Key, value.Value})
			}
		}
		return true
	})
	return bindings
}

func addRemoteGoFieldBindings(bindings map[string]struct{}, lists ...*ast.FieldList) {
	for _, list := range lists {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			addRemoteGoIdentifierBindings(bindings, field.Names)
		}
	}
}

func addRemoteGoIdentifierBindings(bindings map[string]struct{}, names []*ast.Ident) {
	for _, name := range names {
		if name != nil && name.Name != "_" {
			bindings[name.Name] = struct{}{}
		}
	}
}

func addRemoteGoExpressionBindings(bindings map[string]struct{}, expressions []ast.Expr) {
	for _, expression := range expressions {
		if identifier, ok := expression.(*ast.Ident); ok {
			addRemoteGoIdentifierBindings(bindings, []*ast.Ident{identifier})
		}
	}
}

func remoteGoObservedValueSpecDefinitions(value *ast.ValueSpec, imports map[string]string) []remoteGoObservedAliasDefinition {
	names := make([]ast.Expr, len(value.Names))
	for index, name := range value.Names {
		names[index] = name
	}
	return remoteGoObservedAliasDefinitionsForValues(names, value.Values, imports)
}

func remoteGoObservedAssignmentDefinitions(value *ast.AssignStmt, imports map[string]string) []remoteGoObservedAliasDefinition {
	return remoteGoObservedAliasDefinitionsForValues(value.Lhs, value.Rhs, imports)
}

func remoteGoObservedAliasDefinitionsForValues(names, values []ast.Expr, imports map[string]string) []remoteGoObservedAliasDefinition {
	if len(names) != len(values) {
		return nil
	}
	definitions := make([]remoteGoObservedAliasDefinition, 0, len(names))
	for index, candidate := range names {
		identifier, ok := candidate.(*ast.Ident)
		if !ok || identifier.Name == "_" {
			continue
		}
		definitions = append(definitions, remoteGoObservedAliasDefinition{name: identifier.Name, expression: values[index], imports: imports})
	}
	return definitions
}

// resolveRemoteGoObservedAliasNames 固定点解析直接敏感函数值与传递别名，避免漏掉真实动态调用。
func resolveRemoteGoObservedAliasNames(definitions []remoteGoObservedAliasDefinition) remoteGoObservedAliasCacheEntry {
	aliases := remoteGoObservedAliasCacheEntry{names: make(map[string]struct{}), selectorNames: make(map[string]struct{})}
	changed := true
	for changed {
		changed = false
		for _, definition := range definitions {
			if _, exists := aliases.names[definition.name]; exists {
				continue
			}
			selector, found := remoteGoObservedAliasExpressionKind(definition.expression, definition.imports, aliases)
			if !found {
				continue
			}
			aliases.names[definition.name] = struct{}{}
			if selector {
				aliases.selectorNames[definition.name] = struct{}{}
			}
			changed = true
		}
	}
	return aliases
}

// remoteGoObservedAliasExpressionKind 区分可直接调用的函数别名与承载函数值的复合别名。
func remoteGoObservedAliasExpressionKind(expression ast.Expr, imports map[string]string, aliases remoteGoObservedAliasCacheEntry) (bool, bool) {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		if remoteGoSensitiveSelector(value, imports) {
			return false, true
		}
	case *ast.Ident:
		_, found := aliases.names[value.Name]
		_, selector := aliases.selectorNames[value.Name]
		return selector, found
	case *ast.ParenExpr:
		return remoteGoObservedAliasExpressionKind(value.X, imports, aliases)
	}
	return true, remoteGoSensitiveAliasSelectorFound(expression, imports) || remoteGoExpressionReferencesObservedAlias(expression, aliases.names)
}

func remoteGoExpressionReferencesObservedAlias(expression ast.Expr, names map[string]struct{}) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok {
			_, found = names[identifier.Name]
		}
		return !found
	})
	return found
}

func remoteGoAliasCallRoot(call *ast.CallExpr) (string, bool, bool) {
	if call == nil {
		return "", false, false
	}
	_, selector := call.Fun.(*ast.SelectorExpr)
	root, ok := remoteGoExpressionRootIdentifier(call.Fun)
	return root, selector, ok
}

// remoteGoExpressionRootIdentifier 提取函数值、字段或索引调用的根变量名。
func remoteGoExpressionRootIdentifier(expression ast.Expr) (string, bool) {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name, value.Name != ""
	case *ast.SelectorExpr:
		return remoteGoExpressionRootIdentifier(value.X)
	case *ast.IndexExpr:
		return remoteGoExpressionRootIdentifier(value.X)
	case *ast.IndexListExpr:
		return remoteGoExpressionRootIdentifier(value.X)
	case *ast.ParenExpr:
		return remoteGoExpressionRootIdentifier(value.X)
	default:
		return "", false
	}
}
