package remoteci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (snapshot *remoteGitTreeSnapshot) buildWorkerExecutionGoIndex() *workerExecutionGoIndex {
	return snapshot.buildWorkerExecutionGoIndexWithKeyStrategy(workerExecutionStableUnitKeys)
}

// buildWorkerExecutionGoIndexWithKeyStrategy 只允许历史回放显式选择旧位置键；生产身份始终使用稳定语义键。
func (snapshot *remoteGitTreeSnapshot) buildWorkerExecutionGoIndexWithKeyStrategy(strategy workerExecutionUnitKeyStrategy) *workerExecutionGoIndex {
	return snapshot.buildWorkerExecutionGoIndexWithStrategies(strategy, false)
}

func (snapshot *remoteGitTreeSnapshot) buildWorkerExecutionGoIndexPreviousGroupedDeclaration() *workerExecutionGoIndex {
	return snapshot.buildWorkerExecutionGoIndexWithStrategies(workerExecutionStableUnitKeys, true)
}

func (snapshot *remoteGitTreeSnapshot) buildWorkerExecutionGoIndexWithStrategies(strategy workerExecutionUnitKeyStrategy, previousGroupedDeclaration bool) *workerExecutionGoIndex {
	index := &workerExecutionGoIndex{
		symbols:                    make(map[string]map[string][]*workerExecutionGoUnit),
		methods:                    make(map[string]map[string][]*workerExecutionGoUnit),
		receiverMethods:            make(map[string]map[string][]*workerExecutionGoUnit),
		initializers:               make(map[string][]*workerExecutionGoUnit),
		routes:                     make(map[string]map[string][]*workerExecutionGoUnit),
		parseErrors:                make(map[string][]error),
		unitKeyStrategy:            strategy,
		previousGroupedDeclaration: previousGroupedDeclaration,
		unitKeyOrdinals:            make(map[string]int),
	}
	paths := make([]string, 0, len(snapshot.goSources))
	for filePath := range snapshot.goSources {
		if path.Ext(filePath) == ".go" && !strings.HasSuffix(filePath, "_test.go") {
			paths = append(paths, filePath)
		}
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		source := snapshot.goSources[filePath]
		if !remoteGoSourceAppliesLinuxAMD64(filePath, source) {
			continue
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, filePath, source, parser.ParseComments)
		directory := path.Dir(filePath)
		if err != nil {
			index.parseErrors[directory] = append(index.parseErrors[directory],
				fmt.Errorf("parse worker execution source %q: %w", filePath, err))
			continue
		}
		imports, err := snapshot.workerExecutionImports(filePath, file)
		if err != nil {
			index.parseErrors[directory] = append(index.parseErrors[directory], err)
			continue
		}
		for _, declaration := range file.Decls {
			index.addDeclaration(filePath, source, fileSet, file.Name.Name, imports, declaration)
		}
	}
	return index
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (snapshot *remoteGitTreeSnapshot) workerExecutionImports(
	filePath string,
	file *ast.File,
) (map[string]workerExecutionGoImport, error) {
	imports := make(map[string]workerExecutionGoImport)
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("parse worker execution import in %q: %w", filePath, err)
		}
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imported := workerExecutionGoImport{importPath: importPath}
		if directory, ok := snapshot.resolveLocalGoImport(importPath); ok {
			imported.local = true
			imported.directory = directory
		}
		imports[name] = imported
	}
	return imports, nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) addDeclaration(
	filePath string,
	source []byte,
	fileSet *token.FileSet,
	packageName string,
	imports map[string]workerExecutionGoImport,
	declaration ast.Decl,
) {
	directory := path.Dir(filePath)
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		index.addFunctionDeclaration(directory, filePath, source, fileSet, packageName, imports, value)
	case *ast.GenDecl:
		index.addGeneralDeclaration(directory, filePath, source, fileSet, packageName, imports, value)
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) addFunctionDeclaration(directory, filePath string, source []byte, fileSet *token.FileSet, packageName string, imports map[string]workerExecutionGoImport, function *ast.FuncDecl) {
	if function.Name == nil {
		return
	}
	receiver := workerExecutionReceiverName(function.Recv)
	localTypes := workerExecutionFunctionLocalTypes(function)
	unit := workerExecutionFunctionUnit(directory, filePath, source, fileSet, packageName, imports, function, receiver, localTypes)
	unit.key = index.workerExecutionUnitKey(unit)
	if function.Name.Name == "init" && receiver == "" {
		index.initializers[directory] = append(index.initializers[directory], unit)
		return
	}
	if receiver == "" {
		index.addNamedUnit(index.symbols, directory, function.Name.Name, unit)
		index.addCommandRoutes(unit, function)
		return
	}
	index.addNamedUnit(index.methods, directory, function.Name.Name, unit)
	index.addNamedUnit(index.receiverMethods, directory, receiver, unit)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionFunctionUnit(directory, filePath string, source []byte, fileSet *token.FileSet, packageName string, imports map[string]workerExecutionGoImport, function *ast.FuncDecl, receiver string, localTypes map[string]ast.Expr) *workerExecutionGoUnit {
	unit := &workerExecutionGoUnit{
		directory: directory, filePath: filePath, packageName: packageName,
		kind: "func", names: []string{function.Name.Name}, receiver: receiver,
		source: source, fileSet: fileSet, node: function, content: function,
		signature:    workerExecutionFunctionResults(function),
		dependencies: workerExecutionFunctionBody(function),
		localTypes:   localTypes,
		localNames:   workerExecutionFunctionLocalNames(function, localTypes),
		imports:      imports,
	}
	return unit
}

// workerExecutionFunctionResults 将缺失的返回值列表保留为 nil ast.Node。
func workerExecutionFunctionResults(function *ast.FuncDecl) ast.Node {
	if function.Type.Results == nil {
		return nil
	}
	return function.Type.Results
}

// workerExecutionFunctionBody 将仅声明函数的缺失函数体保留为 nil ast.Node。
func workerExecutionFunctionBody(function *ast.FuncDecl) ast.Node {
	if function.Body == nil {
		return nil
	}
	return function.Body
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) addGeneralDeclaration(directory, filePath string, source []byte, fileSet *token.FileSet, packageName string, imports map[string]workerExecutionGoImport, declaration *ast.GenDecl) {
	if declaration.Tok == token.IMPORT {
		return
	}
	for _, spec := range declaration.Specs {
		index.addGeneralSpec(directory, filePath, source, fileSet, packageName, imports, declaration, spec)
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) addGeneralSpec(directory, filePath string, source []byte, fileSet *token.FileSet, packageName string, imports map[string]workerExecutionGoImport, declaration *ast.GenDecl, spec ast.Spec) {
	names, receiver, dependencies := workerExecutionSpecMetadata(spec)
	if len(names) == 0 {
		return
	}
	content := ast.Node(spec)
	if index.previousGroupedDeclaration {
		dependencies = declaration
		content = declaration
	} else if declaration.Tok == token.CONST && workerExecutionConstSpecNeedsDeclarationContext(spec) {
		dependencies = declaration
		content = declaration
	}
	unit := &workerExecutionGoUnit{directory: directory, filePath: filePath, packageName: packageName, kind: declaration.Tok.String(), names: names, receiver: receiver, source: source, fileSet: fileSet, node: spec, content: content, dependencies: dependencies, imports: imports}
	unit.key = index.workerExecutionUnitKey(unit)
	if index.addGeneralNames(directory, names, unit) {
		return
	}
	index.initializers[directory] = append(index.initializers[directory], unit)
}

// workerExecutionConstSpecNeedsDeclarationContext 对省略表达式或依赖 iota
// 序号的常量保留完整声明块；普通显式常量只绑定自身 ValueSpec。
func workerExecutionConstSpecNeedsDeclarationContext(spec ast.Spec) bool {
	value, ok := spec.(*ast.ValueSpec)
	if !ok || len(value.Values) == 0 {
		return true
	}
	usesIota := false
	for _, expression := range value.Values {
		ast.Inspect(expression, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && identifier.Name == "iota" {
				usesIota = true
				return false
			}
			return !usesIota
		})
	}
	return usesIota
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) addGeneralNames(directory string, names []string, unit *workerExecutionGoUnit) bool {
	found := false
	for _, name := range names {
		if name != "_" {
			found = true
			index.addNamedUnit(index.symbols, directory, name, unit)
		}
	}
	return found
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) addCommandRoutes(
	functionUnit *workerExecutionGoUnit,
	function *ast.FuncDecl,
) {
	if function.Body == nil {
		return
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expression := range clause.List {
			literal, ok := expression.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			command, err := strconv.Unquote(literal.Value)
			if err != nil || strings.TrimSpace(command) != command || command == "" {
				continue
			}
			unit := &workerExecutionGoUnit{
				directory: functionUnit.directory, filePath: functionUnit.filePath,
				packageName: functionUnit.packageName, kind: "route",
				names: []string{command}, source: functionUnit.source,
				fileSet: functionUnit.fileSet, node: clause, content: clause, dependencies: clause,
				localTypes: functionUnit.localTypes, imports: functionUnit.imports,
			}
			unit.key = index.workerExecutionUnitKey(unit)
			index.addNamedUnit(index.routes, functionUnit.directory, command, unit)
		}
		return true
	})
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionSpecMetadata(spec ast.Spec) ([]string, string, ast.Node) {
	switch value := spec.(type) {
	case *ast.TypeSpec:
		return []string{value.Name.Name}, value.Name.Name, value.Type
	case *ast.ValueSpec:
		names := make([]string, 0, len(value.Names))
		for _, name := range value.Names {
			names = append(names, name.Name)
		}
		return names, "", value
	default:
		return nil, "", nil
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionReceiverName(receiver *ast.FieldList) string {
	if receiver == nil || len(receiver.List) != 1 {
		return ""
	}
	expression := receiver.List[0].Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	identifier, _ := expression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionFunctionLocalTypes(function *ast.FuncDecl) map[string]ast.Expr {
	types := make(map[string]ast.Expr)
	for _, fields := range []*ast.FieldList{function.Recv, function.Type.Params, function.Type.Results} {
		if fields == nil {
			continue
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				types[name.Name] = field.Type
			}
		}
	}
	return types
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionFunctionLocalNames(
	function *ast.FuncDecl,
	localTypes map[string]ast.Expr,
) map[string]struct{} {
	names := make(map[string]struct{}, len(localTypes))
	for name := range localTypes {
		names[name] = struct{}{}
	}
	if function.Body == nil {
		return names
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE {
				workerExecutionAddAssignedNames(names, value.Lhs)
			}
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				workerExecutionAddAssignedNames(names, []ast.Expr{value.Key, value.Value})
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				names[name.Name] = struct{}{}
			}
		case *ast.TypeSpec:
			names[value.Name.Name] = struct{}{}
		}
		return true
	})
	return names
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionAddAssignedNames(names map[string]struct{}, expressions []ast.Expr) {
	for _, expression := range expressions {
		identifier, ok := expression.(*ast.Ident)
		if ok && identifier != nil {
			names[identifier.Name] = struct{}{}
		}
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) workerExecutionUnitKey(unit *workerExecutionGoUnit) string {
	if index.unitKeyStrategy == workerExecutionPositionalUnitKeys {
		return fmt.Sprintf("%s:%d:%d:%s", unit.filePath, unit.node.Pos(), unit.node.End(), unit.kind)
	}
	base := workerExecutionStableUnitKeyBase(unit)
	ordinal := index.unitKeyOrdinals[base]
	index.unitKeyOrdinals[base] = ordinal + 1
	return fmt.Sprintf("%s#%d", base, ordinal)
}

// workerExecutionStableUnitKeyBase 用声明语义而非源码偏移构造键，避免文件前部窄改让未变闭包整体失效。
func workerExecutionStableUnitKeyBase(unit *workerExecutionGoUnit) string {
	parts := []string{unit.filePath, unit.packageName, unit.kind, unit.receiver}
	parts = append(parts, unit.names...)
	for index := range parts {
		parts[index] = strconv.Quote(parts[index])
	}
	return strings.Join(parts, "|")
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) addNamedUnit(
	target map[string]map[string][]*workerExecutionGoUnit,
	directory string,
	name string,
	unit *workerExecutionGoUnit,
) {
	if target[directory] == nil {
		target[directory] = make(map[string][]*workerExecutionGoUnit)
	}
	target[directory][name] = append(target[directory][name], unit)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (index *workerExecutionGoIndex) resolveRoot(root workerExecutionRoot) (*workerExecutionGoUnit, error) {
	units := append([]*workerExecutionGoUnit(nil), index.symbols[root.directory][root.symbol]...)
	units = append(units, index.methods[root.directory][root.symbol]...)
	if len(units) != 1 {
		if parseErrors := index.parseErrors[root.directory]; len(parseErrors) > 0 {
			return nil, parseErrors[0]
		}
		if len(units) == 0 {
			return nil, fmt.Errorf("%w: %s.%s", errWorkerExecutionRootUnavailable, root.directory, root.symbol)
		}
		return nil, fmt.Errorf("worker execution root %s.%s resolved to %d declarations",
			root.directory, root.symbol, len(units))
	}
	return units[0], nil
}
