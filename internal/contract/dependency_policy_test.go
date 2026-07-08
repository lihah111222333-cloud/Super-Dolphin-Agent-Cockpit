package contract

import (
	"errors"
	"strings"
	"testing"
)

func TestDependencyAbsencePolicyRejectsProductionMissingDependencies(t *testing.T) {
	for _, policy := range RegisteredDependencyAbsencePolicies() {
		if policy.Profile == DependencyProfileProduction {
			t.Fatalf("registered policy %+v allows production missing dependency", policy)
		}
		if AllowsMissingDependency(policy.Name, DependencyProfileProduction) {
			t.Fatalf("AllowsMissingDependency(%q, production) = true, want false", policy.Name)
		}
	}
}

func TestDependencyAbsencePolicyAllowsOnlyNamedDesktopDependencies(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dependency string
		want       bool
	}{
		{
			name:       "desktop runtime reporter",
			dependency: "runtime_reporter.orchestration_service",
			want:       true,
		},
		{
			name:       "desktop toolbridge thread lookup",
			dependency: "toolbridge.agent_thread_lookup",
			want:       true,
		},
		{
			name:       "desktop toolbridge config override",
			dependency: "toolbridge.thread_config_override_store",
			want:       true,
		},
		{
			name:       "desktop bind session generation",
			dependency: "thread.bind_session_generation",
			want:       true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AllowsMissingDependency(tc.dependency, DependencyProfileDesktopHost)
			if got != tc.want {
				t.Fatalf("AllowsMissingDependency() = %v, want %v", got, tc.want)
			}
		})
	}
	if AllowsMissingDependency("toolbridge.lifecycle_backfiller", DependencyProfileDesktopHost) {
		t.Fatal("AllowsMissingDependency(test-only dependency, desktop) = true, want false")
	}
}

func TestDependencyAbsencePolicyAllowsOnlyNamedTestDependencies(t *testing.T) {
	for _, dependency := range []string{
		"runtime_reporter.orchestration_service",
		"toolbridge.lifecycle_backfiller",
		"toolbridge.skill_tools",
		"thread.bind_session_generation",
	} {
		if !AllowsMissingDependency(dependency, DependencyProfileTest) {
			t.Fatalf("AllowsMissingDependency(%q, test) = false, want true", dependency)
		}
	}
	if AllowsMissingDependency("toolbridge.agent_thread_lookup", DependencyProfileTest) {
		t.Fatal("AllowsMissingDependency(desktop-only dependency, test) = true, want false")
	}
}

func TestDependencyAbsencePolicyRejectsUnknownDependency(t *testing.T) {
	if AllowsMissingDependency("unknown.optional", DependencyProfileDesktopHost) {
		t.Fatal("AllowsMissingDependency(unknown.optional, desktop) = true, want false")
	}
	err := MissingDependencyModeError("unknown.optional", DependencyProfileDesktopHost)
	if err == nil || !strings.Contains(err.Error(), "unknown dependency absence policy") {
		t.Fatalf("MissingDependencyModeError() error = %v, want unknown dependency failure", err)
	}
}

func TestDependencyAbsencePolicyRejectsEmptyProfile(t *testing.T) {
	if AllowsMissingDependency("runtime_reporter.orchestration_service", "") {
		t.Fatal("AllowsMissingDependency(runtime_reporter, empty profile) = true, want false")
	}
	err := MissingDependencyModeError("runtime_reporter.orchestration_service", "")
	if err == nil || !strings.Contains(err.Error(), "dependency profile is required") {
		t.Fatalf("MissingDependencyModeError() error = %v, want dependency profile failure", err)
	}
}

func TestDependencyAbsencePolicyReturnsTypedModeErrorOnlyWhenAllowed(t *testing.T) {
	err := MissingDependencyModeError("thread.bind_session_generation", DependencyProfileDesktopHost)
	if !IsDependencyModeError(err, "thread.bind_session_generation", DependencyProfileDesktopHost, ErrUnsupportedDependencyMode) {
		t.Fatalf("MissingDependencyModeError() error = %v, want typed unsupported", err)
	}

	err = MissingDependencyModeError("thread.bind_session_generation", DependencyProfileProduction)
	if err == nil {
		t.Fatal("MissingDependencyModeError() error = nil, want production failure")
	}
	if errors.Is(err, ErrUnsupportedDependencyMode) {
		t.Fatalf("MissingDependencyModeError() error = %v, production must not be typed unsupported", err)
	}
	if !strings.Contains(err.Error(), "thread.bind_session_generation") ||
		!strings.Contains(err.Error(), string(DependencyProfileProduction)) {
		t.Fatalf("MissingDependencyModeError() error = %v, want dependency and profile", err)
	}
}

func TestRegisteredDependencyAbsencePoliciesReturnsCopy(t *testing.T) {
	policies := RegisteredDependencyAbsencePolicies()
	if len(policies) == 0 {
		t.Fatal("RegisteredDependencyAbsencePolicies() returned empty policy list")
	}
	policies[0].Name = "mutated"

	if !AllowsMissingDependency("runtime_reporter.orchestration_service", DependencyProfileDesktopHost) {
		t.Fatal("AllowsMissingDependency() = false after caller mutation, want immutable registry")
	}
}
