package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureProductionSmokeEvidenceGenerateScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence-generate.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02s",
		"SKILL_PD_ROLLOUT_REPORT_FILE",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE_OUT",
		"SKILL_PD_OPERATOR",
		"SUPER_DOLPHIN_METRICS_URL",
		"PROMETHEUS_URL",
		"ALERTMANAGER_URL",
		"Evidence type: production-smoke",
		"Total host tool calls",
		"P25-HIGH-02g rollout smoke passed.",
		"real traffic is non-zero",
		"raw production smoke output block is required",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing production smoke evidence generator token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosureProductionSmokeEvidenceGeneratePassAndFailClosed(t *testing.T) {
	script := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence-generate.sh"
	tempDir := t.TempDir()
	report := filepath.Join(tempDir, "rollout-report.md")
	out := filepath.Join(tempDir, "production-smoke-evidence.md")
	reportBody := strings.Join([]string{
		"# P25-HIGH-02h rollout observation report",
		"",
		"- Prometheus URL: http://prometheus.example",
		"- Query window: 24h",
		"",
		"| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |",
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|",
		"| 2026-04-28 | abc123 | canary | 24h | 5 | 5 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | artifact_approval_miss=0 |",
		"",
		"## Production smoke output",
		"",
		"```",
		"P25-HIGH-02g rollout smoke passed.",
		"targets up; rules loaded; alertmanager ready",
		"```",
		"",
	}, "\n")
	if err := os.WriteFile(report, []byte(reportBody), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_ROLLOUT_REPORT_FILE="+report,
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE_OUT="+out,
		"SKILL_PD_OPERATOR=release-owner",
		"SUPER_DOLPHIN_METRICS_URL=http://metrics.example/metrics",
		"ALERTMANAGER_URL=http://alertmanager.example",
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
		"Evidence type: production-smoke",
		"Evidence date: 2026-04-28",
		"Version / commit: abc123",
		"Operator: release-owner",
		"Metrics URL: http://metrics.example/metrics",
		"Prometheus URL: http://prometheus.example",
		"Alertmanager URL: http://alertmanager.example",
		"Observation row date: 2026-04-28",
		"Total host tool calls: 5",
		"Production smoke result: P25-HIGH-02g smoke passed.",
		"Real traffic statement: real traffic is non-zero",
		"targets up; rules loaded; alertmanager ready",
	} {
		if !strings.Contains(evidence, token) {
			t.Fatalf("generated evidence missing %q\n%s", token, evidence)
		}
	}
	if strings.Contains(evidence, "TODO") {
		t.Fatalf("generated evidence contains TODO:\n%s", evidence)
	}

	zeroReport := filepath.Join(tempDir, "zero-report.md")
	if err := os.WriteFile(zeroReport, []byte(strings.Replace(reportBody, "| 5 | 5 |", "| 0 | 0 |", 1)), 0o644); err != nil {
		t.Fatalf("write zero report: %v", err)
	}
	zero := exec.Command("bash", script)
	zero.Env = append(os.Environ(),
		"SKILL_PD_ROLLOUT_REPORT_FILE="+zeroReport,
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE_OUT="+filepath.Join(tempDir, "zero-evidence.md"),
		"SKILL_PD_OPERATOR=release-owner",
		"SUPER_DOLPHIN_METRICS_URL=http://metrics.example/metrics",
		"ALERTMANAGER_URL=http://alertmanager.example",
	)
	zeroOutput, err := zero.CombinedOutput()
	if err == nil || !strings.Contains(string(zeroOutput), "Total host tool calls must be positive") {
		t.Fatalf("expected zero-traffic fail, got err=%v\n%s", err, string(zeroOutput))
	}

	noPassReport := filepath.Join(tempDir, "no-pass-report.md")
	if err := os.WriteFile(noPassReport, []byte(strings.Replace(reportBody, "P25-HIGH-02g rollout smoke passed.", "smoke failed", 1)), 0o644); err != nil {
		t.Fatalf("write no-pass report: %v", err)
	}
	noPass := exec.Command("bash", script)
	noPass.Env = append(os.Environ(),
		"SKILL_PD_ROLLOUT_REPORT_FILE="+noPassReport,
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE_OUT="+filepath.Join(tempDir, "no-pass-evidence.md"),
		"SKILL_PD_OPERATOR=release-owner",
		"SUPER_DOLPHIN_METRICS_URL=http://metrics.example/metrics",
		"ALERTMANAGER_URL=http://alertmanager.example",
	)
	noPassOutput, err := noPass.CombinedOutput()
	if err == nil || !strings.Contains(string(noPassOutput), "raw production smoke output missing") {
		t.Fatalf("expected missing smoke pass fail, got err=%v\n%s", err, string(noPassOutput))
	}
}
