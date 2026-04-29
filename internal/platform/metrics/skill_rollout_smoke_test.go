package metrics

import (
	"os"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureRolloutSmokeScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-smoke.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02g",
		"SUPER_DOLPHIN_METRICS_URL",
		"PROMETHEUS_URL",
		"ALERTMANAGER_URL",
		"SKILL_PD_PROMETHEUS_JOB",
		"host_tool_calls_total",
		"enrich_failures_total",
		"/api/v1/targets?state=active",
		`\"health\":\"up\"`,
		"/api/v1/rules",
		"SkillHostToolHighErrorRate",
		"SkillHostToolCWDMissing",
		"SkillHostToolApprovalRequiredStuck",
		"SkillToolEnrichFailures",
		"/-/ready",
		"/api/v2/status",
		"30-day rollout observation window",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required rollout-smoke token %q", path, token)
		}
	}
}
