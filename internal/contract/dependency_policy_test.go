package contract

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDependencyAbsencePolicyRejectsProductionMissingDependencies(t *testing.T) {
	for _, policy := range RegisteredDependencyAbsencePolicies() {
		if policy.Profile == DependencyProfileProduction {
			t.Fatalf("RegisteredDependencyAbsencePolicies() includes production policy %#v, want none", policy)
		}
		if AllowsMissingDependency(policy.Name, DependencyProfileProduction) {
			t.Fatalf("AllowsMissingDependency(%q, production) = true, want false", policy.Name)
		}
		err := MissingDependencyModeError(policy.Name, DependencyProfileProduction)
		if IsDependencyModeError(err, policy.Name, DependencyProfileProduction, ErrUnsupportedDependencyMode) {
			t.Fatalf("MissingDependencyModeError(%q, production) returned typed unsupported error: %v", policy.Name, err)
		}
	}
}

func TestDependencyAbsencePolicyAllowsOnlyNamedDesktopDependencies(t *testing.T) {
	want := map[string]DependencyAbsenceReason{
		"runtime_reporter.orchestration_service":  DependencyAbsenceDesktopExternal,
		"toolbridge.agent_thread_lookup":          DependencyAbsenceDesktopExternal,
		"toolbridge.thread_config_override_store": DependencyAbsenceDesktopExternal,
		"thread.bind_session_generation":          DependencyAbsenceDesktopExternal,
	}

	assertPolicySet(t, DependencyProfileDesktopHost, want)
	assertPolicyRejects(t, "toolbridge.lifecycle_backfiller", DependencyProfileDesktopHost)
	assertPolicyRejects(t, "toolbridge.skill_tools", DependencyProfileDesktopHost)
}

func TestDependencyAbsencePolicyAllowsOnlyNamedTestDependencies(t *testing.T) {
	want := map[string]DependencyAbsenceReason{
		"runtime_reporter.orchestration_service": DependencyAbsenceDesktopExternal,
		"toolbridge.lifecycle_backfiller":        DependencyAbsenceTestHarness,
		"toolbridge.skill_tools":                 DependencyAbsenceTestHarness,
		"thread.bind_session_generation":         DependencyAbsenceDesktopExternal,
	}

	assertPolicySet(t, DependencyProfileTest, want)
	assertPolicyRejects(t, "toolbridge.agent_thread_lookup", DependencyProfileTest)
	assertPolicyRejects(t, "toolbridge.thread_config_override_store", DependencyProfileTest)
}

func TestDependencyAbsencePolicyRejectsUnknownDependency(t *testing.T) {
	const dependency = "toolbridge.unregistered_dependency"

	for _, profile := range []DependencyProfile{
		DependencyProfileProduction,
		DependencyProfileDesktopHost,
		DependencyProfileTest,
	} {
		assertPolicyRejects(t, dependency, profile)
	}
}

func TestDependencyAbsencePolicyRejectsEmptyProfile(t *testing.T) {
	const dependency = "runtime_reporter.orchestration_service"

	if AllowsMissingDependency(dependency, "") {
		t.Fatalf("AllowsMissingDependency(%q, empty profile) = true, want false", dependency)
	}
	err := MissingDependencyModeError(dependency, "")
	if err == nil {
		t.Fatal("MissingDependencyModeError(empty profile) = nil, want error")
	}
	if IsDependencyModeError(err, dependency, "", ErrUnsupportedDependencyMode) {
		t.Fatalf("MissingDependencyModeError(empty profile) returned typed unsupported error: %v", err)
	}
	if !strings.Contains(err.Error(), "dependency profile is required") {
		t.Fatalf("MissingDependencyModeError(empty profile) = %q, want profile-required error", err.Error())
	}
}

func assertPolicySet(t *testing.T, profile DependencyProfile, want map[string]DependencyAbsenceReason) {
	t.Helper()

	got := map[string]DependencyAbsenceReason{}
	for _, policy := range RegisteredDependencyAbsencePolicies() {
		if policy.Profile != profile {
			continue
		}
		assertRegisteredPolicyMetadata(t, policy)
		got[policy.Name] = policy.Reason
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policies for %s = %#v, want %#v", profile, got, want)
	}
	for name := range want {
		assertPolicyAllows(t, name, profile)
	}
}

func assertRegisteredPolicyMetadata(t *testing.T, policy DependencyAbsencePolicy) {
	t.Helper()

	if policy.Name == "" {
		t.Fatalf("policy for %s has empty name: %#v", policy.Profile, policy)
	}
	if policy.Owner == "" {
		t.Fatalf("policy %q for %s has empty owner", policy.Name, policy.Profile)
	}
	if policy.Error == nil {
		t.Fatalf("policy %q for %s has nil error", policy.Name, policy.Profile)
	}
	if !errors.Is(policy.Error, ErrUnsupportedDependencyMode) {
		t.Fatalf("policy %q for %s error = %v, want ErrUnsupportedDependencyMode", policy.Name, policy.Profile, policy.Error)
	}
	if !IsDependencyModeError(policy.Error, policy.Name, policy.Profile, ErrUnsupportedDependencyMode) {
		t.Fatalf("policy %q for %s error lost dependency/profile context: %v", policy.Name, policy.Profile, policy.Error)
	}
}

func assertPolicyAllows(t *testing.T, name string, profile DependencyProfile) {
	t.Helper()

	if !AllowsMissingDependency(name, profile) {
		t.Fatalf("AllowsMissingDependency(%q, %s) = false, want true", name, profile)
	}
	err := MissingDependencyModeError(name, profile)
	if !IsDependencyModeError(err, name, profile, ErrUnsupportedDependencyMode) {
		t.Fatalf("MissingDependencyModeError(%q, %s) = %v, want typed unsupported error", name, profile, err)
	}
}

func assertPolicyRejects(t *testing.T, name string, profile DependencyProfile) {
	t.Helper()

	if AllowsMissingDependency(name, profile) {
		t.Fatalf("AllowsMissingDependency(%q, %s) = true, want false", name, profile)
	}
	err := MissingDependencyModeError(name, profile)
	if err == nil {
		t.Fatalf("MissingDependencyModeError(%q, %s) = nil, want error", name, profile)
	}
	if IsDependencyModeError(err, name, profile, ErrUnsupportedDependencyMode) {
		t.Fatalf("MissingDependencyModeError(%q, %s) returned typed unsupported error: %v", name, profile, err)
	}
}
