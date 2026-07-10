package archtest_test

import (
	"bytes"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func threadInterfaceIsolationBudgets() []interfaceBudget {
	return []interfaceBudget{
		{relPath: "internal/module/thread/persistence_port.go", name: "ThreadStore", maxMethods: 10, maxEmbedded: 0},
		{relPath: "internal/module/thread/persistence_port.go", name: "BindingStore", maxMethods: 9, maxEmbedded: 0},
	}
}

func TestThreadPersistencePortsOwnTheirDTOs(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	sources := threadPortTypeSources(t, root)
	var violations []string
	for _, name := range []string{"threadServiceStorePort", "bindingServiceStorePort", "ThreadStore", "BindingStore"} {
		source, ok := sources[name]
		if !ok {
			continue
		}
		if strings.Contains(source, "threadstore.") || strings.Contains(source, "bindingstore.") {
			violations = append(violations, fmt.Sprintf("%s leaks Store-qualified DTOs: %s", name, source))
		}
	}
	for _, name := range []string{"ThreadStore", "BindingStore"} {
		if _, ok := sources[name]; !ok {
			violations = append(violations, fmt.Sprintf("thread persistence port %s is missing", name))
		}
	}
	failIfViolations(t, violations)
}

func TestThreadPortsDoNotUseAny(t *testing.T) {
	t.Parallel()

	sources := threadPortTypeSources(t, repoRoot(t))
	var violations []string
	if source, ok := sources["promptServiceCatalogPort"]; ok && source == "any" {
		violations = append(violations, "promptServiceCatalogPort has underlying type any")
	}
	if source, ok := sources["PromptCatalog"]; !ok {
		violations = append(violations, "thread prompt port PromptCatalog is missing")
	} else if source == "any" {
		violations = append(violations, "PromptCatalog has underlying type any")
	}
	failIfViolations(t, violations)
}

func threadPortTypeSources(t *testing.T, root string) map[string]string {
	t.Helper()
	sources := make(map[string]string)
	for _, relPath := range []string{
		"internal/module/thread/module.go",
		"internal/module/thread/persistence_port.go",
		"internal/module/thread/factory.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(relPath))
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("stat %s: %v", relPath, err)
		}
		file := parseGoFileForInterfaceGuard(t, root, relPath)
		for _, name := range []string{
			"threadServiceStorePort",
			"bindingServiceStorePort",
			"promptServiceCatalogPort",
			"ThreadStore",
			"BindingStore",
			"PromptCatalog",
		} {
			typeSpec, ok := findTypeSpec(file, name)
			if !ok {
				continue
			}
			var rendered bytes.Buffer
			if err := format.Node(&rendered, token.NewFileSet(), typeSpec.Type); err != nil {
				t.Fatalf("format %s.%s: %v", relPath, name, err)
			}
			sources[name] = rendered.String()
		}
	}
	return sources
}

func TestThreadServiceDropsUnusedStoreDependencies(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const modulePath = "internal/module/thread/module.go"
	const servicePath = "internal/module/thread/service.go"
	const constructorPath = "internal/module/thread/factory.go"
	var violations []string
	for _, name := range []string{"sharedFileServiceStorePort", "promptServiceStorePort"} {
		if _, _, ok := interfaceShape(t, root, modulePath, name); ok {
			violations = append(violations, fmt.Sprintf("%s: unused interface %s must be removed", modulePath, name))
		}
	}
	for _, field := range []string{"sharedFiles", "promptStore"} {
		if actual, ok := structFieldType(t, root, servicePath, "service", field); ok {
			violations = append(violations, fmt.Sprintf("%s: unused service.%s dependency must be removed, got %s", servicePath, field, actual))
		}
	}
	for _, check := range []struct {
		funcName string
		param    string
	}{
		{funcName: "NewServiceWithPromptAssemblyAndSharedFiles", param: "sharedFiles"},
		{funcName: "NewServiceWithPromptAssemblyAndSharedFiles", param: "promptStore"},
		{funcName: "newService", param: "sharedFiles"},
		{funcName: "newService", param: "promptStore"},
	} {
		if actual, ok := functionParamType(t, root, constructorPath, check.funcName, check.param); ok {
			violations = append(violations, fmt.Sprintf("%s: unused %s.%s dependency must be removed, got %s", constructorPath, check.funcName, check.param, actual))
		}
	}
	failIfViolations(t, violations)
}
