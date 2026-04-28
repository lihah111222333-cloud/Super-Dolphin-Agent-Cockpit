package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureRolloutDailyScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-daily.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02r",
		"one-command daily rollout observation runner",
		"SKILL_PD_OBSERVATION_FILE",
		"SKILL_PD_ROLLOUT_REPORT_SCRIPT",
		"SKILL_PD_ROLLOUT_APPEND_SCRIPT",
		"SKILL_PD_ROLLOUT_STATUS_SCRIPT",
		"SKILL_PD_DAILY_OUTPUT_DIR",
		"SKILL_PD_DAILY_REQUIRE_REAL_SAMPLE",
		"SKILL_PD_DAILY_DRY_RUN",
		"skill-progressive-disclosure-rollout-report.sh",
		"skill-progressive-disclosure-rollout-append.sh",
		"skill-progressive-disclosure-rollout-status.sh",
		"P25-HIGH-02r rollout daily passed.",
		"report preserved",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
		"merge branches",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing rollout daily token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosureRolloutDailyScriptRunsReportAppendStatus(t *testing.T) {
	script := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-daily.sh"
	tempDir := t.TempDir()
	observation := filepath.Join(tempDir, "rollout-observation.md")
	reportScript := filepath.Join(tempDir, "fake-rollout-report.sh")
	outputDir := filepath.Join(tempDir, "daily-output")

	observationBody := strings.Join([]string{
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
	if err := os.WriteFile(observation, []byte(observationBody), 0o644); err != nil {
		t.Fatalf("write observation: %v", err)
	}

	reportBody := strings.Join([]string{
		"#!/usr/bin/env bash",
		"cat <<'REPORT'",
		"# P25-HIGH-02h rollout observation report",
		"| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |",
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|",
		"| 2026-04-30 | abc125 | canary | 24h | 3 | 3 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | artifact_approval_miss=0 |",
		"REPORT",
		"",
	}, "\n")
	if err := os.WriteFile(reportScript, []byte(reportBody), 0o755); err != nil {
		t.Fatalf("write fake report script: %v", err)
	}

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_ROLLOUT_REPORT_SCRIPT="+reportScript,
		"SKILL_PD_DAILY_OUTPUT_DIR="+outputDir,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected daily pass: %v\n%s", err, string(output))
	}
	body := string(output)
	for _, token := range []string{
		"P25-HIGH-02r rollout daily passed.",
		"P25-HIGH-02p rollout observation append passed.",
		"P25-HIGH-02q rollout status:",
		"sample_days=1",
		"remaining_sample_days=0",
		"Run skill-progressive-disclosure-rollout-gate.sh against the observation file.",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("daily output missing %q\n%s", token, body)
		}
	}

	updated, err := os.ReadFile(observation)
	if err != nil {
		t.Fatalf("read observation: %v", err)
	}
	if !strings.Contains(string(updated), "| 2026-04-30 | abc125 | canary | 24h | 3 | 3 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | artifact_approval_miss=0 |") {
		t.Fatalf("observation missing daily row:\n%s", string(updated))
	}
	for _, path := range []string{
		filepath.Join(outputDir, "rollout-report-"),
		filepath.Join(outputDir, "rollout-append-"),
		filepath.Join(outputDir, "rollout-status-"),
	} {
		matches, err := filepath.Glob(path + "*.md")
		if strings.Contains(path, "rollout-append-") || strings.Contains(path, "rollout-status-") {
			matches, err = filepath.Glob(path + "*.txt")
		}
		if err != nil || len(matches) == 0 {
			t.Fatalf("expected artifact for prefix %s, matches=%v err=%v", path, matches, err)
		}
	}

	duplicate := exec.Command("bash", script)
	duplicate.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_ROLLOUT_REPORT_SCRIPT="+reportScript,
		"SKILL_PD_DAILY_OUTPUT_DIR="+filepath.Join(tempDir, "duplicate-output"),
	)
	dupOutput, err := duplicate.CombinedOutput()
	if err == nil || !strings.Contains(string(dupOutput), "observation row already exists") {
		t.Fatalf("expected duplicate daily fail, got err=%v\n%s", err, string(dupOutput))
	}
}
