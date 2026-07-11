package archtest_test

import (
	"fmt"
	"testing"
)

func TestDashboardStoreReadersUseOwnerLocalInterfaces(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const moduleRelPath = "internal/module/dashboard/module.go"
	const adapterPackageRelPath = "internal/app/storeadapter/dashboard"
	var violations []string

	fieldChecks := []struct {
		field string
		want  string
	}{
		{field: "AgentStatuses", want: "AgentStatusReader"},
		{field: "SystemLogs", want: "SystemLogReader"},
		{field: "AuditLogs", want: "AuditLogReader"},
		{field: "BusLogs", want: "BusLogReader"},
		{field: "AILogs", want: "AILogReader"},
		{field: "DBQueries", want: "DBQueryExecutor"},
		{field: "CommandCards", want: "CommandCardReader"},
		{field: "Prompts", want: "PromptTemplateReader"},
		{field: "SharedFiles", want: "SharedFileReader"},
	}
	for _, check := range fieldChecks {
		actual, ok := structFieldType(t, root, moduleRelPath, "serviceParams", check.field)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s: serviceParams.%s not found", moduleRelPath, check.field))
			continue
		}
		if actual != check.want {
			violations = append(violations, fmt.Sprintf("%s: serviceParams.%s must depend on dashboard %s, got %s; keep store readers behind dashboard adapters", moduleRelPath, check.field, check.want, actual))
		}
	}

	adapterChecks := []struct {
		funcName  string
		paramName string
		want      string
	}{
		{funcName: "provideDashboardAgentStatusReader", paramName: "store", want: "agentstatusstore.Store"},
		{funcName: "provideDashboardSystemLogReader", paramName: "store", want: "systemlogstore.Store"},
		{funcName: "provideDashboardAuditLogReader", paramName: "store", want: "auditlogstore.Store"},
		{funcName: "provideDashboardBusLogReader", paramName: "store", want: "buslogstore.Store"},
		{funcName: "provideDashboardAILogReader", paramName: "store", want: "ailogstore.Store"},
		{funcName: "provideDashboardDBQueryExecutor", paramName: "store", want: "dbquerystore.Store"},
		{funcName: "provideDashboardCommandCardReader", paramName: "reader", want: "commandcardstore.Reader"},
		{funcName: "provideDashboardPromptTemplateReader", paramName: "reader", want: "promptstore.Reader"},
		{funcName: "provideDashboardSharedFileReader", paramName: "reader", want: "sharedfilestore.Reader"},
	}
	for _, check := range adapterChecks {
		adapterRelPath := singleFunctionFileInPackage(t, root, adapterPackageRelPath, check.funcName)
		actual, ok := functionParamType(t, root, adapterRelPath, check.funcName, check.paramName)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s: %s.%s not found", adapterRelPath, check.funcName, check.paramName))
			continue
		}
		if actual != check.want {
			violations = append(violations, fmt.Sprintf("%s: %s.%s must be the only %s adapter input, got %s", adapterRelPath, check.funcName, check.paramName, check.want, actual))
		}
	}
	failIfViolations(t, violations)
}
