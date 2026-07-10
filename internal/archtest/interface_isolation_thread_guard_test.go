package archtest_test

import (
	"fmt"
	"testing"
)

func threadInterfaceIsolationBudgets() []interfaceBudget {
	return []interfaceBudget{
		{relPath: "internal/module/thread/module.go", name: "threadServiceStorePort", maxMethods: 10, maxEmbedded: 0},
		{relPath: "internal/module/thread/module.go", name: "bindingServiceStorePort", maxMethods: 9, maxEmbedded: 0},
	}
}

func TestThreadServiceDropsUnusedStoreDependencies(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const modulePath = "internal/module/thread/module.go"
	const servicePath = "internal/module/thread/service.go"
	const constructorPath = "internal/module/thread/service_constructor.go"
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
