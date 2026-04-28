package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosurePhase3EvidenceStatusScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-status.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02u",
		"read-only Phase 3 evidence readiness status helper",
		"SKILL_PD_OBSERVATION_FILE",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE",
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE",
		"SKILL_PD_REQUIRED_SAMPLE_DAYS",
		"SKILL_PD_PHASE3_STATUS_FAIL_ON_BLOCKERS",
		"rollout_gate_ready=",
		"production_smoke_evidence_ready=",
		"claudecli_e2e_evidence_ready=",
		"phase3_collect_ready=",
		"blocker_count=",
		"skill-progressive-disclosure-production-smoke-evidence-generate.sh",
		"skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh",
		"skill-progressive-disclosure-phase3-evidence-collect.sh",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
		"merge branches",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing phase3 evidence status token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosurePhase3EvidenceStatusReadyAndMissing(t *testing.T) {
	script := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-status.sh"
	tempDir := t.TempDir()
	productionSmoke := filepath.Join(tempDir, "production-smoke-evidence.md")
	writePhase3EvidenceStatusFile(t, productionSmoke, strings.Join([]string{
		"Evidence type: production-smoke",
		"Total host tool calls: 22",
		"P25-HIGH-02g smoke passed.",
		"real traffic is non-zero",
		"raw smoke output: PASS",
		"",
	}, "\n"))
	claudeE2E := filepath.Join(tempDir, "claudecli-e2e-evidence.md")
	writePhase3EvidenceStatusFile(t, claudeE2E, strings.Join([]string{
		"Evidence type: authenticated-claudecli-e2e",
		"Authenticated environment: true",
		"Test name: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"Result: PASS",
		"Skip status: none",
		"--- PASS: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"",
	}, "\n"))
	observation := filepath.Join(tempDir, "rollout-observation.md")
	writePhase3EvidenceStatusFile(t, observation, phase3EvidenceStatusObservationRows(
		"| 2026-04-26 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | clean |",
		"| 2026-04-27 | abc124 | canary | 24h | 12 | 12 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	))

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE="+claudeE2E,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected phase3 evidence status pass: %v\n%s", err, string(output))
	}
	body := string(output)
	for _, token := range []string{
		"P25-HIGH-02u phase3 evidence status:",
		"sample_days=2",
		"rollout_gate_ready=true",
		"production_smoke_evidence_ready=true",
		"claudecli_e2e_evidence_ready=true",
		"phase3_collect_ready=true",
		"blocker_count=0",
		"Run skill-progressive-disclosure-phase3-evidence-collect.sh",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("ready status output missing %q\n%s", token, body)
		}
	}

	missingClaude := exec.Command("bash", script)
	missingClaude.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
		"SKILL_PD_PHASE3_STATUS_FAIL_ON_BLOCKERS=true",
	)
	missingOutput, err := missingClaude.CombinedOutput()
	if err == nil {
		t.Fatalf("expected fail-on-blockers to fail without Claude evidence:\n%s", string(missingOutput))
	}
	missingBody := string(missingOutput)
	for _, token := range []string{
		"claudecli_e2e_evidence_ready=false",
		"phase3_collect_ready=false",
		"authenticated Claude CLI E2E evidence path is required",
		"Generate authenticated Claude CLI E2E evidence",
		"Do not open a Phase 3 default-policy PR",
	} {
		if !strings.Contains(missingBody, token) {
			t.Fatalf("missing-evidence status output missing %q\n%s", token, missingBody)
		}
	}
}

func writePhase3EvidenceStatusFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func phase3EvidenceStatusObservationRows(rows ...string) string {
	header := "| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|\n"
	return header + strings.Join(rows, "\n") + "\n"
}
