package archtest_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func orchestrationServiceUsesFromSource(
	t *testing.T,
	relPath string,
	src string,
	packageAliases map[string]orchestrationServiceAliasSource,
) []orchestrationServiceUse {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	contractAliases := contractImportAliases(t, relPath, file)
	return collectOrchestrationServiceUses(fset, file, relPath, contractAliases, packageAliases)
}

func unexpectedOrchestrationServiceUseMessages(uses []orchestrationServiceUse) []string {
	var violations []string
	for _, use := range uses {
		if isAllowedOrchestrationServiceSemanticUse(use) {
			continue
		}
		violations = append(violations, use.violationMessage())
	}
	return violations
}

type orchestrationServiceSemanticGuardCase struct {
	name         string
	relPath      string
	src          string
	packageAlias map[string]orchestrationServiceAliasSource
	wantContains []string
}

func TestOrchestrationServiceSemanticGuardFixtures(t *testing.T) {
	t.Parallel()

	for _, tt := range orchestrationServiceSemanticGuardCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := unexpectedOrchestrationServiceUseMessages(orchestrationServiceUsesFromSource(t, tt.relPath, tt.src, tt.packageAlias))
			if len(tt.wantContains) == 0 {
				if len(got) > 0 {
					t.Fatalf("unexpected violations:\n%s", strings.Join(got, "\n"))
				}
				return
			}
			for _, want := range tt.wantContains {
				if !containsViolation(got, want) {
					t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

func orchestrationServiceSemanticGuardCases() []orchestrationServiceSemanticGuardCase {
	cases := []orchestrationServiceSemanticGuardCase{narrowRPCFacadeGuardCase()}
	cases = append(cases, rejectedServiceFacadeGuardCases()...)
	return append(cases, rejectedRPCFacadeGuardCases()...)
}

func fullServicePackageAliases() map[string]orchestrationServiceAliasSource {
	return map[string]orchestrationServiceAliasSource{
		"Service": {name: "Service", relPath: orchestrationServiceFacadeRelPath, line: 39},
	}
}

func narrowRPCFacadeGuardCase() orchestrationServiceSemanticGuardCase {
	return orchestrationServiceSemanticGuardCase{
		name:    "rpc facade allows narrow local interface",
		relPath: orchestrationRPCFacadeRelPath,
		src: `package fixture

type rpcFacadeService interface {
	Snapshot(ctx any, agentID string) (any, error)
}

func ProvideRPCFacade(svc rpcFacadeService) any { return nil }
func submissionFromParams(ctx any, svc rpcFacadeService, p any) (any, error) { return nil, nil }
func submissionThreadID(ctx any, svc rpcFacadeService, agentID string) string { return "" }
`,
	}
}

func rejectedServiceFacadeGuardCases() []orchestrationServiceSemanticGuardCase {
	cases := rejectedServiceFacadeDeclarationGuardCases()
	cases = append(cases, rejectedServiceFacadeExpressionGuardCases()...)
	cases = append(cases, rejectedServiceFacadeFunctionExpressionGuardCases()...)
	return append(cases, rejectedServiceFacadeInitializerGuardCases()...)
}

func rejectedServiceFacadeDeclarationGuardCases() []orchestrationServiceSemanticGuardCase {
	return []orchestrationServiceSemanticGuardCase{
		{
			name:    "service facade rejects local alias",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
type FullService = contract.OrchestrationService
`,
			wantContains: []string{"type alias FullService uses full orchestration service"},
		},
		{
			name:    "service facade rejects field",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
type holder struct { service Service }
`,
			wantContains: []string{"field service uses full orchestration service"},
		},
		{
			name:    "service facade rejects parameter",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use(svc Service) {}
`,
			wantContains: []string{"parameter svc in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects return",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() Service { return nil }
`,
			wantContains: []string{"return value (anonymous) in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects variable",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
var svc Service
`,
			wantContains: []string{"variable svc uses full orchestration service"},
		},
		{
			name:    "service facade rejects function type parameter",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use[T contract.OrchestrationService]() {}
`,
			wantContains: []string{"type parameter T in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects type parameter",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
type holder[T contract.OrchestrationService] struct{}
`,
			wantContains: []string{"type parameter T uses full orchestration service"},
		},
	}
}

func rejectedServiceFacadeExpressionGuardCases() []orchestrationServiceSemanticGuardCase {
	return []orchestrationServiceSemanticGuardCase{
		{
			name:    "service facade rejects type assertion",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use(value any) { _, _ = value.(contract.OrchestrationService) }
`,
			wantContains: []string{"type assertion in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects type conversion",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use(value any) { _ = contract.OrchestrationService(value) }
`,
			wantContains: []string{"type conversion in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects composite literal",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() { _ = []contract.OrchestrationService{} }
`,
			wantContains: []string{"composite literal in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects builtin type argument",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() { _ = make([]contract.OrchestrationService, 0) }
`,
			wantContains: []string{"builtin type argument in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects type switch case",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use(value any) {
	switch value.(type) {
	case contract.OrchestrationService:
	}
}
`,
			wantContains: []string{"type switch case in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects method expression",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() { _ = contract.OrchestrationService.SubmitTurn }
`,
			wantContains: []string{"method expression in use uses full orchestration service"},
		},
	}
}

func rejectedServiceFacadeFunctionExpressionGuardCases() []orchestrationServiceSemanticGuardCase {
	return []orchestrationServiceSemanticGuardCase{
		{
			name:    "service facade rejects function literal parameter",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() { _ = func(svc contract.OrchestrationService) {} }
`,
			wantContains: []string{"parameter svc in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects function literal return",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() { _ = func() contract.OrchestrationService { return nil } }
`,
			wantContains: []string{"return value (anonymous) in use uses full orchestration service"},
		},
	}
}

func rejectedServiceFacadeInitializerGuardCases() []orchestrationServiceSemanticGuardCase {
	return []orchestrationServiceSemanticGuardCase{
		{
			name:    "service facade rejects function literal parameter in local var initializer",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() { var f = func(svc contract.OrchestrationService) {}; _ = f }
`,
			wantContains: []string{"parameter svc in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects method expression in local var initializer",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() { var submit = contract.OrchestrationService.SubmitTurn; _ = submit }
`,
			wantContains: []string{"method expression in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects function literal return in package var initializer",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
var _ = func() contract.OrchestrationService { return nil }
`,
			wantContains: []string{"return value (anonymous) uses full orchestration service"},
		},
	}
}

func rejectedRPCFacadeGuardCases() []orchestrationServiceSemanticGuardCase {
	return []orchestrationServiceSemanticGuardCase{
		{
			name:    "rpc facade rejects local alias source",
			relPath: orchestrationRPCFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func ProvideRPCFacade(svc Service) any { return nil }
`,
			wantContains: []string{"type alias Service uses full orchestration service", "parameter svc in ProvideRPCFacade uses full orchestration service"},
		},
		{
			name:         "rpc facade rejects field even with package full service alias",
			relPath:      orchestrationRPCFacadeRelPath,
			src:          "package fixture\ntype holder struct { service Service }\n",
			packageAlias: fullServicePackageAliases(),
			wantContains: []string{"field service uses full orchestration service"},
		},
		{
			name:    "rpc facade rejects facade parameter with package full service alias",
			relPath: orchestrationRPCFacadeRelPath,
			src: `package fixture
func ProvideRPCFacade(svc Service) any { return nil }
func submissionFromParams(ctx any, svc Service, p any) (any, error) { return nil, nil }
func submissionThreadID(ctx any, svc Service, agentID string) string { return "" }
`,
			packageAlias: fullServicePackageAliases(),
			wantContains: []string{
				"parameter svc in ProvideRPCFacade uses full orchestration service",
				"parameter svc in submissionFromParams uses full orchestration service",
				"parameter svc in submissionThreadID uses full orchestration service",
			},
		},
		{
			name:         "rpc facade rejects return even with package full service alias",
			relPath:      orchestrationRPCFacadeRelPath,
			src:          "package fixture\nfunc helper() Service { return nil }\n",
			packageAlias: fullServicePackageAliases(),
			wantContains: []string{"return value (anonymous) in helper uses full orchestration service"},
		},
		{
			name:         "cross-file package alias does not bypass non-rpc files",
			relPath:      "cmd/mcp-orch/orchestration/other.go",
			src:          "package fixture\nfunc helper(svc Service) {}\n",
			packageAlias: fullServicePackageAliases(),
			wantContains: []string{"cmd/mcp-orch/orchestration/other.go:2 parameter svc in helper uses full orchestration service"},
		},
		{
			name:         "rpc facade rejects package alias type assertion",
			relPath:      orchestrationRPCFacadeRelPath,
			src:          "package fixture\nfunc helper(value any) { _, _ = value.(Service) }\n",
			packageAlias: fullServicePackageAliases(),
			wantContains: []string{"type assertion in helper uses full orchestration service"},
		},
	}
}

func containsViolation(violations []string, want string) bool {
	for _, violation := range violations {
		if strings.Contains(violation, want) {
			return true
		}
	}
	return false
}
