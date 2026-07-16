package turn

import (
	"fmt"
	"go/ast"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

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
	calls, _, _ := functionEvidence(fn)
	for target, schemaName := range targets {
		if calls[target] {
			discovered[schemaName] = append(discovered[schemaName], consumerKey(callLocator{Path: relativePath, Symbol: fn.Name.Name, Calls: target}))
		}
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
