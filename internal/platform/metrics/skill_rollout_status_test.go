package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureRolloutStatusScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-status.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02q",
		"SKILL_PD_OBSERVATION_FILE",
		"SKILL_PD_REQUIRED_SAMPLE_DAYS",
		"SKILL_PD_MAX_NON_OK_RATE",
		"sample_days=",
		"remaining_sample_days=",
		"rollback_drill_pass=",
		"blocker_count=",
		"Next phase actions:",
		"skill-progressive-disclosure-rollout-append.sh",
		"skill-progressive-disclosure-rollout-gate.sh",
		"skill-progressive-disclosure-phase3-evidence-collect.sh",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
		"merge branches",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing rollout status token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosureRolloutStatusScriptNextActions(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-status.sh"
	tempDir := t.TempDir()
	observation := filepath.Join(tempDir, "observation.md")
	if err := os.WriteFile(observation, []byte(observationRows(
		"| 2026-04-28 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	)), 0o644); err != nil {
		t.Fatalf("write observation: %v", err)
	}

	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected status pass: %v\n%s", err, string(output))
	}
	body := string(output)
	for _, token := range []string{
		"P25-HIGH-02q rollout status:",
		"sample_days=1",
		"required_sample_days=2",
		"remaining_sample_days=1",
		"rollback_drill_pass=false",
		"blocker_count=0",
		"Continue daily production smoke/report/append until 1 more real sample day",
		"Schedule and record at least one rollback drill PASS",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("status output missing %q\n%s", token, body)
		}
	}

	readyObservation := filepath.Join(tempDir, "ready-observation.md")
	if err := os.WriteFile(readyObservation, []byte(observationRows(
		"| 2026-04-28 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | clean |",
		"| 2026-04-29 | abc124 | canary | 24h | 12 | 12 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	)), 0o644); err != nil {
		t.Fatalf("write ready observation: %v", err)
	}
	ready := exec.Command("bash", path)
	ready.Env = append(os.Environ(),
		"SKILL_PD_OBSERVATION_FILE="+readyObservation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
	)
	readyOutput, err := ready.CombinedOutput()
	if err != nil {
		t.Fatalf("expected ready status pass: %v\n%s", err, string(readyOutput))
	}
	readyBody := string(readyOutput)
	for _, token := range []string{
		"sample_days=2",
		"remaining_sample_days=0",
		"rollback_drill_pass=true",
		"Run skill-progressive-disclosure-rollout-gate.sh against the observation file.",
		"Collect production-smoke and authenticated Claude CLI E2E evidence.",
		"Run skill-progressive-disclosure-phase3-evidence-collect.sh",
	} {
		if !strings.Contains(readyBody, token) {
			t.Fatalf("ready status output missing %q\n%s", token, readyBody)
		}
	}
}
