package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureRolloutAppendScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-append.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02p",
		"SKILL_PD_OBSERVATION_FILE",
		"SKILL_PD_ROLLOUT_REPORT_SCRIPT",
		"SKILL_PD_ROLLOUT_REPORT_FILE",
		"SKILL_PD_APPEND_DRY_RUN",
		"SKILL_PD_APPEND_REQUIRE_REAL_SAMPLE",
		"skill-progressive-disclosure-rollout-report.sh",
		"## Daily observation row template",
		"## 30-day summary template",
		"no-sample row decision",
		"decision=continue requires manual/prometheus/rollback PASS",
		"observation row already exists",
		"P25-HIGH-02p rollout observation append passed.",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing rollout append token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosureRolloutAppendScriptPassDuplicateAndNoSampleFail(t *testing.T) {
	script := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-append.sh"
	tempDir := t.TempDir()
	obsPath := filepath.Join(tempDir, "rollout-observation.md")
	reportPath := filepath.Join(tempDir, "report.md")

	observation := strings.Join([]string{
		"# Skill Progressive Disclosure Rollout Observation",
		"",
		"## Daily observation row template",
		"",
		"| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |",
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|",
		"| YYYY-MM-DD | `<commit>` | `false` / `true` / canary | 24h | 0 | 0 | 0 | 0 | 0 | 0 | `SKIP(no samples)` | `SKIP(not applied)` | `SKIP(no release window)` | none | hold | `无样本` / `no samples`; gate remains open |",
		"",
		"## 30-day summary template",
		"",
		"Final decision: `continue` / `hold` / `rollback`.",
		"",
	}, "\n")
	if err := os.WriteFile(obsPath, []byte(observation), 0o644); err != nil {
		t.Fatalf("write observation: %v", err)
	}

	report := strings.Join([]string{
		"# P25-HIGH-02h rollout observation report",
		"| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |",
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|",
		"| 2026-04-28 | abc123 | false | 24h | 7 | 7 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | artifact_approval_miss=0 |",
		"",
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+obsPath,
		"SKILL_PD_ROLLOUT_REPORT_FILE="+reportPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected append pass: %v\n%s", err, string(output))
	}
	body := string(output)
	if !strings.Contains(body, "P25-HIGH-02p rollout observation append passed.") {
		t.Fatalf("append output missing pass token:\n%s", body)
	}
	updated, err := os.ReadFile(obsPath)
	if err != nil {
		t.Fatalf("read updated observation: %v", err)
	}
	if !strings.Contains(string(updated), "| 2026-04-28 | abc123 | false | 24h | 7 | 7 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | artifact_approval_miss=0 |") {
		t.Fatalf("observation missing appended row:\n%s", string(updated))
	}

	dup := exec.Command("bash", script)
	dup.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+obsPath,
		"SKILL_PD_ROLLOUT_REPORT_FILE="+reportPath,
	)
	dupOutput, err := dup.CombinedOutput()
	if err == nil || !strings.Contains(string(dupOutput), "observation row already exists") {
		t.Fatalf("expected duplicate fail, got err=%v\n%s", err, string(dupOutput))
	}

	noSampleObs := filepath.Join(tempDir, "rollout-observation-nosample.md")
	if err := os.WriteFile(noSampleObs, []byte(observation), 0o644); err != nil {
		t.Fatalf("write no-sample observation: %v", err)
	}
	noSampleReport := filepath.Join(tempDir, "report-nosample.md")
	noSample := strings.Join([]string{
		"# P25-HIGH-02h rollout observation report",
		"| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |",
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|",
		"| 2026-04-29 | abc124 | false | 24h | 0 | 0 | 0 | 0 | 0 | 0 | `SKIP(no samples)` | `PASS` | `SKIP(no release window)` | none | hold | 无样本 / no samples; gate remains open; artifact_approval_miss=0 |",
		"",
	}, "\n")
	if err := os.WriteFile(noSampleReport, []byte(noSample), 0o644); err != nil {
		t.Fatalf("write no-sample report: %v", err)
	}
	noSampleCmd := exec.Command("bash", script)
	noSampleCmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+noSampleObs,
		"SKILL_PD_ROLLOUT_REPORT_FILE="+noSampleReport,
		"SKILL_PD_APPEND_REQUIRE_REAL_SAMPLE=true",
	)
	noSampleOutput, err := noSampleCmd.CombinedOutput()
	if err == nil || !strings.Contains(string(noSampleOutput), "total host tool calls is 0") {
		t.Fatalf("expected no-sample require-real fail, got err=%v\n%s", err, string(noSampleOutput))
	}
}
