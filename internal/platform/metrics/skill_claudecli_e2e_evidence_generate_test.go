package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureClaudeCLIE2EEvidenceGenerateScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02t",
		"SKILL_PD_CLAUDECLI_E2E_OUTPUT_FILE",
		"SKILL_PD_CLAUDECLI_E2E_RAW_OUTPUT_FILE",
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE_OUT",
		"SKILL_PD_VERSION_COMMIT",
		"SKILL_PD_OPERATOR",
		"SKILL_PD_AUTHENTICATED_ENVIRONMENT",
		"SKILL_PD_CLAUDECLI_E2E_COMMAND",
		"Evidence type: authenticated-claudecli-e2e",
		"Authenticated environment: true",
		"TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"raw E2E output must not contain skip markers",
		"raw E2E output indicates unauthenticated environment",
		"raw E2E output missing PASS marker",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing authenticated Claude CLI E2E evidence generator token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosureClaudeCLIE2EEvidenceGeneratePassAndFailClosed(t *testing.T) {
	script := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh"
	tempDir := t.TempDir()
	rawOutput := filepath.Join(tempDir, "claudecli-e2e-output.txt")
	out := filepath.Join(tempDir, "claudecli-e2e-evidence.md")
	rawBody := strings.Join([]string{
		"=== RUN   TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"--- PASS: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E (12.34s)",
		"PASS",
		"ok  github.com/lihah111222333-cloud/Super-Dolphin/cmd/agent-terminal 12.345s",
		"",
	}, "\n")
	if err := os.WriteFile(rawOutput, []byte(rawBody), 0o644); err != nil {
		t.Fatalf("write raw E2E output: %v", err)
	}

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_CLAUDECLI_E2E_OUTPUT_FILE="+rawOutput,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE_OUT="+out,
		"SKILL_PD_VERSION_COMMIT=abc123",
		"SKILL_PD_OPERATOR=release-owner",
		"SKILL_PD_AUTHENTICATED_ENVIRONMENT=true",
		"SKILL_PD_CLAUDECLI_E2E_COMMAND=go test ./cmd/agent-terminal -run '^TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E$' -count=1",
		"SKILL_PD_EVIDENCE_DATE=2026-04-28",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected evidence generation pass: %v\n%s", err, string(output))
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read generated evidence: %v", err)
	}
	evidence := string(body)
	for _, token := range []string{
		"Evidence type: authenticated-claudecli-e2e",
		"Evidence date: 2026-04-28",
		"Version / commit: abc123",
		"Operator: release-owner",
		"Authenticated environment: true",
		"Command: go test ./cmd/agent-terminal -run '^TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E$' -count=1",
		"Test name: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"Result: PASS",
		"Skip status: none",
		"--- PASS: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
	} {
		if !strings.Contains(evidence, token) {
			t.Fatalf("generated evidence missing %q\n%s", token, evidence)
		}
	}
	if strings.Contains(evidence, "TODO") || strings.Contains(evidence, "SKIP") {
		t.Fatalf("generated evidence contains forbidden placeholder/skip token:\n%s", evidence)
	}

	skipOutput := filepath.Join(tempDir, "skip-output.txt")
	if err := os.WriteFile(skipOutput, []byte(strings.Replace(rawBody, "--- PASS:", "--- SKIP:", 1)), 0o644); err != nil {
		t.Fatalf("write skip output: %v", err)
	}
	skipCmd := exec.Command("bash", script)
	skipCmd.Env = append(os.Environ(),
		"SKILL_PD_CLAUDECLI_E2E_OUTPUT_FILE="+skipOutput,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE_OUT="+filepath.Join(tempDir, "skip-evidence.md"),
		"SKILL_PD_VERSION_COMMIT=abc123",
		"SKILL_PD_OPERATOR=release-owner",
		"SKILL_PD_AUTHENTICATED_ENVIRONMENT=true",
	)
	skipCmdOutput, err := skipCmd.CombinedOutput()
	if err == nil || !strings.Contains(string(skipCmdOutput), "raw E2E output must not contain skip markers") {
		t.Fatalf("expected skip fail, got err=%v\n%s", err, string(skipCmdOutput))
	}

	unauth := exec.Command("bash", script)
	unauth.Env = append(os.Environ(),
		"SKILL_PD_CLAUDECLI_E2E_OUTPUT_FILE="+rawOutput,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE_OUT="+filepath.Join(tempDir, "unauth-evidence.md"),
		"SKILL_PD_VERSION_COMMIT=abc123",
		"SKILL_PD_OPERATOR=release-owner",
		"SKILL_PD_AUTHENTICATED_ENVIRONMENT=false",
	)
	unauthOutput, err := unauth.CombinedOutput()
	if err == nil || !strings.Contains(string(unauthOutput), "SKILL_PD_AUTHENTICATED_ENVIRONMENT must be true") {
		t.Fatalf("expected unauthenticated fail, got err=%v\n%s", err, string(unauthOutput))
	}
}
