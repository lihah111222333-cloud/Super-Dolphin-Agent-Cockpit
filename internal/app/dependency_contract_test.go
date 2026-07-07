package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestDependencyContractRequiresProductionRuntimeReporter(t *testing.T) {
	policy := newDependencyContract(contract.DependencyProfileProduction)
	err := policy.Require("runtime_reporter.orchestration_service", nil)
	if err == nil {
		t.Fatal("Require() error = nil, want missing dependency")
	}
}

func TestDependencyContractAllowsDesktopRuntimeReporterNoop(t *testing.T) {
	policy := newDependencyContract(contract.DependencyProfileDesktopHost)
	if err := policy.Require("runtime_reporter.orchestration_service", nil); err != nil {
		t.Fatalf("desktop runtime reporter dependency error = %v", err)
	}
}

func TestDependencyContractAllowedMissingDependenciesComeFromSharedPolicy(t *testing.T) {
	for _, profile := range []contract.DependencyProfile{
		contract.DependencyProfileDesktopHost,
		contract.DependencyProfileTest,
	} {
		dependencies := appDependencyAbsencePolicyNamesForTest(profile)
		if len(dependencies) == 0 {
			t.Fatalf("shared app dependency absence policies for %s profile are empty", profile)
		}
		for _, dependency := range dependencies {
			err := newDependencyContract(profile).Require(dependency, nil)
			if err != nil {
				t.Fatalf("Require(%q, nil) in %s profile error = %v, want shared policy allowance", dependency, profile, err)
			}
		}
	}
}

func TestDependencyContractRejectsUnknownOptionalDependency(t *testing.T) {
	policy := newDependencyContract(contract.DependencyProfileDesktopHost)
	err := policy.Require("unknown.optional", nil)
	if err == nil {
		t.Fatal("Require() error = nil, want unknown optional dependency failure")
	}
}

func TestDependencyContractRejectsEmptyProfile(t *testing.T) {
	policy := newDependencyContract("")
	err := policy.Require("runtime_reporter.orchestration_service", nil)
	if err == nil {
		t.Fatal("Require() error = nil, want missing dependency profile failure")
	}
}

func TestDependencyContractTypedUnsupported(t *testing.T) {
	err := dependencyUnsupported("thread.bind_session_generation", contract.DependencyProfileDesktopHost)
	if !errors.Is(err, contract.ErrUnsupportedDependencyMode) {
		t.Fatalf("error = %v, want ErrUnsupportedDependencyMode", err)
	}
}

func appDependencyAbsencePolicyNamesForTest(profile contract.DependencyProfile) []string {
	var names []string
	for _, policy := range contract.RegisteredDependencyAbsencePolicies() {
		if policy.Profile == profile && policy.Owner == "internal/app" {
			names = append(names, policy.Name)
		}
	}
	return names
}

func TestNewRuntimeReporterFailsInProductionWithoutOrchestrationService(t *testing.T) {
	_, err := newRuntimeReporter(runtimeReporterParams{
		Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction},
	})
	if err == nil {
		t.Fatal("newRuntimeReporter() error = nil, want missing orchestration service")
	}
	if !strings.Contains(err.Error(), "runtime_reporter.orchestration_service") {
		t.Fatalf("newRuntimeReporter() error = %v, want runtime reporter dependency name", err)
	}
}

func TestNewRuntimeReporterAllowsDesktopExternalOrchestration(t *testing.T) {
	reporter, err := newRuntimeReporter(runtimeReporterParams{
		Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileDesktopHost},
	})
	if err != nil {
		t.Fatalf("newRuntimeReporter() error = %v", err)
	}
	if _, ok := reporter.(desktopExternalRuntimeReporter); !ok {
		t.Fatalf("reporter = %T, want desktopExternalRuntimeReporter", reporter)
	}
	err = reporter.ReportRuntime(context.Background(), contract.RuntimeReport{AgentID: "agent-1", Provider: "codex"})
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("ReportRuntime() error = %v, want ErrDependencyDeferred", err)
	}
	if !contract.IsDependencyModeError(err, "runtime_reporter.orchestration_service", contract.DependencyProfileDesktopHost, contract.ErrDependencyDeferred) {
		t.Fatalf("ReportRuntime() error = %v, want typed desktop deferred dependency", err)
	}
}
