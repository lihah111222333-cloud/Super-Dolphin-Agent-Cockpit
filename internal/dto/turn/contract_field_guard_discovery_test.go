package turn

import (
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type validatorTargetTracker struct {
	aliases     map[string]map[string]bool
	targets     map[string]string
	usedTargets map[string]bool
}

func validateExactGoConsumerCoverage(root string, schemas map[string]schemaRegistryEntry, overrides map[string]string) error {
	discovered, err := discoverGoValidatorConsumers(root, schemas, overrides)
	if err != nil {
		return err
	}
	for name, entry := range schemas {
		registered := make([]string, 0, len(entry.GoConsumers))
		for _, consumer := range entry.GoConsumers {
			registered = append(registered, consumerKey(consumer))
		}
		if err := assertExactConsumerSet(name+" Go production consumers", discovered[name], registered); err != nil {
			return err
		}
	}
	return nil
}

func discoverGoValidatorConsumers(root string, schemas map[string]schemaRegistryEntry, overrides map[string]string) (map[string][]string, error) {
	targets := map[string]string{}
	discovered := map[string][]string{}
	for schemaName, entry := range schemas {
		targets[entry.GoValidator.Symbol] = schemaName
		discovered[schemaName] = nil
	}
	files, err := productionGoFiles(root)
	if err != nil {
		return nil, err
	}
	for _, relativePath := range files {
		if err := discoverGoConsumersInFile(root, relativePath, targets, discovered, overrides); err != nil {
			return nil, err
		}
	}
	for schemaName, consumers := range discovered {
		discovered[schemaName] = uniqueSortedStrings(consumers)
	}
	return discovered, nil
}

func productionGoFiles(root string) ([]string, error) {
	files := make([]string, 0)
	for _, scanRoot := range []string{"cmd", "internal", "scripts"} {
		err := filepath.WalkDir(filepath.Join(root, scanRoot), func(path string, entry fs.DirEntry, walkErr error) error {
			relativePath, include, err := productionGoFilePath(root, path, entry, walkErr)
			if err != nil {
				return err
			}
			if include {
				files = append(files, relativePath)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("discover Go validator consumers under %s: %w", scanRoot, err)
		}
	}
	return files, nil
}

func productionGoFilePath(root, path string, entry fs.DirEntry, walkErr error) (string, bool, error) {
	if walkErr != nil {
		return "", false, walkErr
	}
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
		return "", false, nil
	}
	if strings.HasSuffix(entry.Name(), "_test.go") {
		return "", false, nil
	}
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		return "", false, err
	}
	return filepath.ToSlash(relativePath), true, nil
}

func discoverGoConsumersInFile(root, relativePath string, targets map[string]string, discovered map[string][]string, overrides map[string]string) error {
	file, err := parseGoFile(root, relativePath, overrides)
	if err != nil {
		return err
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		discoverGoConsumersInFunction(relativePath, fn, targets, discovered)
	}
	return nil
}

func discoverGoConsumersInFunction(relativePath string, fn *ast.FuncDecl, targets map[string]string, discovered map[string][]string) {
	usedTargets := validatorTargetsInFunction(fn, targets)
	for target, schemaName := range targets {
		if usedTargets[target] {
			discovered[schemaName] = append(discovered[schemaName], consumerKey(callLocator{Path: relativePath, Symbol: fn.Name.Name, Calls: target}))
		}
	}
}

func validatorTargetsInFunction(fn *ast.FuncDecl, targets map[string]string) map[string]bool {
	tracker := validatorTargetTracker{
		aliases:     map[string]map[string]bool{},
		targets:     targets,
		usedTargets: map[string]bool{},
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		tracker.trackNode(node)
		return true
	})
	return tracker.usedTargets
}

func (tracker *validatorTargetTracker) trackNode(node ast.Node) {
	switch typed := node.(type) {
	case *ast.AssignStmt:
		trackValidatorAssignments(typed.Lhs, typed.Rhs, tracker.aliases, tracker.targets, tracker.usedTargets)
	case *ast.DeclStmt:
		tracker.trackDeclaration(typed)
	case *ast.CallExpr:
		tracker.trackCall(typed)
	case *ast.ReturnStmt:
		tracker.trackExpressions(typed.Results)
	case *ast.SendStmt:
		tracker.trackExpression(typed.Value)
	}
}

func (tracker *validatorTargetTracker) trackDeclaration(declaration *ast.DeclStmt) {
	general, ok := declaration.Decl.(*ast.GenDecl)
	if !ok {
		return
	}
	if general.Tok != token.VAR {
		return
	}
	for _, spec := range general.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if ok {
			tracker.trackValueSpec(valueSpec)
		}
	}
}

func (tracker *validatorTargetTracker) trackValueSpec(specification *ast.ValueSpec) {
	left := make([]ast.Expr, 0, len(specification.Names))
	for _, name := range specification.Names {
		left = append(left, name)
	}
	trackValidatorAssignments(left, specification.Values, tracker.aliases, tracker.targets, tracker.usedTargets)
}

func (tracker *validatorTargetTracker) trackCall(call *ast.CallExpr) {
	tracker.trackExpression(call.Fun)
	tracker.trackExpressions(call.Args)
}

func (tracker *validatorTargetTracker) trackExpressions(expressions []ast.Expr) {
	for _, expression := range expressions {
		tracker.trackExpression(expression)
	}
}

func (tracker *validatorTargetTracker) trackExpression(expression ast.Expr) {
	markValidatorTargets(tracker.usedTargets, validatorTargetsInExpression(expression, tracker.aliases, tracker.targets))
}

func trackValidatorAssignments(
	left, right []ast.Expr,
	aliases map[string]map[string]bool,
	targets map[string]string,
	usedTargets map[string]bool,
) {
	for index, destination := range left {
		identifier, isIdentifier := destination.(*ast.Ident)
		if index >= len(right) {
			if isIdentifier {
				delete(aliases, identifier.Name)
			}
			continue
		}
		sourceTargets := validatorTargetsInExpression(right[index], aliases, targets)
		markValidatorTargets(usedTargets, sourceTargets)
		if !isIdentifier {
			continue
		}
		if len(sourceTargets) == 0 {
			delete(aliases, identifier.Name)
			continue
		}
		aliases[identifier.Name] = sourceTargets
	}
}

func validatorTargetsInExpression(expression ast.Expr, aliases map[string]map[string]bool, targets map[string]string) map[string]bool {
	found := map[string]bool{}
	ast.Inspect(expression, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			if _, ok := targets[typed.Name]; ok {
				found[typed.Name] = true
			}
			markValidatorTargets(found, aliases[typed.Name])
		case *ast.SelectorExpr:
			// Source overrides are parsed without package type information. A same-named
			// selector therefore remains an unresolved potential validator method value.
			if _, ok := targets[typed.Sel.Name]; ok {
				found[typed.Sel.Name] = true
			}
		}
		return true
	})
	return found
}

func markValidatorTargets(destination, source map[string]bool) {
	for target := range source {
		destination[target] = true
	}
}

func consumerKey(consumer callLocator) string {
	return consumer.Path + ":" + consumer.Symbol + ":" + consumer.Calls
}

func assertExactConsumerSet(label string, discovered, registered []string) error {
	discoveredSet := stringSet(discovered)
	registeredSet := stringSet(registered)
	missing := fieldDifference(discoveredSet, registeredSet)
	stale := fieldDifference(registeredSet, discoveredSet)
	if len(missing) == 0 && len(stale) == 0 {
		return nil
	}
	return fmt.Errorf("%s missing=%v stale=%v", label, missing, stale)
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func uniqueSortedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for value := range stringSet(values) {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
