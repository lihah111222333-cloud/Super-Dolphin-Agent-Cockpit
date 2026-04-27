package metrics

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosurePhase3PreflightScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02j",
		"Phase 3 default-policy preflight gate",
		"SKILL_PD_ROLLOUT_GATE_SCRIPT",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE",
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE",
		"SKILL_PD_RUN_LOCAL_GO_TESTS",
		"skill-progressive-disclosure-rollout-gate.sh",
		"P25-HIGH-02g smoke passed.",
		"Evidence type: production-smoke",
		"Total host tool calls",
		"real traffic is non-zero",
		"Evidence type: authenticated-claudecli-e2e",
		"Authenticated environment: true",
		"TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"must not contain token: ${needle}",
		"overrideSkillsToSummary",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required phase3-preflight token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosurePhase3PreflightScriptPassAndMissingEvidenceFail(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh"
	tempDir := t.TempDir()
	observationPath := filepath.Join(tempDir, "observation.md")
	if err := os.WriteFile(observationPath, []byte(phase3ObservationRows(
		"| 2026-04-26 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | clean |",
		"| 2026-04-27 | abc124 | canary | 24h | 12 | 12 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	)), 0o644); err != nil {
		t.Fatalf("write observation: %v", err)
	}
	productionSmoke := filepath.Join(tempDir, "production-smoke.txt")
	if err := os.WriteFile(productionSmoke, []byte(strings.Join([]string{
		"Evidence type: production-smoke",
		"Total host tool calls: 22",
		"P25-HIGH-02g smoke passed.",
		"real traffic is non-zero",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write smoke evidence: %v", err)
	}
	claudeE2E := filepath.Join(tempDir, "claude-e2e.txt")
	if err := os.WriteFile(claudeE2E, []byte(strings.Join([]string{
		"Evidence type: authenticated-claudecli-e2e",
		"Authenticated environment: true",
		"--- PASS: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"PASS",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write claude evidence: %v", err)
	}

	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+observationPath,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE="+claudeE2E,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected phase3 preflight pass: %v\n%s", err, string(output))
	}
	if body := string(output); !strings.Contains(body, "phase3 preflight passed") || !strings.Contains(body, "rollout gate passed") {
		t.Fatalf("unexpected phase3 preflight output:\n%s", body)
	}

	cmd = exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+observationPath,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
	)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected phase3 preflight fail without Claude E2E evidence:\n%s", string(output))
	}
	if body := string(output); !strings.Contains(body, "authenticated Claude CLI E2E evidence path is required") {
		t.Fatalf("missing-evidence failure output mismatch:\n%s", body)
	}

	zeroTrafficSmoke := filepath.Join(tempDir, "zero-traffic-smoke.txt")
	if err := os.WriteFile(zeroTrafficSmoke, []byte(strings.Join([]string{
		"Evidence type: production-smoke",
		"Total host tool calls: 0",
		"P25-HIGH-02g smoke passed.",
		"real traffic is non-zero",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write zero-traffic smoke evidence: %v", err)
	}
	cmd = exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+observationPath,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+zeroTrafficSmoke,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE="+claudeE2E,
	)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected phase3 preflight fail for zero traffic evidence:\n%s", string(output))
	}
	if body := string(output); !strings.Contains(body, "production smoke evidence field must be positive: Total host tool calls") {
		t.Fatalf("zero-traffic failure output mismatch:\n%s", body)
	}
}

func TestSkillProgressiveDisclosurePhase3EvidenceTemplates(t *testing.T) {
	cases := []struct {
		path     string
		required []string
	}{
		{
			path: "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence.md",
			required: []string{
				"Evidence type: production-smoke",
				"P25-HIGH-02k evidence template",
				"Total host tool calls",
				"positive integer; must be > 0",
				"P25-HIGH-02g smoke passed.",
				"real traffic is non-zero",
				"skill-progressive-disclosure-rollout-smoke.sh",
				"skill-progressive-disclosure-production-smoke-evidence-generate.sh",
			},
		},
		{
			path: "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence.md",
			required: []string{
				"Evidence type: authenticated-claudecli-e2e",
				"P25-HIGH-02k evidence template",
				"Authenticated environment",
				"true",
				"TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
				"PASS",
				"must not contain `SKIP`",
			},
		},
	}
	for _, tc := range cases {
		raw, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		body := string(raw)
		for _, token := range tc.required {
			if !strings.Contains(body, token) {
				t.Fatalf("%s missing required evidence-template token %q", tc.path, token)
			}
		}
	}
}

func phase3ObservationRows(rows ...string) string {
	header := "| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|\n"
	return header + strings.Join(rows, "\n") + fmt.Sprintln()
}
