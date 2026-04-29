package metrics

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureRolloutGateScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-gate.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02i",
		"SKILL_PD_OBSERVATION_FILE",
		"SKILL_PD_REQUIRED_SAMPLE_DAYS",
		"SKILL_PD_MAX_NON_OK_RATE",
		"30-day rollout gate verifier",
		"Total host tool calls",
		"Manual smoke result",
		"Production Prometheus smoke result",
		"Rollback drill result",
		"no-sample days",
		"non-ok rate",
		"accepted incident",
		"protocol-drift fix merged",
		"rollback_drill_pass=true",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required rollout-gate token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosureRolloutGateScriptPassAndNoSampleFail(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-gate.sh"
	tempDir := t.TempDir()
	passingObservation := filepath.Join(tempDir, "passing-observation.md")
	if err := os.WriteFile(passingObservation, []byte(observationRows(
		"| 2026-04-26 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | clean |",
		"| 2026-04-27 | abc124 | canary | 24h | 12 | 12 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	)), 0o644); err != nil {
		t.Fatalf("write passing observation: %v", err)
	}
	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+passingObservation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected rollout gate pass: %v\n%s", err, string(output))
	}
	if body := string(output); !strings.Contains(body, "rollout gate passed") || !strings.Contains(body, "sample_days=2") {
		t.Fatalf("unexpected pass output:\n%s", body)
	}

	noSampleObservation := filepath.Join(tempDir, "no-sample-observation.md")
	if err := os.WriteFile(noSampleObservation, []byte(observationRows(
		"| 2026-04-27 | abc123 | false | 24h | 0 | 0 | 0 | 0 | 0 | 0 | `SKIP(no samples)` | `SKIP(not applied)` | `SKIP(no release window)` | none | hold | `无样本` / `no samples`; gate remains open |",
	)), 0o644); err != nil {
		t.Fatalf("write no-sample observation: %v", err)
	}
	cmd = exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+noSampleObservation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=1",
	)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected rollout gate fail for no-sample observation:\n%s", string(output))
	}
	body := string(output)
	for _, token := range []string{"rollout gate failed", "sample days 0 < required 1", "no-sample days=1"} {
		if !strings.Contains(body, token) {
			t.Fatalf("rollout gate failure output missing %q\n%s", token, body)
		}
	}
}

func observationRows(rows ...string) string {
	header := "| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|\n"
	return header + strings.Join(rows, "\n") + fmt.Sprintln()
}
