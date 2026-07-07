package config

import (
	"strings"
	"testing"
)

func TestResolveDependencyProfileAllowsDesktopBootstrapDefault(t *testing.T) {
	got, err := resolveDependencyProfile("", DependencyBootstrapDesktopHost)
	if err != nil {
		t.Fatalf("resolveDependencyProfile() error = %v", err)
	}
	if got != DependencyProfileDesktopHost {
		t.Fatalf("profile = %q, want %q", got, DependencyProfileDesktopHost)
	}
}

func TestResolveDependencyProfileRequiresProductionExplicit(t *testing.T) {
	_, err := resolveDependencyProfile("", DependencyBootstrapProduction)
	if err == nil {
		t.Fatal("resolveDependencyProfile() error = nil, want missing production profile error")
	}
}

func TestParseDependencyProfileAcceptsExplicitDesktopHost(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "desktop")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "desktop_host")
	got, err := dependencyProfileFromEnv()
	if err != nil {
		t.Fatalf("dependencyProfileFromEnv() error = %v", err)
	}
	if got != DependencyProfileDesktopHost {
		t.Fatalf("profile = %q, want %q", got, DependencyProfileDesktopHost)
	}
}

func TestParseDependencyProfileRejectsDesktopProfileInProductionBootstrap(t *testing.T) {
	_, err := resolveDependencyProfile("desktop_host", DependencyBootstrapProduction)
	if err == nil {
		t.Fatal("resolveDependencyProfile() error = nil, want production desktop profile rejection")
	}
}

func TestParseDependencyProfileRejectsUnknownValue(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "maybe-production")
	_, err := dependencyProfileFromEnv()
	if err == nil {
		t.Fatal("dependencyProfileFromEnv() error = nil, want invalid profile error")
	}
}

func TestResolveDependencyProfileAllowsExplicitTestBootstrap(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	got, err := dependencyProfileFromEnv()
	if err != nil {
		t.Fatalf("dependencyProfileFromEnv() error = %v", err)
	}
	if got != DependencyProfileTest {
		t.Fatalf("profile = %q, want %q", got, DependencyProfileTest)
	}
}

func TestParseDependencyBootstrapRejectsUnknownValue(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "desktop")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want invalid bootstrap error", err)
	}
}

func TestParseDependencyProfileRejectsTestProfileWithoutTestBootstrap(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "test")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency profile is allowed only with test bootstrap") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want profile-only test rejection", err)
	}
}

func TestParseDependencyProfileRejectsTestProfileWithExplicitDesktopBootstrap(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "desktop_host")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "test")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency profile is allowed only with test bootstrap") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want desktop bootstrap test profile rejection", err)
	}
}

func TestParseDependencyProfileRejectsTestProfileWithDesktopProcessRole(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "desktop")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "test")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency profile is allowed only with test bootstrap") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want desktop role test profile rejection", err)
	}
}

func TestParseDependencyBootstrapRejectsExplicitTestInPackagedRuntime(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency bootstrap is allowed only in Go test binaries") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want packaged test rejection", err)
	}
}

func TestParseDependencyBootstrapRejectsExplicitTestOutsideGoTestBinary(t *testing.T) {
	_, err := dependencyBootstrapMode("test", "", "", func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "test dependency bootstrap is allowed only in Go test binaries") {
		t.Fatalf("dependencyBootstrapMode() error = %v, want non-test-binary rejection", err)
	}
}

func TestParseDependencyBootstrapRejectsExplicitTestForSidecar(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	t.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "sidecar")
	_, err := dependencyProfileFromEnv()
	if err == nil || !strings.Contains(err.Error(), "test dependency bootstrap is allowed only in Go test binaries") {
		t.Fatalf("dependencyProfileFromEnv() error = %v, want sidecar test rejection", err)
	}
}

func TestNewRejectsUndeclaredDependencyBootstrapInGoTests(t *testing.T) {
	t.Setenv("PROJECT_ROOT", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "")
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("RPC_ADDR", "")

	_, err := New()
	if err == nil || !strings.Contains(err.Error(), "SUPER_DOLPHIN_DEPENDENCY_PROFILE is required") {
		t.Fatalf("New() error = %v, want missing dependency profile failure", err)
	}
}
