package archtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCITruthImageCoordinatorGuardsDesktopAndSidecars(t *testing.T) {
	root := repoRootForCICrossPlatformSmokeGuard(t)
	workflowPath := filepath.Join(root, ".github", "workflows", "ci.yml")
	scriptPath := filepath.Join(root, "scripts", "ci_truth_image_gate.sh")
	coordinatorPath := filepath.Join(root, "cmd", "super-dolphin-gate", "coordinator_cli.go")
	workflow := readGuardFile(t, workflowPath)
	script := readGuardFile(t, scriptPath)
	coordinator := readGuardFile(t, coordinatorPath)
	assertCITruthImageRequiredWorkflowTokens(t, workflow)
	assertCITruthImageThinEntrypoint(t, workflow, script, coordinator)
	assertCITruthImageCoordinatorInvocationCounts(t, workflow)
	assertCITruthImageCoordinatorHostActions(t, workflow)
}

func assertCITruthImageRequiredWorkflowTokens(t *testing.T, workflow string) {
	t.Helper()
	requiredWorkflowTokens := []string{
		"pull_request_target:",
		"id-token: write",
		"truth-image-gates:",
		"Trusted bootstrap coordinator",
		"timeout-minutes: 75",
		"actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
		"persist-credentials: false",
		"SUPER_DOLPHIN_GATE_BOOTSTRAP_IMAGE",
		"SUPER_DOLPHIN_GATE_AUTHORITY_BUNDLE_B64",
		"docker pull --platform=linux/amd64 \"$SUPER_DOLPHIN_GATE_BOOTSTRAP_IMAGE\"",
		"--cpus=4",
		"--memory=8g",
		"workflow-host",
		"--repository-root /workspace/super-dolphin-checkout",
		"--env SUPER_DOLPHIN_GATE_AUTHORITY_BUNDLE_B64",
		"--env ACTIONS_ID_TOKEN_REQUEST_URL",
		"--env ACTIONS_ID_TOKEN_REQUEST_TOKEN",
		"target=/workspace/super-dolphin-checkout,readonly",
		"target=\"$runtime_root\"",
	}
	for _, want := range requiredWorkflowTokens {
		if !strings.Contains(workflow, want) {
			t.Fatalf(".github/workflows/ci.yml cross-platform smoke missing %q", want)
		}
	}
}

func assertCITruthImageThinEntrypoint(t *testing.T, workflow, script, coordinator string) {
	t.Helper()
	for _, required := range []string{
		"command -v super-dolphin-gate",
		"exec \"$gate_bin\" workflow-host",
		"submit --wait",
		"exec \"$gate_bin\" _production-launcher \"${submit_args[@]}\"",
		"exec \"$gate_bin\" \"${submit_args[@]}\"",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("ci_truth_image_gate.sh thin coordinator entrypoint missing %q", required)
		}
	}
	if !strings.Contains(coordinator, "runProductionReleaseSubmitPlanWithWaitConnector(") {
		t.Fatal("production release submit connector must wait for the authoritative terminal status")
	}
	if strings.Contains(workflow, "ci_truth_image_gate.sh") {
		t.Fatal("protected workflow must not execute a candidate script")
	}
	for _, forbidden := range []string{
		"SUPER_DOLPHIN_GATE_AUTHORITY_BUNDLE_B64",
		" trusted.git",
		"--pull never",
		"docker run",
		"go test",
		"TEST_WITH_GUARD",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("thin CI entrypoint contains authority or host-gate bypass %q", forbidden)
		}
	}
}

func assertCITruthImageCoordinatorInvocationCounts(t *testing.T, workflow string) {
	t.Helper()
	if count := strings.Count(workflow, "docker pull "); count != 1 {
		t.Fatalf("protected workflow docker pull count = %d, want 1", count)
	}
	if count := strings.Count(workflow, "docker run --rm"); count != 1 {
		t.Fatalf("protected workflow docker run count = %d, want 1", count)
	}
}

func assertCITruthImageCoordinatorHostActions(t *testing.T, workflow string) {
	t.Helper()
	for line := range strings.SplitSeq(workflow, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "run:") && line != "run: |" {
			t.Fatalf(".github/workflows/ci.yml has a host CI action outside the truth-image coordinator: %q", line)
		}
		if strings.HasPrefix(line, "uses:") && line != "uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683" {
			t.Fatalf(".github/workflows/ci.yml has an unapproved action outside the truth-image coordinator: %q", line)
		}
	}
}

func TestCITruthImageBootstrapRejectsMutableImageReference(t *testing.T) {
	root := repoRootForCICrossPlatformSmokeGuard(t)
	command := exec.Command("bash", "-c", protectedWorkflowRunScript(t, root))
	command.Dir = root
	command.Env = append(os.Environ(),
		"SUPER_DOLPHIN_GATE_BOOTSTRAP_IMAGE=registry.example/super-dolphin/bootstrap:latest",
		"RUNNER_TEMP="+t.TempDir(), "GITHUB_WORKSPACE="+t.TempDir(),
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "immutable bootstrap image digest is required") {
		t.Fatalf("mutable bootstrap image result error=%v output=%s", err, output)
	}
}

func readGuardFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func repoRootForCICrossPlatformSmokeGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, ".github", "workflows", "ci.yml")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repository root not found from %s", dir)
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
