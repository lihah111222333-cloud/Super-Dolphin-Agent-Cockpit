package archtest

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

const (
	rootSQLCBoundaryRuleID         BoundaryRuleID = "store_sqlc_store_platform_only"
	orchestratorSQLCBoundaryRuleID BoundaryRuleID = "mcp_orch_sqlc_sidecar_only"
	storeDependencyRuleID          BoundaryRuleID = "store_dependency_surface"
)

type configuredSQLCRuleState struct {
	root, orchestrator, store bool
}

func defaultOrchestratorSQLCRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           orchestratorSQLCBoundaryRuleID,
		Owner:        "sqlc_boundary",
		Reason:       "mcp-orch SQLC generated types remain private to the orchestrator sidecar",
		Kind:         BoundaryRuleScopedImport,
		FilePatterns: patterns.sqlc,
		Deny:         boundaryPolicies("sqlc_boundary", patterns.sqlc, []string{sqlcconfig.OrchestratorOutputOwnerParent}, "orchestrator SQLC imports stay inside mcp-orch"),
		ScopeAllow: []BoundaryFilePolicy{
			boundaryFilePolicy("sqlc_boundary", BoundaryScopeFXMCPOrch, "mcp-orch owns its configured SQLC output"),
		},
		SkipTestFiles: true,
	}
}

func bindConfiguredSQLCBoundary(root string, registry BackendBoundaryRegistry) (BackendBoundaryRegistry, error) {
	rootOutput, orchestratorOutput, err := configuredSQLCOutputs(root)
	if err != nil {
		return BackendBoundaryRegistry{}, err
	}
	bound := cloneBackendBoundaryRegistry(registry)
	state := configuredSQLCRuleState{}
	for index := range bound.Rules {
		if err := bindConfiguredSQLCRule(&bound.Rules[index], rootOutput, orchestratorOutput, &state); err != nil {
			return BackendBoundaryRegistry{}, err
		}
	}
	if !state.root || !state.orchestrator || !state.store {
		return BackendBoundaryRegistry{}, fmt.Errorf("configured SQLC boundary requires root, orchestrator, and store dependency rules")
	}
	return bound, nil
}

func configuredSQLCOutputs(root string) (string, string, error) {
	units, err := sqlcconfig.LoadDefault(root)
	if err != nil {
		return "", "", fmt.Errorf("load configured SQLC boundary: %w", err)
	}
	rootOutput, err := sqlcconfig.OutputForConfig(units, sqlcconfig.RootConfigPath)
	if err != nil {
		return "", "", err
	}
	orchestratorOutput, err := sqlcconfig.OutputForConfig(units, sqlcconfig.OrchestratorConfigPath)
	return rootOutput, orchestratorOutput, err
}

func bindConfiguredSQLCRule(rule *BackendBoundaryRule, rootOutput, orchestratorOutput string, state *configuredSQLCRuleState) error {
	switch rule.ID {
	case rootSQLCBoundaryRuleID:
		return bindConfiguredSQLCDeny(rule, rootOutput, &state.root)
	case orchestratorSQLCBoundaryRuleID:
		return bindConfiguredSQLCDeny(rule, orchestratorOutput, &state.orchestrator)
	case storeDependencyRuleID:
		return bindConfiguredSQLCStoreAllow(rule, rootOutput, &state.store)
	default:
		return nil
	}
}

func bindConfiguredSQLCDeny(rule *BackendBoundaryRule, output string, bound *bool) error {
	if *bound || len(rule.Deny) == 0 {
		return fmt.Errorf("configured SQLC deny rule %q must be unique and carry policy", rule.ID)
	}
	for index := range rule.Deny {
		rule.Deny[index].ImportPrefix = output
	}
	*bound = true
	return nil
}

func bindConfiguredSQLCStoreAllow(rule *BackendBoundaryRule, output string, bound *bool) error {
	if *bound {
		return fmt.Errorf("configured SQLC store dependency rule must be unique")
	}
	rule.Allow = append(rule.Allow, boundaryPolicies(rule.Owner, rule.FilePatterns, []string{output}, "store packages may consume the configured SQLC output")...)
	*bound = true
	return nil
}
