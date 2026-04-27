package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosurePhase3HandoffReportScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-handoff-report.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02w",
		"review-ready Phase 3 evidence handoff report",
		"SKILL_PD_EVIDENCE_BUNDLE_DIR",
		"SKILL_PD_HANDOFF_REPORT_OUT",
		"Evidence type: phase3-handoff-report",
		"phase3_collect_ready=true",
		"blocker_count=0",
		"SHA256",
		"phase3-evidence-status-output.txt",
		"phase3-evidence-collect-output.txt",
		"P25-HIGH-02n evidence collect passed.",
		"P25-HIGH-02l evidence bundle passed.",
		"P25-HIGH-02j phase3 preflight passed.",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
		"merge branches",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing phase3 handoff report token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosurePhase3HandoffReportPassAndMissingFileFail(t *testing.T) {
	script := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-handoff-report.sh"
	bundleDir := t.TempDir()
	writePhase3HandoffFile(t, bundleDir, "production-smoke-evidence.md", strings.Join([]string{
		"Evidence type: production-smoke",
		"Total host tool calls: 22",
		"P25-HIGH-02g smoke passed.",
		"real traffic is non-zero",
		"",
	}, "\n"))
	writePhase3HandoffFile(t, bundleDir, "claudecli-e2e-evidence.md", strings.Join([]string{
		"Evidence type: authenticated-claudecli-e2e",
		"Authenticated environment: true",
		"Test name: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"Result: PASS",
		"--- PASS: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"",
	}, "\n"))
	writePhase3HandoffFile(t, bundleDir, "rollout-observation.md", phase3HandoffObservationRows(
		"| 2026-04-26 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | clean |",
		"| 2026-04-27 | abc124 | canary | 24h | 12 | 12 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	))
	writePhase3HandoffFile(t, bundleDir, "rollout-gate-output.txt", "P25-HIGH-02i rollout gate passed: sample_days=2 total_host_tool_calls=22 non_ok_calls=0 non_ok_rate=0.0000 required_sample_days=2 rollback_drill_pass=true\n")
	writePhase3HandoffFile(t, bundleDir, "phase3-preflight-output.txt", "P25-HIGH-02j phase3 preflight passed.\n30-day rollout gate verifier passed with real samples.\nThis script does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE and does not delete overrideSkillsToSummary.\n")
	writePhase3HandoffFile(t, bundleDir, "evidence-bundle-output.txt", "P25-HIGH-02l evidence bundle passed.\n")
	writePhase3HandoffFile(t, bundleDir, "manifest.md", "Evidence type: phase3-evidence-bundle\nP25-HIGH-02l evidence bundle passed.\n")
	writePhase3HandoffFile(t, bundleDir, "phase3-evidence-status-output.txt", strings.Join([]string{
		"P25-HIGH-02u phase3 evidence status:",
		"sample_days=2",
		"required_sample_days=2",
		"remaining_sample_days=0",
		"total_host_tool_calls=22",
		"non_ok_rate=0.0000",
		"rollback_drill_pass=true",
		"last_sample_date=2026-04-27",
		"phase3_collect_ready=true",
		"blocker_count=0",
		"",
	}, "\n"))
	writePhase3HandoffFile(t, bundleDir, "phase3-evidence-collect-output.txt", "P25-HIGH-02n evidence collect passed.\nevidence bundle passed\n")

	report := filepath.Join(bundleDir, "handoff.md")
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_EVIDENCE_BUNDLE_DIR="+bundleDir,
		"SKILL_PD_HANDOFF_REPORT_OUT="+report,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected handoff report pass: %v\n%s", err, string(output))
	}
	body, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	reportBody := string(body)
	for _, token := range []string{
		"Evidence type: phase3-handoff-report",
		"phase3_collect_ready: true",
		"blocker_count: 0",
		"sample_days: 2",
		"total_host_tool_calls: 22",
		"rollout-gate-output.txt",
		"SHA256",
		"separate owner-approved Phase 3 PR",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
	} {
		if !strings.Contains(reportBody, token) {
			t.Fatalf("handoff report missing %q\n%s", token, reportBody)
		}
	}
	if strings.Contains(reportBody, "TODO") {
		t.Fatalf("handoff report contains TODO:\n%s", reportBody)
	}

	missingDir := t.TempDir()
	writePhase3HandoffFile(t, missingDir, "production-smoke-evidence.md", "Evidence type: production-smoke\nP25-HIGH-02g smoke passed.\nreal traffic is non-zero\n")
	cmd = exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "SKILL_PD_EVIDENCE_BUNDLE_DIR="+missingDir)
	missingOutput, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected handoff report fail for missing files:\n%s", string(missingOutput))
	}
	if body := string(missingOutput); !strings.Contains(body, "missing bundle file: claudecli-e2e-evidence.md") {
		t.Fatalf("missing-file failure mismatch:\n%s", body)
	}
}

func writePhase3HandoffFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func phase3HandoffObservationRows(rows ...string) string {
	header := "| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|\n"
	return header + strings.Join(rows, "\n") + "\n"
}
