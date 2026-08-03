package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIRejectsHostDockerTestWrapper prevents the retired local Docker
// test-only timeout path from being reintroduced beside the ECI-only flow.
func TestRemoteCIRejectsHostDockerTestWrapper(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "scripts", "test_with_guard.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read remote CI test wrapper %s: %v", path, err)
	}
	for _, forbidden := range []string{
		"SUPER_DOLPHIN_GATE_PRODUCTION_DOCKER_E2E",
		"production_docker_e2e_wrapper_timeout",
		"TestProductionProvisionBootstrapOwnerHookDockerE2E",
		"TestProductionProvisionBootstrapOwnerReleaseCLIDockerE2E",
	} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("remote CI test wrapper must not restore retired host Docker path %q", forbidden)
		}
	}
}
