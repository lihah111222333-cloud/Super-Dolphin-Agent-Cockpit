package remoteci

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"path"
)

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func newWorkerExecutionGoClosure(index *workerExecutionGoIndex) *workerExecutionGoClosure {
	return &workerExecutionGoClosure{
		index:       index,
		selected:    make(map[string]*workerExecutionGoUnit),
		usedImports: make(map[string]map[string]struct{}),
		reached:     make(map[string]struct{}),
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) enqueue(unit *workerExecutionGoUnit) {
	if unit == nil {
		return
	}
	if _, exists := closure.selected[unit.key]; exists {
		return
	}
	closure.selected[unit.key] = unit
	closure.queue = append(closure.queue, unit)
	if _, exists := closure.reached[unit.directory]; exists {
		return
	}
	closure.reached[unit.directory] = struct{}{}
	for _, initializer := range closure.index.initializers[unit.directory] {
		closure.enqueue(initializer)
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolve() error {
	for closure.resolved < len(closure.queue) {
		unit := closure.queue[closure.resolved]
		closure.resolved++
		if err := closure.resolveUnit(unit); err != nil {
			return err
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolveSelfCommands() error {
	for cursor := 0; cursor < len(closure.commands); cursor++ {
		command := closure.commands[cursor]
		if len(command) == 0 || path.Base(command[0]) != "super-dolphin-gate" {
			continue
		}
		if len(command) < 2 {
			return errors.New("worker execution self-command has no subcommand")
		}
		directory := ""
		for _, root := range workerExecutionRoots {
			if root.symbol == "runWorkerCLI" {
				directory = root.directory
				break
			}
		}
		if directory == "" {
			return errors.New("worker execution CLI entry root is missing")
		}
		routes := closure.index.routes[directory][command[1]]
		if len(routes) != 1 {
			return fmt.Errorf("worker execution self-command %q resolved to %d routes",
				command[1], len(routes))
		}
		closure.enqueue(routes[0])
		if err := closure.resolve(); err != nil {
			return err
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolveUnit(unit *workerExecutionGoUnit) error {
	if err := closure.recordUnitImports(unit); err != nil {
		return err
	}
	closure.commands = append(closure.commands, workerExecutionStaticCommands(unit.node)...)
	if unit.signature != nil {
		if err := closure.resolveNode(unit, unit.signature); err != nil {
			return err
		}
	}
	if unit.dependencies != nil {
		if err := closure.resolveNode(unit, unit.dependencies); err != nil {
			return err
		}
	}
	if unit.kind == "type" {
		if closure.includeAllReceiverMethods {
			for _, name := range unit.names {
				for _, method := range closure.index.receiverMethods[unit.directory][name] {
					closure.enqueue(method)
				}
			}
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolveNode(
	unit *workerExecutionGoUnit,
	dependencies ast.Node,
) error {
	calledSelectors := workerExecutionCalledSelectors(dependencies)
	handled := make(map[*ast.Ident]struct{})
	localMethodTypes := make(map[ast.Expr]struct{})
	if err := closure.resolveWorkerSelectors(unit, dependencies, calledSelectors, handled, localMethodTypes); err != nil {
		return err
	}
	if err := closure.resolveWorkerLocalTypes(unit, localMethodTypes); err != nil {
		return err
	}
	closure.resolveWorkerIdentifiers(unit, dependencies, handled)
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionCalledSelectors(dependencies ast.Node) map[*ast.SelectorExpr]struct{} {
	result := make(map[*ast.SelectorExpr]struct{})
	ast.Inspect(dependencies, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				result[selector] = struct{}{}
			}
		}
		return true
	})
	return result
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolveWorkerSelectors(unit *workerExecutionGoUnit, dependencies ast.Node, called map[*ast.SelectorExpr]struct{}, handled map[*ast.Ident]struct{}, localTypes map[ast.Expr]struct{}) error {
	var resolveErr error
	ast.Inspect(dependencies, func(node ast.Node) bool {
		if resolveErr != nil {
			return false
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		handled[selector.Sel] = struct{}{}
		resolveErr = closure.resolveWorkerSelector(unit, selector, called, handled, localTypes)
		return resolveErr == nil
	})
	return resolveErr
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolveWorkerSelector(unit *workerExecutionGoUnit, selector *ast.SelectorExpr, called map[*ast.SelectorExpr]struct{}, handled map[*ast.Ident]struct{}, localTypes map[ast.Expr]struct{}) error {
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return closure.resolveWorkerDynamicSelector(unit, selector, called)
	}
	return closure.resolveWorkerIdentifierSelector(unit, selector, identifier, called, handled, localTypes)
}

// 解析标识符 receiver，优先纳入外部 import 或精确本地方法。
func (closure *workerExecutionGoClosure) resolveWorkerIdentifierSelector(unit *workerExecutionGoUnit, selector *ast.SelectorExpr, identifier *ast.Ident, called map[*ast.SelectorExpr]struct{}, handled map[*ast.Ident]struct{}, localTypes map[ast.Expr]struct{}) error {
	if imported, exists := unit.imports[identifier.Name]; exists && !workerExecutionLocalName(unit, identifier.Name) {
		handled[identifier] = struct{}{}
		closure.addUsedImport(unit, imported.importPath)
		return closure.resolveWorkerImportedSelector(imported, selector.Sel.Name)
	}
	if _, called := called[selector]; !called {
		return nil
	}
	if expression, exists := unit.localTypes[identifier.Name]; exists {
		localTypes[expression] = struct{}{}
		if closure.includeAllReceiverMethods {
			return nil
		}
		return closure.enqueueReceiverMethod(unit, expression, selector.Sel.Name)
	}
	if !closure.includeAllReceiverMethods {
		if directory, receiver, resolved := closure.resolveWorkerAssignedReceiverType(unit, identifier.Name); resolved {
			return closure.enqueueWorkerReceiverMethod(directory, receiver, selector.Sel.Name)
		}
	}
	return closure.resolveWorkerUnknownReceiver(unit, identifier, selector.Sel.Name)
}

// 处理非标识符 receiver，仅明确外部调用链可以跳过动态拒绝。
func (closure *workerExecutionGoClosure) resolveWorkerDynamicSelector(unit *workerExecutionGoUnit, selector *ast.SelectorExpr, called map[*ast.SelectorExpr]struct{}) error {
	if closure.includeAllReceiverMethods {
		return nil
	}
	if _, invoked := called[selector]; !invoked || workerExecutionExternalSelectorWithClosure(closure, unit, selector.X) {
		return nil
	}
	if directory, receiver, resolved := closure.resolveWorkerSelectorReceiverType(unit, selector.X); resolved {
		return closure.enqueueWorkerReceiverMethod(directory, receiver, selector.Sel.Name)
	}
	if !closure.workerExecutionMethodNameExists(unit, selector.Sel.Name) {
		return nil
	}
	return workerExecutionDynamicSelectorError(unit, selector)
}

// 提供动态 receiver 的精确生产位置，便于审计 fail-closed 判定来源。
func workerExecutionDynamicSelectorError(unit *workerExecutionGoUnit, selector *ast.SelectorExpr) error {
	if unit == nil || unit.fileSet == nil || selector == nil || selector.Sel == nil {
		return fmt.Errorf("worker execution method receiver is dynamic")
	}
	position := unit.fileSet.Position(selector.Pos())
	return fmt.Errorf("worker execution method receiver for %q is dynamic at %s:%d", selector.Sel.Name, unit.filePath, position.Line)
}

// 未能解析本地 receiver 时 fail-closed，legacy broad closure 保持兼容。
func (closure *workerExecutionGoClosure) resolveWorkerUnknownReceiver(unit *workerExecutionGoUnit, identifier *ast.Ident, name string) error {
	if closure.includeAllReceiverMethods || workerExecutionExternalSelectorWithClosure(closure, unit, identifier) {
		return nil
	}
	if directory, receiver, resolved := closure.resolveWorkerAssignedReceiverType(unit, identifier.Name); resolved {
		return closure.enqueueWorkerReceiverMethod(directory, receiver, name)
	}
	if !closure.workerExecutionMethodNameExists(unit, name) {
		return nil
	}
	return workerExecutionDynamicIdentifierError(unit, identifier, name)
}

// 提供标识符 receiver 的精确生产位置，避免 fail-closed 诊断只剩方法名。
func workerExecutionDynamicIdentifierError(unit *workerExecutionGoUnit, identifier *ast.Ident, name string) error {
	if unit == nil || unit.fileSet == nil || identifier == nil {
		return fmt.Errorf("worker execution method receiver for %q is dynamic", name)
	}
	position := unit.fileSet.Position(identifier.Pos())
	return fmt.Errorf("worker execution method receiver for %q is dynamic at %s:%d", name, unit.filePath, position.Line)
}

// 当前模块完全没有同名方法时，编译成功的 selector 必然来自模块外部类型。
// 存在任一同名本地方法时仍保持 fail-closed，避免把未知本地 receiver 当成外部类型。
func (closure *workerExecutionGoClosure) workerExecutionMethodNameExists(unit *workerExecutionGoUnit, name string) bool {
	if unit == nil {
		return true
	}
	directories := map[string]struct{}{unit.directory: {}}
	for _, imported := range unit.imports {
		if imported.local {
			directories[imported.directory] = struct{}{}
		}
	}
	for directory := range directories {
		for _, methods := range closure.index.receiverMethods[directory] {
			for _, method := range methods {
				if len(method.names) == 1 && method.names[0] == name {
					return true
				}
			}
		}
	}
	return false
}

// 仅将静态 receiver 的实际方法加入 Worker 执行闭包，动态 receiver 直接拒绝。
func (closure *workerExecutionGoClosure) enqueueReceiverMethod(unit *workerExecutionGoUnit, expression ast.Expr, name string) error {
	directory, receiver, local, external := workerExecutionReceiverType(unit, expression)
	if external {
		return nil
	}
	if !local {
		return fmt.Errorf("worker execution method receiver for %q is dynamic", name)
	}
	return closure.enqueueWorkerReceiverMethod(directory, receiver, name)
}

// 将已解析的本地 receiver 方法精确加入闭包，拒绝缺失或重复声明。
func (closure *workerExecutionGoClosure) enqueueWorkerReceiverMethod(directory, receiver, name string) error {
	if closure.workerExecutionPredeclaredMethod(directory, receiver, name) ||
		closure.workerExecutionExternalReceiverAlias(directory, receiver) {
		return nil
	}
	candidates := closure.index.receiverMethods[directory][receiver]
	methods := make([]*workerExecutionGoUnit, 0, len(candidates))
	for _, candidate := range candidates {
		if len(candidate.names) == 1 && candidate.names[0] == name {
			methods = append(methods, candidate)
		}
	}
	if len(methods) == 0 {
		if resolved, err := closure.enqueueWorkerInterfaceMethod(directory, receiver, name); resolved {
			return err
		}
		if closure.workerExecutionReceiverField(directory, receiver, name) {
			return nil
		}
		return fmt.Errorf("worker execution method %s.%s has no production declaration in %s", receiver, name, directory)
	}
	for _, method := range methods {
		closure.enqueue(method)
	}
	return nil
}

// 预声明 error 接口只放行其唯一 Error 方法；同包显式遮蔽声明仍纳入本地闭包。
func (closure *workerExecutionGoClosure) workerExecutionPredeclaredMethod(directory, receiver, name string) bool {
	return receiver == "error" && name == "Error" && len(closure.index.symbols[directory][receiver]) == 0
}

// 外部类型别名沿用其标准库或第三方方法集，不把别名包误当成方法实现所有者。
func (closure *workerExecutionGoClosure) workerExecutionExternalReceiverAlias(directory, receiver string) bool {
	candidates := closure.index.symbols[directory][receiver]
	if len(candidates) != 1 {
		return false
	}
	typeSpec, ok := candidates[0].node.(*ast.TypeSpec)
	return ok && typeSpec.Assign.IsValid() && workerExecutionExternalDeclaredType(candidates[0], typeSpec.Type)
}

// 解析本地、外部或动态 receiver 类型，供方法闭包边界作 fail-fast 判定。
func workerExecutionReceiverType(unit *workerExecutionGoUnit, expression ast.Expr) (string, string, bool, bool) {
	switch value := expression.(type) {
	case *ast.StarExpr:
		return workerExecutionReceiverType(unit, value.X)
	case *ast.Ident:
		return unit.directory, value.Name, true, false
	case *ast.IndexExpr:
		return workerExecutionReceiverType(unit, value.X)
	case *ast.IndexListExpr:
		return workerExecutionReceiverType(unit, value.X)
	case *ast.SelectorExpr:
		identifier, ok := value.X.(*ast.Ident)
		if !ok {
			return "", "", false, false
		}
		imported, ok := unit.imports[identifier.Name]
		if !ok {
			return "", "", false, false
		}
		if !imported.local {
			return "", "", false, true
		}
		return imported.directory, value.Sel.Name, true, false
	default:
		return "", "", false, false
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionLocalName(unit *workerExecutionGoUnit, name string) bool {
	_, ok := unit.localNames[name]
	return ok
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolveWorkerImportedSelector(imported workerExecutionGoImport, symbol string) error {
	if !imported.local {
		return nil
	}
	candidates := closure.index.symbols[imported.directory][symbol]
	if len(candidates) == 0 {
		return closure.unresolvedLocalSymbol(imported.directory, symbol)
	}
	for _, candidate := range candidates {
		closure.enqueue(candidate)
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolveWorkerLocalTypes(unit *workerExecutionGoUnit, localTypes map[ast.Expr]struct{}) error {
	for expression := range localTypes {
		if err := closure.resolveNode(unit, expression); err != nil {
			return err
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) resolveWorkerIdentifiers(unit *workerExecutionGoUnit, dependencies ast.Node, handled map[*ast.Ident]struct{}) {
	defined := workerExecutionDefinedIdentifiers(dependencies)
	ast.Inspect(dependencies, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok && workerExecutionUnresolvedIdentifier(unit, identifier, handled, defined) {
			for _, candidate := range closure.index.symbols[unit.directory][identifier.Name] {
				closure.enqueue(candidate)
			}
		}
		return true
	})
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionUnresolvedIdentifier(unit *workerExecutionGoUnit, identifier *ast.Ident, handled, defined map[*ast.Ident]struct{}) bool {
	if identifier.Name == "_" {
		return false
	}
	if _, ok := handled[identifier]; ok {
		return false
	}
	if _, ok := defined[identifier]; ok {
		return false
	}
	return !workerExecutionLocalName(unit, identifier.Name)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionDefinedIdentifiers(node ast.Node) map[*ast.Ident]struct{} {
	defined := make(map[*ast.Ident]struct{})
	ast.Inspect(node, func(node ast.Node) bool {
		workerExecutionAddDefinedIdentifiers(defined, node)
		return true
	})
	return defined
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionAddDefinedIdentifiers(defined map[*ast.Ident]struct{}, node ast.Node) {
	switch value := node.(type) {
	case *ast.Field:
		workerExecutionAddIdentifierNames(defined, value.Names)
	case *ast.ValueSpec:
		workerExecutionAddIdentifierNames(defined, value.Names)
	case *ast.TypeSpec:
		defined[value.Name] = struct{}{}
	case *ast.AssignStmt:
		workerExecutionAddDefinedExpressions(defined, value.Tok, value.Lhs)
	case *ast.RangeStmt:
		workerExecutionAddDefinedExpressions(defined, value.Tok, []ast.Expr{value.Key, value.Value})
	case *ast.LabeledStmt:
		defined[value.Label] = struct{}{}
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionAddIdentifierNames(defined map[*ast.Ident]struct{}, names []*ast.Ident) {
	for _, name := range names {
		defined[name] = struct{}{}
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionAddDefinedExpressions(defined map[*ast.Ident]struct{}, tokenValue token.Token, expressions []ast.Expr) {
	if tokenValue != token.DEFINE {
		return
	}
	for _, expression := range expressions {
		if identifier, ok := expression.(*ast.Ident); ok && identifier != nil {
			defined[identifier] = struct{}{}
		}
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) recordUnitImports(unit *workerExecutionGoUnit) error {
	ast.Inspect(unit.node, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if imported, exists := unit.imports[identifier.Name]; exists {
			closure.addUsedImport(unit, imported.importPath)
		}
		return true
	})
	for name, imported := range unit.imports {
		if name == "." {
			return fmt.Errorf("worker execution source %q uses an unmodelled dot import %q",
				unit.filePath, imported.importPath)
		}
		if name != "_" {
			continue
		}
		closure.addUsedImport(unit, imported.importPath)
		if imported.local {
			return fmt.Errorf("worker execution source %q uses an unmodelled local side-effect import %q",
				unit.filePath, imported.importPath)
		}
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) addUsedImport(unit *workerExecutionGoUnit, importPath string) {
	if closure.usedImports[unit.key] == nil {
		closure.usedImports[unit.key] = make(map[string]struct{})
	}
	closure.usedImports[unit.key][importPath] = struct{}{}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (closure *workerExecutionGoClosure) unresolvedLocalSymbol(directory string, symbol string) error {
	if parseErrors := closure.index.parseErrors[directory]; len(parseErrors) > 0 {
		return parseErrors[0]
	}
	return fmt.Errorf("worker execution dependency %s.%s has no linux/amd64 production declaration",
		directory, symbol)
}
