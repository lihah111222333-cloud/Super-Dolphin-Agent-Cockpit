package archtest_test

import (
	"strings"
	"testing"
)

type orchestrationServiceSSAGuardFixture struct {
	name         string
	files        map[string]string
	wantContains []string
	wantAbsent   []string
	wantEmpty    bool
}

func TestOrchestrationServiceSSAGuardFixtures(t *testing.T) {
	t.Parallel()

	target := mustLoadOrchestrationServiceTypeObject(t, repoRoot(t))
	for _, tt := range orchestrationServiceSSAGuardFixtures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := typeCheckOrchestrationServiceFixturePackage(t, target.Pkg(), tt.files)
			got := orchestrationServiceSSAUseMessages(collectOrchestrationServiceSSAUses(t, pkg, target))
			assertOrchestrationServiceSSAFixtureMessages(t, tt, got)
		})
	}
}

func assertOrchestrationServiceSSAFixtureMessages(
	t *testing.T,
	fixture orchestrationServiceSSAGuardFixture,
	got []string,
) {
	t.Helper()
	if fixture.wantEmpty && len(got) != 0 {
		t.Fatalf("got unexpected violations:\n%s", strings.Join(got, "\n"))
	}
	for _, want := range fixture.wantContains {
		if !containsViolation(got, want) {
			t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
		}
	}
	for _, unwanted := range fixture.wantAbsent {
		if containsViolation(got, unwanted) {
			t.Fatalf("unexpected violation containing %q; got:\n%s", unwanted, strings.Join(got, "\n"))
		}
	}
}

func orchestrationServiceSSAGuardFixtures() []orchestrationServiceSSAGuardFixture {
	return []orchestrationServiceSSAGuardFixture{
		orchestrationServiceSSASignatureFieldFixture(),
		orchestrationServiceSSAConversionFixture(),
		orchestrationServiceSSAGenericConstraintFixture(),
		orchestrationServiceSSAMethodValueFixture(),
		orchestrationServiceSSAMethodExpressionFixture(),
		orchestrationServiceSSABenignFixture(),
	}
}

func orchestrationServiceSSASignatureFieldFixture() orchestrationServiceSSAGuardFixture {
	return orchestrationServiceSSAGuardFixture{
		name: "signature and field propagation",
		files: map[string]string{
			"cmd/mcp-orch/orchestration/semantic.go": `package orchestration

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type holder struct { service contract.OrchestrationService }

func returns(svc contract.OrchestrationService) contract.OrchestrationService { return svc }
`,
		},
		wantContains: []string{
			"field service uses full orchestration service",
			"parameter svc in returns uses full orchestration service",
			"return value (anonymous) in returns uses full orchestration service",
		},
	}
}

func orchestrationServiceSSAConversionFixture() orchestrationServiceSSAGuardFixture {
	return orchestrationServiceSSAGuardFixture{
		name: "interface assertion and conversion propagation",
		files: map[string]string{
			"cmd/mcp-orch/orchestration/semantic.go": `package orchestration

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

func conversions(value any, svc contract.OrchestrationService) {
	_, _ = value.(contract.OrchestrationService)
	_ = any(svc)
}
`,
		},
		wantContains: []string{
			"interface assertion in conversions uses full orchestration service",
			"type conversion in conversions uses full orchestration service",
		},
	}
}

func orchestrationServiceSSAGenericConstraintFixture() orchestrationServiceSSAGuardFixture {
	return orchestrationServiceSSAGuardFixture{
		name: "generic constraint propagation",
		files: map[string]string{
			"internal/module/dashboard/semantic.go": `package dashboard

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

func generic[T contract.OrchestrationService](value T) {}
`,
		},
		wantContains: []string{
			"generic constraint T in generic uses full orchestration service",
		},
	}
}

func orchestrationServiceSSAMethodValueFixture() orchestrationServiceSSAGuardFixture {
	return orchestrationServiceSSAGuardFixture{
		name: "method value and closure capture propagation",
		files: map[string]string{
			"internal/platform/mcpcontrol/semantic.go": `package mcpcontrol

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

func methodValue(svc contract.OrchestrationService) any {
	submit := svc.SubmitTurn
	captured := func() { _ = svc }
	return func(next contract.OrchestrationService) { _ = submit; captured(); _ = next }
}
`,
		},
		wantContains: []string{
			"method value SubmitTurn in methodValue uses full orchestration service",
			"closure capture svc in methodValue uses full orchestration service",
			"function value propagation methodValue$2 in methodValue uses full orchestration service",
		},
	}
}

func orchestrationServiceSSAMethodExpressionFixture() orchestrationServiceSSAGuardFixture {
	return orchestrationServiceSSAGuardFixture{
		name: "method expression function value propagation",
		files: map[string]string{
			"internal/platform/mcpcontrol/semantic.go": `package mcpcontrol

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

func methodExpression() any { return contract.OrchestrationService.SubmitTurn }
`,
		},
		wantContains: []string{
			"method expression SubmitTurn in methodExpression uses full orchestration service",
		},
	}
}

func orchestrationServiceSSABenignFixture() orchestrationServiceSSAGuardFixture {
	return orchestrationServiceSSAGuardFixture{
		name: "benign narrow port and unrelated function values",
		files: map[string]string{
			"internal/platform/mcpcontrol/semantic.go": `package mcpcontrol

import "context"

type narrowPort interface {
	SubmitTurn(context.Context, string) error
}

func benign(port narrowPort) any {
	next := func(value string) string { return value }
	captured := func() { _ = port }
	captured()
	return next
}
`,
		},
		wantEmpty:  true,
		wantAbsent: []string{"uses full orchestration service"},
	}
}
