package turn

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

type cloneAssignmentTracker struct {
	assignments map[string]ast.Expr
	cloneName   string
	found       bool
	typeName    string
	err         error
}

func validateGoStructClone(
	root string,
	chain goChainLocator,
	fn *ast.FuncDecl,
	schemas map[string]schemaRegistryEntry,
	producers map[string]map[string]bool,
	overrides map[string]string,
) error {
	producerFields, ok := producers[chain.CloneOf]
	if !ok {
		return fmt.Errorf("Go production chain %s clone schema %s is not canonical", chain.Name, chain.CloneOf)
	}
	entry, ok := schemas[chain.CloneOf]
	if !ok {
		return fmt.Errorf("Go production chain %s clone schema %s is not registered", chain.Name, chain.CloneOf)
	}
	definitions, err := goStructJSONFieldDefinitions(root, entry.GoType, overrides)
	if err != nil {
		return fmt.Errorf("Go production chain %s clone type: %w", chain.Name, err)
	}
	goFields := make(map[string]bool, len(definitions))
	for jsonName := range definitions {
		goFields[jsonName] = true
	}
	if err := assertExactFieldCoverage(chain.CloneOf+" clone Go JSON", producerFields, goFields); err != nil {
		return err
	}
	sourceName, err := cloneSourceParameter(fn)
	if err != nil {
		return fmt.Errorf("Go production chain %s: %w", chain.Name, err)
	}
	_, assignments, err := cloneAssignments(fn, entry.GoType.Symbol)
	if err != nil {
		return fmt.Errorf("Go production chain %s: %w", chain.Name, err)
	}
	return validateCloneAssignmentCoverage(
		root,
		chain.CloneOf,
		producerFields,
		definitions,
		assignments,
		[]string{sourceName},
		schemas,
		producers,
		overrides,
	)
}

func cloneSourceParameter(fn *ast.FuncDecl) (string, error) {
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 {
		return "", fmt.Errorf("%s must declare exactly one named clone source parameter", fn.Name.Name)
	}
	name := fn.Type.Params.List[0].Names[0].Name
	if name == "" || name == "_" {
		return "", fmt.Errorf("%s clone source parameter is blank", fn.Name.Name)
	}
	return name, nil
}

func cloneAssignments(fn *ast.FuncDecl, typeName string) (string, map[string]ast.Expr, error) {
	tracker := cloneAssignmentTracker{
		assignments: map[string]ast.Expr{},
		typeName:    typeName,
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		return tracker.inspectNode(node)
	})
	if tracker.err != nil {
		return "", nil, tracker.err
	}
	if !tracker.found {
		return "", nil, fmt.Errorf("%s does not initialize a %s clone literal", fn.Name.Name, typeName)
	}
	return tracker.cloneName, tracker.assignments, nil
}

func (tracker *cloneAssignmentTracker) inspectNode(node ast.Node) bool {
	if tracker.err != nil {
		return false
	}
	switch typed := node.(type) {
	case *ast.AssignStmt:
		tracker.trackAssignment(typed)
	case *ast.DeclStmt:
		tracker.trackDeclaration(typed)
	}
	return tracker.err == nil
}

func (tracker *cloneAssignmentTracker) trackAssignment(assignment *ast.AssignStmt) {
	if tracker.found {
		tracker.err = addSelectorCloneAssignments(tracker.assignments, tracker.cloneName, assignment.Lhs, assignment.Rhs)
		return
	}
	tracker.trackLiteralAssignment(assignment.Lhs, assignment.Rhs)
}

func (tracker *cloneAssignmentTracker) trackDeclaration(declaration *ast.DeclStmt) {
	if tracker.found {
		return
	}
	general, ok := declaration.Decl.(*ast.GenDecl)
	if !ok {
		return
	}
	if general.Tok != token.VAR {
		return
	}
	for _, spec := range general.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		tracker.trackLiteralAssignment(valueSpecNames(valueSpec), valueSpec.Values)
		if tracker.found || tracker.err != nil {
			return
		}
	}
}

func valueSpecNames(specification *ast.ValueSpec) []ast.Expr {
	left := make([]ast.Expr, 0, len(specification.Names))
	for _, name := range specification.Names {
		left = append(left, name)
	}
	return left
}

func (tracker *cloneAssignmentTracker) trackLiteralAssignment(left, right []ast.Expr) {
	name, fields, matched, err := cloneLiteralAssignment(left, right, tracker.typeName)
	if err != nil {
		tracker.err = err
		return
	}
	if !matched {
		return
	}
	tracker.cloneName = name
	tracker.found = true
	tracker.err = addCloneAssignments(tracker.assignments, fields)
}

func cloneLiteralAssignment(left, right []ast.Expr, typeName string) (string, map[string]ast.Expr, bool, error) {
	for index, value := range right {
		literal, ok := value.(*ast.CompositeLit)
		if !ok || compositeLiteralTypeName(literal) != typeName {
			continue
		}
		if index >= len(left) {
			return "", nil, false, fmt.Errorf("%s clone literal has no assignment target", typeName)
		}
		name, ok := left[index].(*ast.Ident)
		if !ok || name.Name == "_" {
			return "", nil, false, fmt.Errorf("%s clone literal target must be a named variable", typeName)
		}
		fields, err := compositeLiteralAssignments(literal)
		if err != nil {
			return "", nil, false, err
		}
		return name.Name, fields, true, nil
	}
	return "", nil, false, nil
}

func compositeLiteralAssignments(literal *ast.CompositeLit) (map[string]ast.Expr, error) {
	fields := map[string]ast.Expr{}
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, fmt.Errorf("clone literal must use keyed fields")
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name == "" {
			return nil, fmt.Errorf("clone literal has an invalid field key")
		}
		if _, exists := fields[key.Name]; exists {
			return nil, fmt.Errorf("clone literal assigns duplicate field %s", key.Name)
		}
		fields[key.Name] = pair.Value
	}
	return fields, nil
}

func addCloneAssignments(destination, source map[string]ast.Expr) error {
	for field, value := range source {
		if _, exists := destination[field]; exists {
			return fmt.Errorf("clone assigns field %s more than once", field)
		}
		destination[field] = value
	}
	return nil
}

func addSelectorCloneAssignments(destination map[string]ast.Expr, cloneName string, left, right []ast.Expr) error {
	for index, target := range left {
		field := selectedCloneField(target, cloneName)
		if field == "" {
			continue
		}
		if index >= len(right) {
			return fmt.Errorf("clone field %s has no source expression", field)
		}
		if _, exists := destination[field]; exists {
			return fmt.Errorf("clone assigns field %s more than once", field)
		}
		destination[field] = right[index]
	}
	return nil
}

func selectedCloneField(expression ast.Expr, cloneName string) string {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok || receiver.Name != cloneName {
		return ""
	}
	return selector.Sel.Name
}

func validateCloneAssignmentCoverage(
	root, schemaName string,
	producerFields map[string]bool,
	definitions map[string]goJSONField,
	assignments map[string]ast.Expr,
	sourcePath []string,
	schemas map[string]schemaRegistryEntry,
	producers map[string]map[string]bool,
	overrides map[string]string,
) error {
	definitionsByGoName := cloneDefinitionsByGoName(definitions)
	covered := map[string]bool{}
	for goName, expression := range assignments {
		definition, err := validateCloneFieldAssignment(
			root,
			schemaName,
			goName,
			expression,
			definitionsByGoName,
			sourcePath,
			schemas,
			producers,
			overrides,
		)
		if err != nil {
			return err
		}
		covered[definition.JSONName] = true
	}
	return assertExactFieldCoverage(schemaName+" clone", producerFields, covered)
}

func cloneDefinitionsByGoName(definitions map[string]goJSONField) map[string]goJSONField {
	byGoName := make(map[string]goJSONField, len(definitions))
	for _, definition := range definitions {
		byGoName[definition.GoName] = definition
	}
	return byGoName
}

func validateCloneFieldAssignment(
	root, schemaName, goName string,
	expression ast.Expr,
	definitionsByGoName map[string]goJSONField,
	sourcePath []string,
	schemas map[string]schemaRegistryEntry,
	producers map[string]map[string]bool,
	overrides map[string]string,
) (goJSONField, error) {
	definition, ok := definitionsByGoName[goName]
	if !ok {
		return goJSONField{}, fmt.Errorf("%s clone assigns stale field %s", schemaName, goName)
	}
	fieldSourcePath := append(append([]string(nil), sourcePath...), definition.GoName)
	if !expressionReferencesSourcePath(expression, fieldSourcePath) {
		return goJSONField{}, fmt.Errorf("%s clone field %s does not copy %s", schemaName, definition.JSONName, strings.Join(fieldSourcePath, "."))
	}
	if referenceGoType(definition.Type) && isDirectSourcePath(expression, fieldSourcePath) {
		return goJSONField{}, fmt.Errorf("%s clone field %s retains a shared reference", schemaName, definition.JSONName)
	}
	if err := validateNestedCloneAssignment(
		root,
		schemaName,
		definition,
		expression,
		fieldSourcePath,
		schemas,
		producers,
		overrides,
	); err != nil {
		return goJSONField{}, err
	}
	return definition, nil
}

func validateNestedCloneAssignment(
	root, schemaName string,
	definition goJSONField,
	expression ast.Expr,
	fieldSourcePath []string,
	schemas map[string]schemaRegistryEntry,
	producers map[string]map[string]bool,
	overrides map[string]string,
) error {
	nestedSchemaName, nestedEntry, nested := nestedCloneSchema(definition.Type, schemas)
	if !nested {
		return nil
	}
	nestedLiteral := typedCompositeLiteral(expression, nestedEntry.GoType.Symbol)
	if nestedLiteral == nil {
		return fmt.Errorf("%s clone field %s does not construct nested %s", schemaName, definition.JSONName, nestedSchemaName)
	}
	nestedDefinitions, err := goStructJSONFieldDefinitions(root, nestedEntry.GoType, overrides)
	if err != nil {
		return fmt.Errorf("%s clone nested type %s: %w", schemaName, nestedSchemaName, err)
	}
	nestedAssignments, err := compositeLiteralAssignments(nestedLiteral)
	if err != nil {
		return fmt.Errorf("%s clone nested %s: %w", schemaName, nestedSchemaName, err)
	}
	return validateCloneAssignmentCoverage(
		root,
		nestedSchemaName,
		producers[nestedSchemaName],
		nestedDefinitions,
		nestedAssignments,
		fieldSourcePath,
		schemas,
		producers,
		overrides,
	)
}

func nestedCloneSchema(fieldType ast.Expr, schemas map[string]schemaRegistryEntry) (string, schemaRegistryEntry, bool) {
	typeName := namedGoType(fieldType)
	if typeName == "" {
		return "", schemaRegistryEntry{}, false
	}
	for schemaName, entry := range schemas {
		if entry.GoType.Symbol == typeName {
			return schemaName, entry, true
		}
	}
	return "", schemaRegistryEntry{}, false
}

func namedGoType(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return namedGoType(typed.X)
	case *ast.ParenExpr:
		return namedGoType(typed.X)
	default:
		return ""
	}
}

func typedCompositeLiteral(expression ast.Expr, typeName string) *ast.CompositeLit {
	var found *ast.CompositeLit
	ast.Inspect(expression, func(node ast.Node) bool {
		if found != nil {
			return false
		}
		literal, ok := node.(*ast.CompositeLit)
		if ok && compositeLiteralTypeName(literal) == typeName {
			found = literal
			return false
		}
		return true
	})
	return found
}

func compositeLiteralTypeName(literal *ast.CompositeLit) string {
	return namedGoType(literal.Type)
}

func expressionReferencesSourcePath(expression ast.Expr, sourcePath []string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		if found {
			return false
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pathHasPrefix(selectorPath(selector), sourcePath) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isDirectSourcePath(expression ast.Expr, sourcePath []string) bool {
	return pathEqual(selectorPath(expression), sourcePath)
}

func selectorPath(expression ast.Expr) []string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return []string{typed.Name}
	case *ast.SelectorExpr:
		path := selectorPath(typed.X)
		if len(path) == 0 {
			return nil
		}
		return append(path, typed.Sel.Name)
	case *ast.ParenExpr:
		return selectorPath(typed.X)
	default:
		return nil
	}
}

func pathHasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	return pathEqual(path[:len(prefix)], prefix)
}

func pathEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func referenceGoType(expression ast.Expr) bool {
	switch expression.(type) {
	case *ast.StarExpr, *ast.ArrayType, *ast.MapType, *ast.InterfaceType, *ast.FuncType, *ast.ChanType:
		return true
	default:
		return false
	}
}
